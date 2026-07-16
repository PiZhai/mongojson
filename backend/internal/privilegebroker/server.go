package privilegebroker

import (
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"net/http"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

var hexDigestPattern = regexp.MustCompile(`^[a-f0-9]{64}$`)

type ServerConfig struct {
	ListenAddress  string
	PolicyPath     string
	StatePath      string
	AuditPath      string
	CheckpointPath string
	ClientKey      []byte
	ControlKey     []byte
	SigningKey     ed25519.PrivateKey
	GrantTTL       time.Duration
	RequestSkew    time.Duration
}

type Server struct {
	policy      *LoadedPolicy
	clientKey   []byte
	controlKey  []byte
	privateKey  ed25519.PrivateKey
	publicKey   ed25519.PublicKey
	keyID       string
	instanceID  string
	grantTTL    time.Duration
	requestSkew time.Duration
	statePath   string
	audit       *auditLog

	stateMu sync.RWMutex
	state   brokerState

	nonceMu sync.Mutex
	nonces  map[string]time.Time

	tokenMu            sync.Mutex
	consumed           map[string]time.Time
	approvalMu         sync.Mutex
	consumedApprovals  map[string]struct{}
	webAuthnSignCounts map[string]uint32

	activeMu sync.Mutex
	active   map[string]context.CancelFunc

	now func() time.Time
}

func NewServer(config ServerConfig) (*Server, error) {
	if len(config.ClientKey) < 32 {
		return nil, fmt.Errorf("broker client key must contain at least 32 bytes")
	}
	if len(config.ControlKey) < 32 {
		return nil, fmt.Errorf("broker control key must contain at least 32 bytes")
	}
	if hmac.Equal(config.ClientKey, config.ControlKey) {
		return nil, fmt.Errorf("broker client and control keys must be distinct")
	}
	if len(config.SigningKey) != ed25519.PrivateKeySize {
		return nil, fmt.Errorf("broker signing key is invalid")
	}
	policy, err := LoadPolicy(config.PolicyPath)
	if err != nil {
		return nil, err
	}
	state, statePath, err := loadBrokerState(config.StatePath)
	if err != nil {
		return nil, err
	}
	audit, err := newProtectedAuditLog(config.AuditPath, config.CheckpointPath, statePath, state, config.SigningKey)
	if err != nil {
		return nil, err
	}
	instanceID, err := randomID()
	if err != nil {
		return nil, err
	}
	grantTTL := config.GrantTTL
	if grantTTL == 0 {
		grantTTL = 30 * time.Second
	}
	if grantTTL < 5*time.Second || grantTTL > 2*time.Minute {
		return nil, fmt.Errorf("broker grant TTL must be between 5s and 2m")
	}
	requestSkew := config.RequestSkew
	if requestSkew == 0 {
		requestSkew = 30 * time.Second
	}
	if requestSkew < 5*time.Second || requestSkew > 5*time.Minute {
		return nil, fmt.Errorf("broker request skew must be between 5s and 5m")
	}
	publicKey := config.SigningKey.Public().(ed25519.PublicKey)
	server := &Server{
		policy: policy, clientKey: append([]byte(nil), config.ClientKey...), controlKey: append([]byte(nil), config.ControlKey...),
		privateKey: append(ed25519.PrivateKey(nil), config.SigningKey...),
		publicKey:  append(ed25519.PublicKey(nil), publicKey...), keyID: publicKeyID(publicKey),
		instanceID: instanceID, grantTTL: grantTTL, requestSkew: requestSkew,
		statePath: statePath, state: state, audit: audit,
		nonces: map[string]time.Time{}, consumed: map[string]time.Time{},
		active: map[string]context.CancelFunc{}, consumedApprovals: audit.ConsumedApprovalProofs(),
		webAuthnSignCounts: audit.WebAuthnSignCounts(),
		now:                func() time.Time { return time.Now().UTC() },
	}
	if err := server.audit.Append(AuditRecord{
		Type: "broker.started", Generation: state.Generation, Outcome: "ready",
		Details: map[string]any{"instance_id": instanceID, "policy_digest": policy.Digest, "stopped": state.Stopped},
	}); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *Server) Run(ctx context.Context, address string) error {
	address = strings.TrimSpace(address)
	if address == "" {
		address = "127.0.0.1:18100"
	}
	if err := validateLoopbackAddress(address); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return fmt.Errorf("listen for privilege broker: %w", err)
	}
	httpServer := &http.Server{
		Handler: s, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		WriteTimeout: 0, IdleTimeout: 30 * time.Second, MaxHeaderBytes: 16 << 10,
	}
	stopped := make(chan error, 1)
	go func() { stopped <- httpServer.Serve(listener) }()
	select {
	case <-ctx.Done():
		s.cancelActive()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		err := <-stopped
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case err := <-stopped:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	}
}

