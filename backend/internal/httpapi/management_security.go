package httpapi

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

const (
	managementSessionTTL        = 30 * 24 * time.Hour
	managementBrowserTicketTTL  = 45 * time.Second
	maxManagementLoginBodyBytes = 4 << 10
	managementSessionCookieName = "steward_management_session"
)

type managementSessionStore interface {
	SaveManagementSession(context.Context, []byte, time.Time) error
	ManagementSessionExpiry(context.Context, []byte, time.Time) (time.Time, bool, error)
	DeleteManagementSession(context.Context, []byte) error
	DeleteExpiredManagementSessions(context.Context, time.Time) error
}

type managementSecurity struct {
	required        bool
	token           string
	allowedOrigins  map[string]struct{}
	allowRemoteHost bool
	now             func() time.Time
	sessionsMu      sync.Mutex
	sessions        map[[sha256.Size]byte]time.Time
	sessionStore    managementSessionStore
	ticketsMu       sync.Mutex
	tickets         map[[sha256.Size]byte]time.Time
}

func newManagementSecurity(required bool, token string, allowedOrigins []string, allowRemoteHost bool, stores ...managementSessionStore) *managementSecurity {
	origins := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		if normalized := normalizeManagementOrigin(origin); normalized != "" {
			origins[normalized] = struct{}{}
		}
	}
	var sessionStore managementSessionStore
	if len(stores) > 0 {
		sessionStore = stores[0]
	}
	return &managementSecurity{
		required:        required || allowRemoteHost,
		token:           strings.TrimSpace(token),
		allowedOrigins:  origins,
		allowRemoteHost: allowRemoteHost,
		now:             time.Now,
		sessions:        make(map[[sha256.Size]byte]time.Time),
		sessionStore:    sessionStore,
		tickets:         make(map[[sha256.Size]byte]time.Time),
	}
}

func (s *managementSecurity) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.requestHostAllowed(r) {
			httpError(w, http.StatusForbidden, "management API Host is not an allowed local endpoint")
			return
		}
		origin := normalizeManagementOrigin(r.Header.Get("Origin"))
		originPresent := strings.TrimSpace(r.Header.Get("Origin")) != ""
		originAllowed := !originPresent || s.originAllowed(r, origin)
		if originPresent && !originAllowed {
			httpError(w, http.StatusForbidden, "management API requests are only accepted from the same origin or an explicitly configured origin")
			return
		}
		if originPresent {
			addVaryHeader(w.Header(), "Origin")
			w.Header().Set("Access-Control-Allow-Origin", origin)
		}
		if r.Method == http.MethodOptions {
			s.handlePreflight(w, r, originPresent)
			return
		}

		if isManagementSessionRoute(r.URL.Path) {
			next.ServeHTTP(w, r)
			return
		}
		if !s.required {
			next.ServeHTTP(w, r)
			return
		}

		authenticatedByRoot := s.validRootBearer(r.Header.Get("Authorization"))
		authenticatedBySession := s.validSessionBearerContext(r.Context(), r.Header.Get("Authorization")) || s.validSessionCookie(r)
		authenticatedByWebSocket := s.validWebSocketSession(r)
		if !authenticatedByRoot && !authenticatedBySession && !authenticatedByWebSocket {
			w.Header().Set("WWW-Authenticate", `Bearer realm="steward-management"`)
			httpError(w, http.StatusUnauthorized, "management authentication is required")
			return
		}
		if authenticatedByWebSocket {
			r.Header.Del("Sec-WebSocket-Protocol")
		}
		if isUnsafeManagementMethod(r.Method) && authenticatedBySession && !authenticatedByRoot && !originPresent {
			httpError(w, http.StatusForbidden, "browser management writes require a same-origin Origin header")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *managementSecurity) requestHostAllowed(r *http.Request) bool {
	if s.allowRemoteHost {
		return true
	}
	hostname := managementRequestHostname(r.Host)
	if hostname == "localhost" {
		return true
	}
	ip := net.ParseIP(hostname)
	return ip != nil && ip.IsLoopback()
}

func managementRequestHostname(hostport string) string {
	parsed, err := url.Parse("//" + strings.TrimSpace(hostport))
	if err != nil || parsed.User != nil {
		return ""
	}
	return strings.TrimSuffix(strings.ToLower(parsed.Hostname()), ".")
}

