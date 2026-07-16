package privilegebroker

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"reflect"
	"strconv"
	"strings"
	"time"
)

type Client struct {
	baseURL    string
	key        []byte
	controlKey []byte
	publicKey  ed25519.PublicKey
	keyID      string
	http       *http.Client
	now        func() time.Time
}

type BrokerError struct {
	StatusCode int
	Code       string
	Message    string
}

func (e *BrokerError) Error() string {
	if e == nil {
		return "privilege broker request failed"
	}
	return fmt.Sprintf("privilege broker %s: %s", e.Code, e.Message)
}

type ExecutionError struct {
	Response ExecuteResponse
}

func (e *ExecutionError) Error() string {
	if e == nil {
		return "privilege broker execution failed"
	}
	receipt := e.Response.Receipt.Payload
	if !receipt.AuditPersisted {
		return fmt.Sprintf("privilege broker execution audit was not persisted (execution_id=%s succeeded=%t)", receipt.ExecutionID, receipt.Succeeded)
	}
	return fmt.Sprintf("privilege broker execution failed: %s (exit_code=%d)", receipt.ErrorCode, receipt.ExitCode)
}

func NewClient(baseURL string, sharedKey []byte, pinnedPublicKey ed25519.PublicKey) (*Client, error) {
	if len(sharedKey) < 32 {
		return nil, fmt.Errorf("broker client key must contain at least 32 bytes")
	}
	if len(pinnedPublicKey) != ed25519.PublicKeySize {
		return nil, fmt.Errorf("broker pinned public key is invalid")
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || parsed.Scheme != "http" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("broker URL must be an http loopback origin")
	}
	if parsed.Path != "" && parsed.Path != "/" {
		return nil, fmt.Errorf("broker URL must not contain a path")
	}
	host := parsed.Hostname()
	ip := net.ParseIP(host)
	if !strings.EqualFold(host, "localhost") && (ip == nil || !ip.IsLoopback()) {
		return nil, fmt.Errorf("broker URL must use a loopback host")
	}
	if parsed.Port() == "" {
		return nil, fmt.Errorf("broker URL must include an explicit port")
	}
	return &Client{
		baseURL: strings.TrimRight(parsed.String(), "/"), key: append([]byte(nil), sharedKey...),
		publicKey: append(ed25519.PublicKey(nil), pinnedPublicKey...), keyID: publicKeyID(pinnedPublicKey),
		http: &http.Client{Transport: &http.Transport{Proxy: nil, DisableCompression: true}},
		now:  func() time.Time { return time.Now().UTC() },
	}, nil
}

func NewClientFromEnv() (*Client, error) {
	client, err := NewClientFromEncoded(
		defaultEnv("STEWARD_BROKER_URL", "http://127.0.0.1:18100"),
		osEnv("STEWARD_BROKER_CLIENT_KEY"),
		osEnv("STEWARD_BROKER_PUBLIC_KEY"),
	)
	if err != nil {
		return nil, err
	}
	encodedControl := strings.TrimSpace(osEnv("STEWARD_BROKER_CONTROL_KEY"))
	if encodedControl != "" {
		controlKey, err := decodeSharedKey(encodedControl)
		if err != nil {
			return nil, fmt.Errorf("decode broker control key: %w", err)
		}
		client.controlKey = controlKey
	}
	return client, nil
}

// NewClientFromEncoded constructs a client from the deployment-safe encoded
// values used by service managers. It is also used to reject invalid R3
// service installations before they mutate the host.
func NewClientFromEncoded(baseURL, encodedSharedKey, encodedPublicKey string) (*Client, error) {
	sharedKey, err := decodeSharedKey(encodedSharedKey)
	if err != nil {
		return nil, err
	}
	publicKey, err := decodePublicKey(encodedPublicKey)
	if err != nil {
		return nil, err
	}
	return NewClient(baseURL, sharedKey, publicKey)
}

func (c *Client) Status(ctx context.Context) (Status, error) {
	var status Status
	if err := c.do(ctx, http.MethodGet, "/v1/status", nil, &status); err != nil {
		return status, err
	}
	if err := c.verifyStatus(status); err != nil {
		return status, err
	}
	return status, nil
}