func (s *Server) ServeHTTP(response http.ResponseWriter, request *http.Request) {
	if !requestFromLoopback(request.RemoteAddr) {
		writeBrokerError(response, http.StatusForbidden, "remote_denied", "privilege broker only accepts loopback requests")
		return
	}
	if request.URL.RawQuery != "" {
		writeBrokerError(response, http.StatusBadRequest, "query_denied", "privilege broker endpoints do not accept query parameters")
		return
	}
	if request.Method == http.MethodGet && request.URL.Path == "/v1/status" {
		s.handleAuthenticated(response, request, s.handleStatus)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/grants" {
		s.handleAuthenticated(response, request, s.handleGrant)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/execute" {
		s.handleAuthenticated(response, request, s.handleExecute)
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/control/stop" {
		s.handleAuthenticated(response, request, func(w http.ResponseWriter, r *http.Request, body []byte) { s.handleControl(w, r, body, true) })
		return
	}
	if request.Method == http.MethodPost && request.URL.Path == "/v1/control/resume" {
		s.handleAuthenticatedWithKey(response, request, s.controlKey, func(w http.ResponseWriter, r *http.Request, body []byte) { s.handleControl(w, r, body, false) })
		return
	}
	writeBrokerError(response, http.StatusNotFound, "not_found", "broker endpoint not found")
}

func (s *Server) handleAuthenticated(response http.ResponseWriter, request *http.Request, handler func(http.ResponseWriter, *http.Request, []byte)) {
	s.handleAuthenticatedWithKey(response, request, s.clientKey, handler)
}

func (s *Server) handleAuthenticatedWithKey(response http.ResponseWriter, request *http.Request, key []byte, handler func(http.ResponseWriter, *http.Request, []byte)) {
	body, err := io.ReadAll(http.MaxBytesReader(response, request.Body, 128<<10))
	if err != nil {
		writeBrokerError(response, http.StatusRequestEntityTooLarge, "request_too_large", "broker request exceeds 128 KiB")
		return
	}
	timestampText := strings.TrimSpace(request.Header.Get(HeaderTimestamp))
	nonce := strings.TrimSpace(request.Header.Get(HeaderNonce))
	signature := strings.TrimSpace(request.Header.Get(HeaderSignature))
	timestamp, err := strconv.ParseInt(timestampText, 10, 64)
	if err != nil || len(nonce) < 16 || len(nonce) > 128 {
		writeBrokerError(response, http.StatusUnauthorized, "invalid_auth", "broker request authentication is invalid")
		return
	}
	now := s.now()
	requestTime := time.Unix(timestamp, 0).UTC()
	if delta := now.Sub(requestTime); delta > s.requestSkew || delta < -s.requestSkew {
		writeBrokerError(response, http.StatusUnauthorized, "stale_request", "broker request timestamp is outside the allowed window")
		return
	}
	if !verifyRequestSignature(key, request.Method, request.URL.Path, timestampText, nonce, signature, body) {
		writeBrokerError(response, http.StatusUnauthorized, "invalid_auth", "broker request signature is invalid")
		return
	}
	if !s.consumeRequestNonce(nonce, now) {
		writeBrokerError(response, http.StatusConflict, "request_replayed", "broker request nonce was already used")
		return
	}
	handler(response, request, body)
}

