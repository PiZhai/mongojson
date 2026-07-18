package httpapi

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/gorilla/websocket"

	"mongojson/backend/internal/config"
)

func TestManagementSecurityDisabledStillRejectsCrossOriginRequests(t *testing.T) {
	handler := managementSecurityTestRouter(config.Config{})

	sameOrigin := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/protected", nil)
	sameOrigin.Header.Set("Origin", "http://127.0.0.1:18080")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, sameOrigin)
	if recorder.Code != http.StatusOK {
		t.Fatalf("same-origin development request status = %d: %s", recorder.Code, recorder.Body.String())
	}

	hostile := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/protected", nil)
	hostile.Header.Set("Origin", "https://attacker.example")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, hostile)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("cross-origin request status = %d, want 403", recorder.Code)
	}
	if recorder.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Fatal("rejected origin received an Access-Control-Allow-Origin header")
	}
}

func TestManagementSecurityRejectsDNSRebindingHost(t *testing.T) {
	handler := managementSecurityTestRouter(config.Config{})

	request := httptest.NewRequest(http.MethodPost, "http://attacker.example/api/protected", nil)
	request.Host = "attacker.example"
	request.Header.Set("Origin", "http://attacker.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("DNS-rebinding request status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManagementSecuritySupportsBearerAndExplicitSessionExchange(t *testing.T) {
	const token = "management-token-0123456789abcdef"
	handler := managementSecurityTestRouter(config.Config{
		ManagementAuthRequired: true,
		ManagementAuthToken:    token,
	})

	unauthorized := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080/api/protected", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, unauthorized)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated request status = %d, want 401", recorder.Code)
	}

	bearer := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/protected", nil)
	bearer.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, bearer)
	if recorder.Code != http.StatusOK {
		t.Fatalf("bearer request status = %d: %s", recorder.Code, recorder.Body.String())
	}

	invalidLoginBody, _ := json.Marshal(map[string]string{"token": "wrong-management-token"})
	invalidLogin := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/auth/session", bytes.NewReader(invalidLoginBody))
	invalidLogin.Header.Set("Origin", "http://127.0.0.1:18080")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, invalidLogin)
	if recorder.Code != http.StatusUnauthorized || len(recorder.Result().Cookies()) != 0 {
		t.Fatalf("invalid session exchange status = %d cookies=%d, want 401 and no cookie", recorder.Code, len(recorder.Result().Cookies()))
	}

	loginBody, _ := json.Marshal(map[string]string{"token": token})
	login := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/auth/session", bytes.NewReader(loginBody))
	login.Header.Set("Origin", "http://127.0.0.1:18080")
	login.Header.Set("Content-Type", "application/json")
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, login)
	if recorder.Code != http.StatusOK {
		t.Fatalf("session exchange status = %d: %s", recorder.Code, recorder.Body.String())
	}
	var exchange struct {
		SessionToken string `json:"session_token"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &exchange); err != nil {
		t.Fatalf("decode session exchange: %v", err)
	}
	if len(exchange.SessionToken) < 32 || strings.Contains(exchange.SessionToken, token) {
		t.Fatalf("session exchange returned an invalid session token: %q", exchange.SessionToken)
	}
	if len(recorder.Result().Cookies()) != 0 {
		t.Fatal("plaintext loopback session exchange must not issue a host-wide cookie")
	}

	read := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080/api/protected", nil)
	read.Header.Set("Authorization", "Bearer "+exchange.SessionToken)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, read)
	if recorder.Code != http.StatusOK {
		t.Fatalf("session read status = %d: %s", recorder.Code, recorder.Body.String())
	}

	writeWithoutOrigin := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/protected", nil)
	writeWithoutOrigin.Header.Set("Authorization", "Bearer "+exchange.SessionToken)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, writeWithoutOrigin)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("session write without Origin status = %d, want 403", recorder.Code)
	}

	write := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/protected", nil)
	write.Header.Set("Origin", "http://127.0.0.1:18080")
	write.Header.Set("Authorization", "Bearer "+exchange.SessionToken)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, write)
	if recorder.Code != http.StatusOK {
		t.Fatalf("same-origin session write status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManagementSecurityFailsClosedWhenRequiredTokenIsMissing(t *testing.T) {
	handler := managementSecurityTestRouter(config.Config{ManagementAuthRequired: true})

	protected := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080/api/protected", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, protected)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("protected request status = %d, want 401: %s", recorder.Code, recorder.Body.String())
	}

	for _, body := range []string{`{}`, `{"token":""}`} {
		login := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/auth/session", strings.NewReader(body))
		login.Header.Set("Origin", "http://127.0.0.1:18080")
		recorder = httptest.NewRecorder()
		handler.ServeHTTP(recorder, login)
		if recorder.Code != http.StatusServiceUnavailable || len(recorder.Result().Cookies()) != 0 {
			t.Fatalf("unconfigured session exchange status = %d cookies=%d, want 503 and no cookie", recorder.Code, len(recorder.Result().Cookies()))
		}
	}
}

func TestManagementSecuritySessionExpiresAndCannotBeForged(t *testing.T) {
	const token = "management-token-0123456789abcdef"
	security := newManagementSecurity(true, token, nil, false)
	issuedAt := time.Unix(1_800_000_000, 0)
	security.now = func() time.Time { return issuedAt }

	session, _, err := security.issueSessionToken()
	if err != nil {
		t.Fatalf("issue session token: %v", err)
	}

	security.now = func() time.Time { return issuedAt.Add(managementSessionTTL + time.Second) }
	read := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080/api/protected", nil)
	read.Header.Set("Authorization", "Bearer "+session)
	if security.validSessionBearer(read.Header.Get("Authorization")) {
		t.Fatal("expired management session remained valid")
	}

	security.now = func() time.Time { return issuedAt }
	if security.validSessionBearer("Bearer " + session + "forged") {
		t.Fatal("forged management session was accepted")
	}
}

func TestManagementSecuritySessionCanBeRevoked(t *testing.T) {
	security := newManagementSecurity(true, "management-token-0123456789abcdef", nil, false)
	session, _, err := security.issueSessionToken()
	if err != nil {
		t.Fatalf("issue session token: %v", err)
	}
	if !security.validSessionBearer("Bearer " + session) {
		t.Fatal("new session token was not accepted")
	}
	security.revokeSessionToken(session)
	if security.validSessionBearer("Bearer " + session) {
		t.Fatal("revoked session token remained valid")
	}
}

func TestManagementSecurityAcceptsSessionOnlyAsWebSocketSubprotocol(t *testing.T) {
	security := newManagementSecurity(true, "management-token-0123456789abcdef", nil, false)
	session, _, err := security.issueSessionToken()
	if err != nil {
		t.Fatalf("issue session token: %v", err)
	}
	handler := security.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("Sec-WebSocket-Protocol"); got != "" {
			t.Fatalf("management subprotocol leaked to websocket handler: %q", got)
		}
		w.WriteHeader(http.StatusNoContent)
	}))

	websocketRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080/api/protected", nil)
	websocketRequest.Header.Set("Connection", "Upgrade")
	websocketRequest.Header.Set("Upgrade", "websocket")
	websocketRequest.Header.Set("Sec-WebSocket-Protocol", "steward-management."+session)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, websocketRequest)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("websocket session status = %d: %s", recorder.Code, recorder.Body.String())
	}

	ordinaryRequest := httptest.NewRequest(http.MethodGet, "http://127.0.0.1:18080/api/protected", nil)
	ordinaryRequest.Header.Set("Sec-WebSocket-Protocol", "steward-management."+session)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, ordinaryRequest)
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("ordinary HTTP subprotocol status = %d, want 401", recorder.Code)
	}
}

func TestManagementSecurityWebSocketSubprotocolCompletesUpgrade(t *testing.T) {
	security := newManagementSecurity(true, "management-token-0123456789abcdef", nil, false)
	session, _, err := security.issueSessionToken()
	if err != nil {
		t.Fatalf("issue session token: %v", err)
	}
	upgrader := websocket.Upgrader{}
	server := httptest.NewServer(security.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		connection, upgradeErr := upgrader.Upgrade(w, r, nil)
		if upgradeErr == nil {
			_ = connection.Close()
		}
	})))
	defer server.Close()

	dialer := websocket.Dialer{Subprotocols: []string{"steward-management." + session}}
	connection, response, err := dialer.Dial("ws"+strings.TrimPrefix(server.URL, "http"), nil)
	if err != nil {
		status := 0
		if response != nil {
			status = response.StatusCode
		}
		t.Fatalf("authenticated websocket upgrade failed (status=%d): %v", status, err)
	}
	_ = connection.Close()
}

func TestManagementSecurityAllowsOnlyExplicitCORSOrigins(t *testing.T) {
	const token = "management-token-0123456789abcdef"
	handler := managementSecurityTestRouter(config.Config{
		ManagementAuthRequired:   true,
		ManagementAuthToken:      token,
		ManagementAllowedOrigins: []string{"https://console.example.test"},
	})

	preflight := httptest.NewRequest(http.MethodOptions, "http://127.0.0.1:18080/api/protected", nil)
	preflight.Header.Set("Origin", "https://console.example.test")
	preflight.Header.Set("Access-Control-Request-Method", http.MethodPost)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, preflight)
	if recorder.Code != http.StatusNoContent {
		t.Fatalf("allowed preflight status = %d: %s", recorder.Code, recorder.Body.String())
	}
	if got := recorder.Header().Get("Access-Control-Allow-Origin"); got != "https://console.example.test" {
		t.Fatalf("allowed origin response header = %q", got)
	}

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/protected", nil)
	request.Header.Set("Origin", "https://console.example.test")
	request.Header.Set("Authorization", "Bearer "+token)
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("explicit-origin bearer request status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManagementSecurityUsesTrustedForwardedOriginForRemoteManagement(t *testing.T) {
	const token = "management-token-0123456789abcdef"
	handler := managementSecurityTestRouter(config.Config{
		AllowRemoteManagement:    true,
		ManagementAuthToken:      token,
		ManagementAllowedOrigins: []string{"https://tools.example.test:8443"},
	})

	request := httptest.NewRequest(http.MethodPost, "http://backend:8080/api/protected", nil)
	request.Host = "backend:8080"
	request.Header.Set("Origin", "https://tools.example.test:8443")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "tools.example.test:8443")
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("forwarded same-origin request status = %d: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManagementSecurityDoesNotTrustUnconfiguredForwardedOrigin(t *testing.T) {
	const token = "management-token-0123456789abcdef"
	handler := managementSecurityTestRouter(config.Config{
		AllowRemoteManagement: true,
		ManagementAuthToken:   token,
	})

	request := httptest.NewRequest(http.MethodPost, "http://backend:8080/api/protected", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("unconfigured forwarded-origin status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManagementSecurityIgnoresForwardedOriginInLoopbackOnlyMode(t *testing.T) {
	handler := managementSecurityTestRouter(config.Config{})

	request := httptest.NewRequest(http.MethodPost, "http://127.0.0.1:18080/api/protected", nil)
	request.Header.Set("Origin", "https://attacker.example")
	request.Header.Set("X-Forwarded-Proto", "https")
	request.Header.Set("X-Forwarded-Host", "attacker.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("spoofed forwarded-origin request status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
}

func TestManagementSecurityRejectsInvalidTrustedForwardedProtocol(t *testing.T) {
	const token = "management-token-0123456789abcdef"
	handler := managementSecurityTestRouter(config.Config{
		AllowRemoteManagement: true,
		ManagementAuthToken:   token,
	})

	request := httptest.NewRequest(http.MethodPost, "http://backend:8080/api/protected", nil)
	request.Header.Set("Origin", "https://tools.example.test")
	request.Header.Set("X-Forwarded-Proto", "javascript")
	request.Header.Set("X-Forwarded-Host", "tools.example.test")
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusForbidden {
		t.Fatalf("invalid forwarded protocol status = %d, want 403: %s", recorder.Code, recorder.Body.String())
	}
}

func managementSecurityTestRouter(cfg config.Config) http.Handler {
	router := chi.NewRouter()
	security := newManagementSecurity(cfg.ManagementAuthRequired, cfg.ManagementAuthToken, cfg.ManagementAllowedOrigins, cfg.AllowRemoteManagement)
	router.Route("/api", func(r chi.Router) {
		r.Use(security.middleware)
		r.Get("/auth/session", security.getSession)
		r.Post("/auth/session", security.exchangeSession)
		r.Delete("/auth/session", security.deleteSession)
		r.Get("/protected", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
		r.Post("/protected", func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusOK) })
	})
	return router
}