func (c *Client) Capability(ctx context.Context, name string) (PublicCapability, error) {
	status, err := c.Status(ctx)
	if err != nil {
		return PublicCapability{}, err
	}
	name = strings.ToLower(strings.TrimSpace(name))
	for _, capability := range status.Capabilities {
		if capability.Name == name {
			return capability, nil
		}
	}
	return PublicCapability{}, fmt.Errorf("privilege broker capability %q is unavailable", name)
}

func (c *Client) Grant(ctx context.Context, authorization Authorization) (GrantResponse, error) {
	request := GrantRequest{
		Capability: strings.ToLower(strings.TrimSpace(authorization.Capability)),
		Subject:    strings.TrimSpace(authorization.Subject), PlanHash: strings.ToLower(strings.TrimSpace(authorization.PlanHash)),
		ApprovalRef: strings.TrimSpace(authorization.ApprovalRef), ApprovalProof: authorization.ApprovalProof,
		ControlGeneration: authorization.ControlGeneration,
	}
	if err := normalizeGrantRequest(&request); err != nil {
		return GrantResponse{}, err
	}
	var response GrantResponse
	if err := c.do(ctx, http.MethodPost, "/v1/grants", request, &response); err != nil {
		return response, err
	}
	claims, err := decodeSignedToken(c.publicKey, response.Token)
	if err != nil {
		return response, err
	}
	if response.KeyID != c.keyID || !reflect.DeepEqual(claims, response.Claims) ||
		claims.Capability != request.Capability || claims.Subject != request.Subject ||
		claims.PlanHash != request.PlanHash || claims.ApprovalRef != request.ApprovalRef ||
		claims.ApprovalProofID != request.ApprovalProof.Claims.ProofID || claims.ApprovalKeyID != request.ApprovalProof.KeyID ||
		!claims.ApprovalExpiresAt.Equal(request.ApprovalProof.Claims.ExpiresAt) ||
		claims.ControlGeneration != request.ControlGeneration {
		return response, fmt.Errorf("broker grant response does not match authorization request")
	}
	if !claims.ExpiresAt.After(c.now()) {
		return response, fmt.Errorf("broker returned an expired capability token")
	}
	return response, nil
}

func (c *Client) Execute(ctx context.Context, grant GrantResponse) (ExecuteResponse, error) {
	request := ExecuteRequest{
		Token: grant.Token, Capability: grant.Claims.Capability, Subject: grant.Claims.Subject,
		PlanHash: grant.Claims.PlanHash, ApprovalRef: grant.Claims.ApprovalRef,
		ControlGeneration: grant.Claims.ControlGeneration,
	}
	var response ExecuteResponse
	if err := c.do(ctx, http.MethodPost, "/v1/execute", request, &response); err != nil {
		return response, err
	}
	if err := c.verifyReceipt(response.Receipt, grant.Claims); err != nil {
		return response, err
	}
	if !response.Receipt.Payload.Succeeded || !response.Receipt.Payload.AuditPersisted {
		return response, &ExecutionError{Response: response}
	}
	return response, nil
}

func (c *Client) ExecuteCapability(ctx context.Context, authorization Authorization) (ExecuteResponse, error) {
	grant, err := c.Grant(ctx, authorization)
	if err != nil {
		return ExecuteResponse{}, err
	}
	return c.Execute(ctx, grant)
}

func (c *Client) IssueDelegation(ctx context.Context, request BrokerDelegationRequest) (SignedBrokerDelegation, error) {
	var response SignedBrokerDelegation
	if err := c.do(ctx, http.MethodPost, "/v1/delegations", request, &response); err != nil {
		return response, err
	}
	if response.KeyID != c.keyID || response.Claims.OriginBrokerKeyID != c.keyID ||
		response.Claims.TargetDeviceID != strings.TrimSpace(request.TargetDeviceID) ||
		response.Claims.TargetBrokerKeyID != request.TargetStatus.KeyID ||
		response.Claims.Capability != strings.ToLower(strings.TrimSpace(request.Capability)) ||
		response.Claims.Subject != strings.TrimSpace(request.Subject) || response.Claims.PlanHash != strings.ToLower(strings.TrimSpace(request.PlanHash)) ||
		response.Claims.ApprovalRef != strings.TrimSpace(request.ApprovalRef) {
		return response, fmt.Errorf("broker delegation response does not match request bindings")
	}
	if err := verifyValue(c.publicKey, response.Claims, response.Signature); err != nil {
		return response, fmt.Errorf("verify broker delegation: %w", err)
	}
	if !response.Claims.ExpiresAt.After(c.now()) {
		return response, fmt.Errorf("broker returned an expired delegation")
	}
	return response, nil
}