func (s *Server) consumeRequestNonce(nonce string, now time.Time) bool {
	s.nonceMu.Lock()
	defer s.nonceMu.Unlock()
	for key, expires := range s.nonces {
		if !expires.After(now) {
			delete(s.nonces, key)
		}
	}
	if _, exists := s.nonces[nonce]; exists {
		return false
	}
	s.nonces[nonce] = now.Add(2 * s.requestSkew)
	return true
}

func (s *Server) handleStatus(response http.ResponseWriter, _ *http.Request, _ []byte) {
	status, err := s.signedStatus()
	if err != nil {
		writeBrokerError(response, http.StatusInternalServerError, "status_failed", err.Error())
		return
	}
	writeBrokerJSON(response, http.StatusOK, status)
}

func (s *Server) handleGrant(response http.ResponseWriter, _ *http.Request, body []byte) {
	var input GrantRequest
	if err := decodeStrictJSON(body, &input); err != nil {
		writeBrokerError(response, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	if err := normalizeGrantRequest(&input); err != nil {
		writeBrokerError(response, http.StatusBadRequest, "invalid_grant", err.Error())
		return
	}
	capability, found := s.policy.Capability(input.Capability)
	if !found {
		s.auditDenied("grant.denied", input.Subject, input.Capability, input.ControlGeneration, "capability_not_found")
		writeBrokerError(response, http.StatusForbidden, "capability_denied", "broker capability is missing or disabled")
		return
	}
	state := s.currentState()
	if state.Stopped {
		s.auditDenied("grant.denied", input.Subject, capability.Name, input.ControlGeneration, "emergency_stopped")
		writeBrokerError(response, http.StatusLocked, "emergency_stopped", "privilege broker emergency stop is active")
		return
	}
	if input.ControlGeneration != state.Generation {
		s.auditDenied("grant.denied", input.Subject, capability.Name, input.ControlGeneration, "generation_mismatch")
		writeBrokerError(response, http.StatusConflict, "generation_mismatch", "control generation does not match privilege broker state")
		return
	}
	if err := VerifyApprovalProof(s.policy.PublicApprovalAuthorities(), input.ApprovalProof, ApprovalProofExpectation{
		Subject: input.Subject, PlanHash: input.PlanHash, Capability: capability.Name,
		ControlGeneration: input.ControlGeneration,
	}, s.now()); err != nil {
		s.auditDenied("grant.denied", input.Subject, capability.Name, input.ControlGeneration, "invalid_approval_proof")
		writeBrokerError(response, http.StatusForbidden, "invalid_approval_proof", err.Error())
		return
	}
	if input.ApprovalRef != input.ApprovalProof.Claims.ProofID {
		writeBrokerError(response, http.StatusBadRequest, "invalid_approval_proof", "approval_ref must equal the signed approval proof id")
		return
	}
	s.approvalMu.Lock()
	defer s.approvalMu.Unlock()
	if _, consumed := s.consumedApprovals[input.ApprovalProof.Claims.ProofID]; consumed {
		s.auditDenied("grant.denied", input.Subject, capability.Name, input.ControlGeneration, "approval_proof_replayed")
		writeBrokerError(response, http.StatusConflict, "approval_proof_replayed", "signed approval proof was already consumed")
		return
	}
	var webAuthnCount uint32
	if input.ApprovalProof.WebAuthn != nil {
		var countErr error
		webAuthnCount, countErr = webAuthnSignCount(*input.ApprovalProof.WebAuthn)
		if countErr != nil {
			writeBrokerError(response, http.StatusForbidden, "invalid_approval_proof", countErr.Error())
			return
		}
		previous := s.webAuthnSignCounts[input.ApprovalProof.KeyID]
		if countErr = validateWebAuthnSignCount(previous, webAuthnCount); countErr != nil {
			s.auditDenied("grant.denied", input.Subject, capability.Name, input.ControlGeneration, "webauthn_counter_rollback")
			writeBrokerError(response, http.StatusConflict, "webauthn_counter_rollback", countErr.Error())
			return
		}
	}
	tokenID, err := randomID()
	if err != nil {
		writeBrokerError(response, http.StatusInternalServerError, "grant_failed", err.Error())
		return
	}
	now := s.now()
	claims := CapabilityTokenClaims{
		TokenID: tokenID, BrokerInstanceID: s.instanceID,
		Capability: capability.Name, CapabilityDigest: capability.digest,
		Subject: input.Subject, PlanHash: input.PlanHash, ApprovalRef: input.ApprovalRef,
		ApprovalProofID: input.ApprovalProof.Claims.ProofID, ApprovalKeyID: input.ApprovalProof.KeyID,
		ApprovalExpiresAt: input.ApprovalProof.Claims.ExpiresAt,
		ControlGeneration: input.ControlGeneration, IssuedAt: now, ExpiresAt: now.Add(s.grantTTL),
	}
	token, err := encodeSignedToken(s.privateKey, claims)
	if err != nil {
		writeBrokerError(response, http.StatusInternalServerError, "grant_failed", err.Error())
		return
	}
	if err := s.audit.Append(AuditRecord{
		Type: "grant.issued", Subject: claims.Subject, Capability: claims.Capability,
		Generation: claims.ControlGeneration, Outcome: "issued",
		Details: map[string]any{"token_id": tokenID, "plan_hash": claims.PlanHash, "approval_ref": claims.ApprovalRef,
			"approval_proof_id": claims.ApprovalProofID, "approval_key_id": claims.ApprovalKeyID,
			"approval_expires_at": claims.ApprovalExpiresAt, "expires_at": claims.ExpiresAt,
			"webauthn_sign_count": webAuthnCount},
	}); err != nil {
		writeBrokerError(response, http.StatusInternalServerError, "audit_failed", err.Error())
		return
	}
	s.consumedApprovals[claims.ApprovalProofID] = struct{}{}
	if webAuthnCount > 0 {
		s.webAuthnSignCounts[claims.ApprovalKeyID] = webAuthnCount
	}
	writeBrokerJSON(response, http.StatusCreated, GrantResponse{Token: token, Claims: claims, KeyID: s.keyID})
}

func (s *Server) handleExecute(response http.ResponseWriter, request *http.Request, body []byte) {
	var input ExecuteRequest
	if err := decodeStrictJSON(body, &input); err != nil {
		writeBrokerError(response, http.StatusBadRequest, "invalid_execute", err.Error())
		return
	}
	if err := validateExecuteRequest(input); err != nil {
		writeBrokerError(response, http.StatusBadRequest, "invalid_execute", err.Error())
		return
	}
	claims, err := decodeSignedToken(s.publicKey, input.Token)
	if err != nil {
		writeBrokerError(response, http.StatusUnauthorized, "invalid_token", err.Error())
		return
	}
	if err := s.validateExecutionClaims(input, claims); err != nil {
		s.auditDenied("execute.denied", input.Subject, input.Capability, input.ControlGeneration, brokerErrorCode(err))
		status := http.StatusForbidden
		if errors.Is(err, errTokenReplay) || errors.Is(err, errGenerationMismatch) {
			status = http.StatusConflict
		}
		writeBrokerError(response, status, brokerErrorCode(err), err.Error())
		return
	}
	capability, found := s.policy.Capability(claims.Capability)
	if !found || capability.digest != claims.CapabilityDigest {
		writeBrokerError(response, http.StatusConflict, "policy_changed", "capability policy changed after token issuance")
		return
	}
	result, executeErr := s.executeCapability(request.Context(), capability, claims)
	if executeErr != nil && result.Receipt.Payload.ExecutionID == "" {
		code := brokerErrorCode(executeErr)
		status := http.StatusInternalServerError
		switch {
		case errors.Is(executeErr, errTokenReplay), errors.Is(executeErr, errGenerationMismatch):
			status = http.StatusConflict
		case errors.Is(executeErr, errEmergencyStopped):
			status = http.StatusLocked
		case errors.Is(executeErr, errExecutableChanged):
			status = http.StatusConflict
		}
		s.auditDenied("execute.denied", claims.Subject, claims.Capability, claims.ControlGeneration, code)
		writeBrokerError(response, status, code, executeErr.Error())
		return
	}
	writeBrokerJSON(response, http.StatusOK, result)
}

var (
	errTokenReplay        = errors.New("capability token replayed")
	errGenerationMismatch = errors.New("broker control generation mismatch")
	errEmergencyStopped   = errors.New("privilege broker emergency stop is active")
	errExecutableChanged  = errors.New("broker executable digest changed")
	errControlGeneration  = errors.New("control generation must advance when broker stop state changes")
)

func (s *Server) validateExecutionClaims(input ExecuteRequest, claims CapabilityTokenClaims) error {
	now := s.now()
	if claims.BrokerInstanceID != s.instanceID {
		return fmt.Errorf("broker instance changed; request a fresh capability token")
	}
	if !claims.ExpiresAt.After(now) || claims.IssuedAt.After(now.Add(s.requestSkew)) {
		return fmt.Errorf("capability token expired or has an invalid issue time")
	}
	if claims.ApprovalProofID != claims.ApprovalRef || !hexDigestPattern.MatchString(claims.ApprovalProofID) ||
		claims.ApprovalKeyID == "" || !claims.ApprovalExpiresAt.After(now) {
		return fmt.Errorf("signed approval proof expired or is missing from the capability token")
	}
	if input.Capability != claims.Capability || input.Subject != claims.Subject ||
		input.PlanHash != claims.PlanHash || input.ApprovalRef != claims.ApprovalRef ||
		input.ControlGeneration != claims.ControlGeneration {
		return fmt.Errorf("execute request does not match capability token bindings")
	}
	state := s.currentState()
	if state.Stopped {
		return fmt.Errorf("privilege broker emergency stop is active")
	}
	if state.Generation != claims.ControlGeneration {
		return errGenerationMismatch
	}
	return nil
}

func (s *Server) consumeCapabilityToken(tokenID string, expiresAt time.Time) bool {
	s.tokenMu.Lock()
	defer s.tokenMu.Unlock()
	now := s.now()
	for key, expires := range s.consumed {
		if !expires.After(now) {
			delete(s.consumed, key)
		}
	}
	if _, exists := s.consumed[tokenID]; exists {
		return false
	}
	s.consumed[tokenID] = expiresAt
	return true
}

func (s *Server) executeCapability(parent context.Context, capability Capability, claims CapabilityTokenClaims) (ExecuteResponse, error) {
	actualDigest, err := hashFile(capability.Executable)
	if err != nil || actualDigest != capability.ExecutableSHA256 {
		return ExecuteResponse{}, errExecutableChanged
	}
	timeout := time.Duration(capability.TimeoutSeconds) * time.Second
	ctx, cancel := context.WithTimeout(parent, timeout)
	defer cancel()
	if err := s.admitExecution(claims, cancel); err != nil {
		return ExecuteResponse{}, err
	}
	defer func() {
		s.activeMu.Lock()
		delete(s.active, claims.TokenID)
		s.activeMu.Unlock()
	}()

	stdout := newDigestLimitedBuffer(capability.MaxOutputBytes)
	stderr := newDigestLimitedBuffer(capability.MaxOutputBytes)
	command := exec.Command(capability.Executable, capability.Arguments...)
	command.Dir = capability.WorkingDirectory
	command.Env = brokerEnvironment()
	command.Stdout = stdout
	command.Stderr = stderr
	startedAt := s.now()
	if err := s.audit.Append(AuditRecord{
		Type: "execute.started", Subject: claims.Subject, Capability: capability.Name,
		Generation: claims.ControlGeneration, Outcome: "started",
		Details: map[string]any{"token_id": claims.TokenID, "plan_hash": claims.PlanHash, "approval_ref": claims.ApprovalRef,
			"approval_proof_id": claims.ApprovalProofID, "approval_key_id": claims.ApprovalKeyID},
	}); err != nil {
		return ExecuteResponse{}, fmt.Errorf("persist execute.started audit before spawn: %w", err)
	}
	runErr := runBrokerCommand(ctx, command)
	finishedAt := s.now()
	exitCode := -1
	if command.ProcessState != nil {
		exitCode = command.ProcessState.ExitCode()
	}
	errorCode := ""
	if runErr != nil {
		switch {
		case errors.Is(ctx.Err(), context.DeadlineExceeded):
			errorCode = "timeout"
		case errors.Is(ctx.Err(), context.Canceled):
			errorCode = "cancelled"
		default:
			errorCode = "exit_failed"
		}
	}
	receiptPayload := ExecutionReceipt{
		ExecutionID: claims.TokenID, BrokerInstanceID: s.instanceID,
		Capability: capability.Name, CapabilityDigest: capability.digest,
		Subject: claims.Subject, PlanHash: claims.PlanHash, ApprovalRef: claims.ApprovalRef,
		ApprovalProofID: claims.ApprovalProofID, ApprovalKeyID: claims.ApprovalKeyID,
		ApprovalExpiresAt: claims.ApprovalExpiresAt,
		ControlGeneration: claims.ControlGeneration, ExitCode: exitCode, Succeeded: runErr == nil,
		StdoutSHA256: stdout.Digest(), StderrSHA256: stderr.Digest(),
		StdoutBytes: stdout.total, StderrBytes: stderr.total,
		StdoutTruncated: stdout.truncated, StderrTruncated: stderr.truncated,
		StartedAt: startedAt, FinishedAt: finishedAt, ErrorCode: errorCode,
	}
	outcome := "succeeded"
	if runErr != nil {
		outcome = errorCode
	}
	auditErr := s.audit.Append(AuditRecord{
		Type: "execute.finished", Subject: claims.Subject, Capability: capability.Name,
		Generation: claims.ControlGeneration, Outcome: outcome,
		Details: map[string]any{
			"execution_id": claims.TokenID, "exit_code": exitCode,
			"approval_proof_id": claims.ApprovalProofID, "approval_key_id": claims.ApprovalKeyID,
			"stdout_sha256": receiptPayload.StdoutSHA256, "stderr_sha256": receiptPayload.StderrSHA256,
			"stdout_bytes": stdout.total, "stderr_bytes": stderr.total,
		},
	})
	receiptPayload.AuditPersisted = auditErr == nil
	signature, err := signValue(s.privateKey, receiptPayload)
	if err != nil {
		return ExecuteResponse{}, err
	}
	response := ExecuteResponse{
		Stdout: stdout.String(), Stderr: stderr.String(),
		Receipt: SignedExecutionReceipt{Payload: receiptPayload, KeyID: s.keyID, Signature: signature},
	}
	if auditErr != nil {
		return response, fmt.Errorf("persist execute.finished audit: %w", auditErr)
	}
	return response, runErr
}

// admitExecution linearizes process admission with emergency stop. A stop must
// either observe this execution in the active set and cancel it, or commit the
// stopped state first and make admission fail.
func (s *Server) admitExecution(claims CapabilityTokenClaims, cancel context.CancelFunc) error {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	if s.state.Stopped {
		return errEmergencyStopped
	}
	if s.state.Generation != claims.ControlGeneration {
		return errGenerationMismatch
	}
	if !s.consumeCapabilityToken(claims.TokenID, claims.ExpiresAt) {
		return errTokenReplay
	}
	s.activeMu.Lock()
	s.active[claims.TokenID] = cancel
	s.activeMu.Unlock()
	return nil
}

func (s *Server) handleControl(response http.ResponseWriter, _ *http.Request, body []byte, stopped bool) {
	var input ControlRequest
	if err := decodeStrictJSON(body, &input); err != nil {
		writeBrokerError(response, http.StatusBadRequest, "invalid_control", err.Error())
		return
	}
	input.Reason = strings.TrimSpace(input.Reason)
	input.ChangedBy = strings.TrimSpace(input.ChangedBy)
	if input.Generation < 0 || input.Reason == "" || len([]rune(input.Reason)) > 1000 || input.ChangedBy == "" || len([]rune(input.ChangedBy)) > 200 {
		writeBrokerError(response, http.StatusBadRequest, "invalid_control", "generation, reason, and changed_by are required")
		return
	}
	if err := s.updateControl(stopped, input); err != nil {
		// State persistence is authoritative even if the subsequent audit append
		// fails. A committed stop must still cancel every admitted process.
		state := s.currentState()
		if stopped && state.Stopped && state.Generation == input.Generation {
			s.cancelActive()
		}
		if errors.Is(err, errControlGeneration) {
			writeBrokerError(response, http.StatusConflict, "generation_mismatch", err.Error())
		} else {
			writeBrokerError(response, http.StatusInternalServerError, "control_persist_failed", err.Error())
		}
		return
	}
	if stopped {
		s.cancelActive()
	}
	status, err := s.signedStatus()
	if err != nil {
		writeBrokerError(response, http.StatusInternalServerError, "status_failed", err.Error())
		return
	}
	writeBrokerJSON(response, http.StatusOK, status)
}

func (s *Server) updateControl(stopped bool, input ControlRequest) error {
	s.stateMu.Lock()
	defer s.stateMu.Unlock()
	if input.Generation < s.state.Generation || (input.Generation == s.state.Generation && stopped != s.state.Stopped) {
		return errControlGeneration
	}
	changed := input.Generation != s.state.Generation || stopped != s.state.Stopped
	if !changed {
		return nil
	}
	previous := s.state
	next := brokerState{Stopped: stopped, Generation: input.Generation, Reason: input.Reason, ChangedBy: input.ChangedBy, ChangedAt: s.now()}
	if err := persistBrokerState(s.statePath, next); err != nil {
		if stopped {
			// Emergency stop wins over durability. Latch the in-memory state so
			// active processes are cancelled even when the disk/ACL is unhealthy;
			// the caller still receives an error and must repair persistence before
			// restarting the Broker.
			s.state = next
		}
		return err
	}
	action := "control.resumed"
	if stopped {
		action = "control.stopped"
	}
	if err := s.audit.Append(AuditRecord{
		Type: action, Subject: input.ChangedBy, Generation: input.Generation, Outcome: "applied",
		Details: map[string]any{"reason": input.Reason},
	}); err != nil {
		if stopped {
			// Emergency stop is fail-safe: once the stopped state is durable it
			// remains authoritative even when audit/checkpoint persistence fails.
			s.state = next
			return err
		}
		// Resume is fail-closed. Never expose the resumed state unless its audit
		// record and checkpoint are durable. Roll the state back to the previous
		// stopped generation; if rollback itself fails, keep the in-memory state
		// stopped and join both failures so the service remains unavailable.
		rollbackErr := persistBrokerState(s.statePath, previous)
		s.state = previous
		if rollbackErr == nil {
			_ = s.audit.Append(AuditRecord{
				Type: "control.resume_rolled_back", Subject: input.ChangedBy,
				Generation: previous.Generation, Outcome: "stopped",
				Details: map[string]any{"reason": input.Reason, "failed_generation": input.Generation},
			})
		}
		return errors.Join(err, rollbackErr)
	}
	s.state = next
	return nil
}

func (s *Server) signedStatus() (Status, error) {
	state := s.currentState()
	s.activeMu.Lock()
	active := len(s.active)
	s.activeMu.Unlock()
	status := Status{
		Version: APIVersion, InstanceID: s.instanceID, Stopped: state.Stopped,
		Generation: state.Generation, PolicyDigest: s.policy.Digest,
		Capabilities: s.policy.PublicCapabilities(), ApprovalAuthorities: s.policy.PublicApprovalAuthorities(), ActiveExecutions: active,
		PublicKey: base64.StdEncoding.EncodeToString(s.publicKey), KeyID: s.keyID, IssuedAt: s.now(),
	}
	unsigned := status
	unsigned.Signature = ""
	signature, err := signValue(s.privateKey, unsigned)
	if err != nil {
		return status, err
	}
	status.Signature = signature
	return status, nil
}

func (s *Server) currentState() brokerState {
	s.stateMu.RLock()
	defer s.stateMu.RUnlock()
	return s.state
}

func (s *Server) cancelActive() {
	s.activeMu.Lock()
	cancels := make([]context.CancelFunc, 0, len(s.active))
	for _, cancel := range s.active {
		cancels = append(cancels, cancel)
	}
	s.activeMu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (s *Server) auditDenied(eventType, subject, capability string, generation int64, reason string) {
	_ = s.audit.Append(AuditRecord{
		Type: eventType, Subject: subject, Capability: capability,
		Generation: generation, Outcome: "denied", Details: map[string]any{"reason": reason},
	})
}

func normalizeGrantRequest(input *GrantRequest) error {
	if input == nil {
		return fmt.Errorf("grant request is required")
	}
	input.Capability = strings.ToLower(strings.TrimSpace(input.Capability))
	input.Subject = strings.TrimSpace(input.Subject)
	input.PlanHash = strings.ToLower(strings.TrimSpace(input.PlanHash))
	input.ApprovalRef = strings.TrimSpace(input.ApprovalRef)
	if !capabilityNamePattern.MatchString(input.Capability) {
		return fmt.Errorf("capability is invalid")
	}
	if input.Subject == "" || len([]rune(input.Subject)) > 200 {
		return fmt.Errorf("subject is required and must not exceed 200 characters")
	}
	if !hexDigestPattern.MatchString(input.PlanHash) {
		return fmt.Errorf("plan_hash must be lowercase SHA-256")
	}
	if !hexDigestPattern.MatchString(input.ApprovalRef) {
		return fmt.Errorf("approval_ref must be the signed lowercase approval proof id")
	}
	if input.ApprovalRef != input.ApprovalProof.Claims.ProofID {
		return fmt.Errorf("approval_ref must match approval_proof.claims.proof_id")
	}
	if input.ControlGeneration < 0 {
		return fmt.Errorf("control_generation must not be negative")
	}
	return nil
}

func validateExecuteRequest(input ExecuteRequest) error {
	if len(input.Token) < 32 || len(input.Token) > 8192 {
		return fmt.Errorf("capability token length is invalid")
	}
	if !capabilityNamePattern.MatchString(input.Capability) {
		return fmt.Errorf("capability is invalid")
	}
	if strings.TrimSpace(input.Subject) != input.Subject || input.Subject == "" || len([]rune(input.Subject)) > 200 {
		return fmt.Errorf("subject is invalid")
	}
	if !hexDigestPattern.MatchString(input.PlanHash) {
		return fmt.Errorf("plan_hash must be lowercase SHA-256")
	}
	if !hexDigestPattern.MatchString(input.ApprovalRef) {
		return fmt.Errorf("approval_ref is invalid")
	}
	if input.ControlGeneration < 0 {
		return fmt.Errorf("control_generation must not be negative")
	}
	return nil
}

func decodeStrictJSON(body []byte, target any) error {
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return fmt.Errorf("request must contain exactly one JSON object")
	}
	return nil
}

func writeBrokerJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.Header().Set("Cache-Control", "no-store")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeBrokerError(response http.ResponseWriter, status int, code, message string) {
	writeBrokerJSON(response, status, ErrorResponse{Code: code, Error: message})
}

func validateLoopbackAddress(address string) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid broker listen address: %w", err)
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("privilege broker must bind to an explicit loopback address")
	}
	return nil
}