func (s *managementSecurity) getSession(w http.ResponseWriter, r *http.Request) {
	authenticated := !s.required || s.validRootBearer(r.Header.Get("Authorization")) || s.validSessionBearerContext(r.Context(), r.Header.Get("Authorization")) || s.validSessionCookie(r)
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusOK, map[string]bool{
		"required":      s.required,
		"authenticated": authenticated,
	})
}

func (s *managementSecurity) exchangeSession(w http.ResponseWriter, r *http.Request) {
	if !s.required {
		respondJSON(w, http.StatusOK, map[string]bool{"required": false, "authenticated": true})
		return
	}
	if s.token == "" {
		httpError(w, http.StatusServiceUnavailable, "management authentication is not configured")
		return
	}

	var body struct {
		Token string `json:"token"`
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxManagementLoginBodyBytes)
	decoder := json.NewDecoder(r.Body)
	if err := decoder.Decode(&body); err != nil {
		httpError(w, http.StatusBadRequest, "management token is required")
		return
	}
	candidate := strings.TrimSpace(body.Token)
	if !constantTimeEqual(candidate, s.token) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="steward-management"`)
		httpError(w, http.StatusUnauthorized, "invalid management token")
		return
	}

	sessionToken, expires, err := s.issueSessionTokenContext(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, "management session could not be created")
		return
	}
	s.setSessionCookie(w, r, sessionToken, expires)
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusOK, map[string]any{
		"required":      true,
		"authenticated": true,
		"session_token": sessionToken,
		"expires_at":    expires.UTC().Format(time.RFC3339),
	})
}

func (s *managementSecurity) deleteSession(w http.ResponseWriter, r *http.Request) {
	token := bearerToken(r.Header.Get("Authorization"))
	if token == "" || s.validRootBearer(r.Header.Get("Authorization")) {
		if cookie, err := r.Cookie(managementSessionCookieName); err == nil {
			token = cookie.Value
		}
	}
	_ = s.revokeSessionTokenContext(r.Context(), token)
	s.clearSessionCookie(w, r)
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusOK, map[string]bool{"required": s.required, "authenticated": !s.required})
}

func (s *managementSecurity) issueBrowserTicket(w http.ResponseWriter, r *http.Request) {
	if !s.required || s.token == "" {
		httpError(w, http.StatusServiceUnavailable, "management authentication is not configured")
		return
	}
	if !s.validRootBearer(r.Header.Get("Authorization")) {
		w.Header().Set("WWW-Authenticate", `Bearer realm="steward-management"`)
		httpError(w, http.StatusUnauthorized, "the root management token is required to create a browser ticket")
		return
	}
	ticket, expires, err := s.newBrowserTicket()
	if err != nil {
		httpError(w, http.StatusInternalServerError, "browser ticket could not be created")
		return
	}
	w.Header().Set("Cache-Control", "no-store")
	respondJSON(w, http.StatusCreated, map[string]any{
		"ticket":      ticket,
		"launch_path": "/api/auth/browser-tickets/" + url.PathEscape(ticket),
		"expires_at":  expires.UTC().Format(time.RFC3339),
	})
}

func (s *managementSecurity) consumeBrowserTicket(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("Referrer-Policy", "no-referrer")
	if !s.consumeBrowserTicketValue(chi.URLParam(r, "ticket")) {
		httpError(w, http.StatusUnauthorized, "browser ticket is invalid, expired, or already used")
		return
	}
	sessionToken, expires, err := s.issueSessionTokenContext(r.Context())
	if err != nil {
		httpError(w, http.StatusInternalServerError, "management session could not be created")
		return
	}
	s.setSessionCookie(w, r, sessionToken, expires)
	http.Redirect(w, r, "/tools/steward", http.StatusSeeOther)
}

func (s *managementSecurity) handlePreflight(w http.ResponseWriter, r *http.Request, originPresent bool) {
	if !originPresent {
		httpError(w, http.StatusBadRequest, "CORS preflight requires Origin")
		return
	}
	method := strings.ToUpper(strings.TrimSpace(r.Header.Get("Access-Control-Request-Method")))
	if !isAllowedManagementMethod(method) {
		httpError(w, http.StatusForbidden, "requested management method is not allowed")
		return
	}
	w.Header().Set("Access-Control-Allow-Methods", "GET, HEAD, POST, PUT, PATCH, DELETE, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Accept, Authorization, Content-Type, Last-Event-ID")
	w.Header().Set("Access-Control-Max-Age", "300")
	w.WriteHeader(http.StatusNoContent)
}

func (s *managementSecurity) originAllowed(r *http.Request, origin string) bool {
	if origin == "" {
		return false
	}
	if constantTimeEqual(origin, s.requestManagementOrigin(r)) {
		return true
	}
	_, ok := s.allowedOrigins[origin]
	return ok
}

// requestManagementOrigin reconstructs the browser-visible origin. Forwarded
// headers are trusted only when remote management has explicitly been enabled;
// in the loopback-only mode they must not let a client spoof the request origin.
func (s *managementSecurity) requestManagementOrigin(r *http.Request) string {
	scheme := "http"
	if r.TLS != nil {
		scheme = "https"
	}
	host := strings.TrimSpace(r.Host)

	if s.allowRemoteHost {
		forwarded := false
		if forwardedProto := firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Proto")); forwardedProto != "" {
			forwardedProto = strings.ToLower(forwardedProto)
			if forwardedProto != "http" && forwardedProto != "https" {
				return ""
			}
			scheme = forwardedProto
			forwarded = true
		}
		if forwardedHost := firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Host")); forwardedHost != "" {
			host = forwardedHost
			forwarded = true
		}
		candidate := normalizeManagementOrigin(scheme + "://" + host)
		if forwarded {
			// A public listener must not let arbitrary clients manufacture their
			// own same-origin value with X-Forwarded-* headers. Only the explicitly
			// configured external origin of a controlled reverse proxy is trusted.
			if _, ok := s.allowedOrigins[candidate]; !ok {
				return ""
			}
		}
		return candidate
	}

	return normalizeManagementOrigin(scheme + "://" + host)
}

func firstForwardedHeaderValue(value string) string {
	if index := strings.IndexByte(value, ','); index >= 0 {
		value = value[:index]
	}
	return strings.TrimSpace(value)
}

func (s *managementSecurity) validBearer(header string) bool {
	return s.validRootBearer(header) || s.validSessionBearer(header)
}

func (s *managementSecurity) validRootBearer(header string) bool {
	if !s.required || s.token == "" {
		return false
	}
	candidate := bearerToken(header)
	return candidate != "" && constantTimeEqual(candidate, s.token)
}

func (s *managementSecurity) validSessionBearer(header string) bool {
	return s.validSessionToken(bearerToken(header))
}

func (s *managementSecurity) validSessionBearerContext(ctx context.Context, header string) bool {
	return s.validSessionTokenContext(ctx, bearerToken(header))
}

func (s *managementSecurity) validSessionCookie(r *http.Request) bool {
	cookie, err := r.Cookie(managementSessionCookieName)
	return err == nil && s.validSessionTokenContext(r.Context(), cookie.Value)
}

func (s *managementSecurity) validWebSocketSession(r *http.Request) bool {
	if !headerContainsToken(r.Header, "Connection", "upgrade") || !strings.EqualFold(strings.TrimSpace(r.Header.Get("Upgrade")), "websocket") {
		return false
	}
	for _, value := range r.Header.Values("Sec-WebSocket-Protocol") {
		for _, protocol := range strings.Split(value, ",") {
			protocol = strings.TrimSpace(protocol)
			if strings.HasPrefix(protocol, "steward-management.") && s.validSessionTokenContext(r.Context(), strings.TrimPrefix(protocol, "steward-management.")) {
				return true
			}
		}
	}
	return false
}

func (s *managementSecurity) validSessionToken(token string) bool {
	return s.validSessionTokenContext(context.Background(), token)
}

func (s *managementSecurity) validSessionTokenContext(ctx context.Context, token string) bool {
	if !s.required {
		return false
	}
	if token == "" || constantTimeEqual(token, s.token) {
		return false
	}
	hash := sha256.Sum256([]byte(token))
	now := s.now()
	s.sessionsMu.Lock()
	expires, ok := s.sessions[hash]
	if ok && !now.Before(expires) {
		delete(s.sessions, hash)
		ok = false
	}
	s.sessionsMu.Unlock()
	if ok {
		return true
	}
	if s.sessionStore == nil {
		return false
	}
	persistedExpiry, valid, err := s.sessionStore.ManagementSessionExpiry(ctx, hash[:], now)
	if err != nil || !valid {
		return false
	}
	s.sessionsMu.Lock()
	s.sessions[hash] = persistedExpiry
	s.sessionsMu.Unlock()
	return true
}

func headerContainsToken(header http.Header, name string, token string) bool {
	for _, value := range header.Values(name) {
		for _, item := range strings.Split(value, ",") {
			if strings.EqualFold(strings.TrimSpace(item), token) {
				return true
			}
		}
	}
	return false
}

func (s *managementSecurity) issueSessionToken() (string, time.Time, error) {
	return s.issueSessionTokenContext(context.Background())
}

func (s *managementSecurity) issueSessionTokenContext(ctx context.Context) (string, time.Time, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", time.Time{}, err
	}
	token := base64.RawURLEncoding.EncodeToString(random)
	hash := sha256.Sum256([]byte(token))
	expires := s.now().Add(managementSessionTTL)
	if s.sessionStore != nil {
		if err := s.sessionStore.SaveManagementSession(ctx, hash[:], expires); err != nil {
			return "", time.Time{}, err
		}
		_ = s.sessionStore.DeleteExpiredManagementSessions(ctx, s.now())
	}
	s.sessionsMu.Lock()
	for existing, existingExpiry := range s.sessions {
		if !s.now().Before(existingExpiry) {
			delete(s.sessions, existing)
		}
	}
	s.sessions[hash] = expires
	s.sessionsMu.Unlock()
	return token, expires, nil
}

func (s *managementSecurity) revokeSessionToken(token string) {
	_ = s.revokeSessionTokenContext(context.Background(), token)
}

func (s *managementSecurity) revokeSessionTokenContext(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	hash := sha256.Sum256([]byte(token))
	s.sessionsMu.Lock()
	delete(s.sessions, hash)
	s.sessionsMu.Unlock()
	if s.sessionStore != nil {
		return s.sessionStore.DeleteManagementSession(ctx, hash[:])
	}
	return nil
}

func (s *managementSecurity) newBrowserTicket() (string, time.Time, error) {
	random := make([]byte, 32)
	if _, err := rand.Read(random); err != nil {
		return "", time.Time{}, err
	}
	ticket := base64.RawURLEncoding.EncodeToString(random)
	hash := sha256.Sum256([]byte(ticket))
	now := s.now()
	expires := now.Add(managementBrowserTicketTTL)
	s.ticketsMu.Lock()
	for existing, existingExpiry := range s.tickets {
		if !now.Before(existingExpiry) {
			delete(s.tickets, existing)
		}
	}
	s.tickets[hash] = expires
	s.ticketsMu.Unlock()
	return ticket, expires, nil
}

func (s *managementSecurity) consumeBrowserTicketValue(ticket string) bool {
	ticket = strings.TrimSpace(ticket)
	if ticket == "" {
		return false
	}
	hash := sha256.Sum256([]byte(ticket))
	now := s.now()
	s.ticketsMu.Lock()
	expires, ok := s.tickets[hash]
	delete(s.tickets, hash)
	s.ticketsMu.Unlock()
	return ok && now.Before(expires)
}

func (s *managementSecurity) setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     managementSessionCookieName,
		Value:    token,
		Path:     "/api",
		Expires:  expires.UTC(),
		MaxAge:   int(managementSessionTTL / time.Second),
		HttpOnly: true,
		Secure:   requestUsesHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func (s *managementSecurity) clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     managementSessionCookieName,
		Path:     "/api",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   requestUsesHTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func requestUsesHTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(firstForwardedHeaderValue(r.Header.Get("X-Forwarded-Proto")), "https")
}

func normalizeManagementOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.User != nil || parsed.Host == "" || parsed.Path != "" || parsed.RawQuery != "" || parsed.Fragment != "" {
		return ""
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ""
	}
	return scheme + "://" + strings.ToLower(parsed.Host)
}

func isManagementSessionRoute(path string) bool {
	path = strings.TrimSuffix(path, "/")
	return path == "/api/auth/session" || path == "/api/auth/browser-tickets" || strings.HasPrefix(path, "/api/auth/browser-tickets/")
}

func isUnsafeManagementMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

func isAllowedManagementMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete, http.MethodOptions:
		return true
	default:
		return false
	}
}

func bearerToken(header string) string {
	parts := strings.Fields(header)
	if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
		return ""
	}
	return strings.TrimSpace(parts[1])
}

func constantTimeEqual(left string, right string) bool {
	if len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}

func addVaryHeader(header http.Header, value string) {
	for _, current := range header.Values("Vary") {
		for _, item := range strings.Split(current, ",") {
			if strings.EqualFold(strings.TrimSpace(item), value) {
				return
			}
		}
	}
	header.Add("Vary", value)
}