func (c *Client) ExecuteDelegation(ctx context.Context, delegation SignedBrokerDelegation, originStatus Status) (ExecuteResponse, error) {
	var response ExecuteResponse
	if err := c.do(ctx, http.MethodPost, "/v1/delegated-execute", DelegatedExecuteRequest{Delegation: delegation, OriginStatus: originStatus}, &response); err != nil {
		return response, err
	}
	claims := delegation.Claims
	tokenClaims := CapabilityTokenClaims{
		TokenID: claims.DelegationID, BrokerInstanceID: claims.TargetBrokerInstanceID,
		Capability: claims.Capability, CapabilityDigest: claims.CapabilityDigest,
		Subject: claims.Subject, PlanHash: claims.PlanHash, ApprovalRef: claims.ApprovalRef,
		ApprovalProofID: claims.ApprovalProofID, ApprovalKeyID: claims.ApprovalKeyID,
		ApprovalExpiresAt: claims.ApprovalExpiresAt, ControlGeneration: claims.TargetControlGeneration,
		DelegationID: claims.DelegationID, OriginBrokerKeyID: claims.OriginBrokerKeyID, CredentialRefs: claims.CredentialRefs,
	}
	if err := c.verifyReceipt(response.Receipt, tokenClaims); err != nil {
		return response, err
	}
	if !response.Receipt.Payload.Succeeded || !response.Receipt.Payload.AuditPersisted {
		return response, &ExecutionError{Response: response}
	}
	return response, nil
}

func (c *Client) SetControl(ctx context.Context, stopped bool, request ControlRequest) (Status, error) {
	path := "/v1/control/resume"
	if stopped {
		path = "/v1/control/stop"
	} else if len(c.controlKey) < 32 {
		return Status{}, fmt.Errorf("broker resume requires the independent control key")
	}
	var status Status
	key := c.key
	if !stopped {
		key = c.controlKey
	}
	if err := c.doWithKey(ctx, http.MethodPost, path, request, &status, key); err != nil {
		return status, err
	}
	if err := c.verifyStatus(status); err != nil {
		return status, err
	}
	if status.Stopped != stopped || status.Generation != request.Generation {
		return status, fmt.Errorf("broker control response does not match requested state")
	}
	return status, nil
}

func (c *Client) verifyStatus(status Status) error {
	if status.Version != APIVersion || status.KeyID != c.keyID || status.PublicKey != base64.StdEncoding.EncodeToString(c.publicKey) {
		return fmt.Errorf("broker identity does not match the pinned public key")
	}
	unsigned := status
	unsigned.Signature = ""
	if err := verifyValue(c.publicKey, unsigned, status.Signature); err != nil {
		return err
	}
	now := c.now()
	if status.IssuedAt.Before(now.Add(-time.Minute)) || status.IssuedAt.After(now.Add(time.Minute)) {
		return fmt.Errorf("broker status signature is stale")
	}
	return nil
}

func VerifyStatus(encodedPublicKey string, status Status, now time.Time) error {
	publicKey, err := decodePublicKey(encodedPublicKey)
	if err != nil {
		return err
	}
	if status.Version != APIVersion || status.KeyID != publicKeyID(publicKey) || status.PublicKey != base64.StdEncoding.EncodeToString(publicKey) {
		return fmt.Errorf("broker identity does not match the pinned public key")
	}
	unsigned := status
	unsigned.Signature = ""
	if err := verifyValue(publicKey, unsigned, status.Signature); err != nil {
		return err
	}
	if status.IssuedAt.Before(now.Add(-time.Minute)) || status.IssuedAt.After(now.Add(time.Minute)) {
		return fmt.Errorf("broker status signature is stale")
	}
	return nil
}