func requestFromLoopback(remote string) bool {
	host, _, err := net.SplitHostPort(strings.TrimSpace(remote))
	if err != nil {
		return false
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

type digestLimitedBuffer struct {
	data      strings.Builder
	hash      hash.Hash
	limit     int
	total     int64
	truncated bool
}

func newDigestLimitedBuffer(limit int) *digestLimitedBuffer {
	return &digestLimitedBuffer{hash: sha256.New(), limit: limit}
}

func (b *digestLimitedBuffer) Write(payload []byte) (int, error) {
	original := len(payload)
	b.total += int64(original)
	_, _ = b.hash.Write(payload)
	remaining := b.limit - b.data.Len()
	if remaining <= 0 {
		b.truncated = true
		return original, nil
	}
	if len(payload) > remaining {
		payload = payload[:remaining]
		b.truncated = true
	}
	_, _ = b.data.Write(payload)
	return original, nil
}

func (b *digestLimitedBuffer) String() string { return b.data.String() }
func (b *digestLimitedBuffer) Digest() string { return hex.EncodeToString(b.hash.Sum(nil)) }

func brokerErrorCode(err error) string {
	switch {
	case errors.Is(err, errTokenReplay):
		return "token_replayed"
	case errors.Is(err, errGenerationMismatch):
		return "generation_mismatch"
	case errors.Is(err, errEmergencyStopped):
		return "emergency_stopped"
	case errors.Is(err, errExecutableChanged):
		return "executable_changed"
	default:
		return "execute_failed"
	}
}