func (c *Client) verifyReceipt(receipt SignedExecutionReceipt, claims CapabilityTokenClaims) error {
	if receipt.KeyID != c.keyID {
		return fmt.Errorf("broker receipt key id does not match pinned key")
	}
	if err := verifyValue(c.publicKey, receipt.Payload, receipt.Signature); err != nil {
		return err
	}
	payload := receipt.Payload
	if payload.ExecutionID != claims.TokenID || payload.BrokerInstanceID != claims.BrokerInstanceID ||
		payload.Capability != claims.Capability || payload.CapabilityDigest != claims.CapabilityDigest ||
		payload.Subject != claims.Subject || payload.PlanHash != claims.PlanHash ||
		payload.ApprovalRef != claims.ApprovalRef || payload.ApprovalProofID != claims.ApprovalProofID ||
		payload.ApprovalKeyID != claims.ApprovalKeyID || !payload.ApprovalExpiresAt.Equal(claims.ApprovalExpiresAt) ||
		payload.ControlGeneration != claims.ControlGeneration {
		return fmt.Errorf("broker receipt does not match capability token bindings")
	}
	if payload.DelegationID != claims.DelegationID || payload.OriginBrokerKeyID != claims.OriginBrokerKeyID ||
		!stringSlicesEqual(payload.CredentialRefs, claims.CredentialRefs) {
		return fmt.Errorf("broker receipt does not match delegation bindings")
	}
	return nil
}

func stringSlicesEqual(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func VerifyDelegatedReceipt(encodedTargetPublicKey string, delegation SignedBrokerDelegation, receipt SignedExecutionReceipt) error {
	publicKey, err := decodePublicKey(encodedTargetPublicKey)
	if err != nil {
		return err
	}
	claims := delegation.Claims
	if receipt.KeyID != claims.TargetBrokerKeyID || receipt.KeyID != publicKeyID(publicKey) {
		return fmt.Errorf("delegated receipt target Broker identity mismatch")
	}
	if err := verifyValue(publicKey, receipt.Payload, receipt.Signature); err != nil {
		return err
	}
	payload := receipt.Payload
	if payload.ExecutionID != claims.DelegationID || payload.BrokerInstanceID != claims.TargetBrokerInstanceID ||
		payload.Capability != claims.Capability || payload.CapabilityDigest != claims.CapabilityDigest ||
		payload.Subject != claims.Subject || payload.PlanHash != claims.PlanHash || payload.ApprovalRef != claims.ApprovalRef ||
		payload.ApprovalProofID != claims.ApprovalProofID || payload.ApprovalKeyID != claims.ApprovalKeyID ||
		!payload.ApprovalExpiresAt.Equal(claims.ApprovalExpiresAt) || !payload.AuditPersisted ||
		payload.ControlGeneration != claims.TargetControlGeneration || payload.DelegationID != claims.DelegationID ||
		payload.OriginBrokerKeyID != claims.OriginBrokerKeyID || !stringSlicesEqual(payload.CredentialRefs, claims.CredentialRefs) {
		return fmt.Errorf("delegated receipt does not match Broker delegation bindings")
	}
	return nil
}

func (c *Client) do(ctx context.Context, method, path string, input any, output any) error {
	return c.doWithKey(ctx, method, path, input, output, c.key)
}

func (c *Client) doWithKey(ctx context.Context, method, path string, input any, output any, key []byte) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return err
		}
	}
	nonce, err := randomID()
	if err != nil {
		return err
	}
	timestamp := strconv.FormatInt(c.now().Unix(), 10)
	request, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	request.Header.Set("Accept", "application/json")
	if input != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	request.Header.Set(HeaderTimestamp, timestamp)
	request.Header.Set(HeaderNonce, nonce)
	request.Header.Set(HeaderSignature, requestSignature(key, method, path, timestamp, nonce, body))
	response, err := c.http.Do(request)
	if err != nil {
		return fmt.Errorf("call privilege broker: %w", err)
	}
	defer response.Body.Close()
	payload, err := io.ReadAll(io.LimitReader(response.Body, 2<<20))
	if err != nil {
		return err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		var brokerError ErrorResponse
		_ = json.Unmarshal(payload, &brokerError)
		return &BrokerError{StatusCode: response.StatusCode, Code: brokerError.Code, Message: brokerError.Error}
	}
	if output == nil {
		return nil
	}
	if err := json.Unmarshal(payload, output); err != nil {
		return fmt.Errorf("decode privilege broker response: %w", err)
	}
	return nil
}
