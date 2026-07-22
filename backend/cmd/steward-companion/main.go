package main

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
	"mongojson/backend/internal/service/steward"
	"mongojson/backend/internal/service/stewardcompanion"
)

func main() {
	logFile := configureCompanionLogging()
	if logFile != nil {
		defer logFile.Close()
	}
	dataDir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	fs := flag.NewFlagSet("steward-companion", flag.ExitOnError)
	listen := fs.String("listen", envOrDefault("STEWARD_COMPANION_ADDR", ""), "optional loopback companion HTTP address")
	pipe := fs.String("pipe", envOrDefault("STEWARD_COMPANION_PIPE", `\\.\pipe\MongojsonStewardCompanion`), "authenticated companion named pipe")
	serviceName := fs.String("service-name", "MongojsonSteward", "Windows service name allowed to connect to the companion pipe")
	privateEnvironmentFile := fs.String("private-environment-file", "", "Protected JSON file containing the local encryption key")
	managementAccessTokenFile := fs.String("management-access-token-file", envOrDefault("STEWARD_MANAGEMENT_ACCESS_TOKEN_FILE", defaultManagementAccessTokenFile()), "protected file containing the local management API bearer token")
	requireManagementToken := fs.Bool("require-management-token", boolEnvOrDefault("STEWARD_COMPANION_REQUIRE_MANAGEMENT_TOKEN", true), "fail closed unless a management API bearer token is available; set false only for explicit unauthenticated development")
	apiBase := fs.String("api", envOrDefault("STEWARD_API_BASE", "http://127.0.0.1:18080/api"), "local steward API base")
	openWorkspace := fs.Bool("open-workspace", false, "authenticate the current Windows user and open the Steward workspace in the default browser")
	dbPath := fs.String("db", filepath.Join(dataDir, "MongojsonSteward", "companion.db"), "encrypted row buffer SQLite path")
	flushInterval := fs.Duration("flush-interval", 10*time.Second, "buffer flush interval")
	controlInterval := fs.Duration("control-interval", 15*time.Second, "main-service capture control refresh interval")
	captureInterval := fs.Duration("capture-interval", durationEnvOrDefault("STEWARD_COMPANION_CAPTURE_INTERVAL", stewardcompanion.DefaultCaptureInterval), "interactive Windows activity sample interval")
	afkThreshold := fs.Duration("afk-threshold", durationEnvOrDefault("STEWARD_COMPANION_AFK_THRESHOLD", stewardcompanion.DefaultAFKThreshold), "idle duration before the session is considered AFK")
	segmentDuration := fs.Duration("capture-segment-duration", durationEnvOrDefault("STEWARD_COMPANION_CAPTURE_SEGMENT_DURATION", stewardcompanion.DefaultSegmentDuration), "maximum duration of one revisioned activity segment")
	captureEnabled := fs.Bool("capture", boolEnvOrDefault("STEWARD_COMPANION_CAPTURE_ENABLED", true), "capture foreground and AFK activity in the logged-in session")
	_ = fs.Parse(os.Args[1:])
	if strings.TrimSpace(*privateEnvironmentFile) != "" {
		if err := servicecontrol.LoadPrivateEnvironmentFile(*privateEnvironmentFile); err != nil {
			log.Fatal(err)
		}
	}
	if *listen != "" {
		if err := validateLoopbackListen(*listen); err != nil {
			log.Fatal(err)
		}
	}
	if strings.TrimSpace(*pipe) == "" {
		log.Fatal("companion named pipe is required")
	}
	managementToken, err := readSingleLineSecret(*managementAccessTokenFile, *requireManagementToken)
	if err != nil {
		log.Fatal(err)
	}
	managementClient, err := stewardcompanion.NewManagementHTTPClient(managementToken, 20*time.Second)
	if err != nil {
		log.Fatal(err)
	}
	if *openWorkspace {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		launchURL, err := stewardcompanion.RequestBrowserLaunchURL(ctx, *apiBase, managementClient)
		if err != nil {
			log.Fatal(err)
		}
		if err := openDefaultBrowser(launchURL); err != nil {
			log.Fatal(err)
		}
		return
	}
	key, err := companionKey()
	if err != nil {
		log.Fatal(err)
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	buffer, err := stewardcompanion.Open(ctx, stewardcompanion.Options{
		Path:              *dbPath,
		Key:               key,
		MaxPending:        stewardcompanion.DefaultMaxPending,
		AllowedDataLevels: companionDataLevels(),
	})
	if err != nil {
		log.Fatal(err)
	}
	defer buffer.Close()
	var captureLoop *stewardcompanion.CaptureLoop
	var flushEnabled atomic.Bool
	flushEnabled.Store(!*requireManagementToken)
	runtimeHealth := newCompanionRuntimeHealth(*requireManagementToken, managementToken != "")
	if *captureEnabled {
		captureLoop = stewardcompanion.NewCaptureLoop(
			stewardcompanion.NewNativeActivitySampler(),
			buffer,
			stewardcompanion.CaptureOptions{Interval: *captureInterval, AFKThreshold: *afkThreshold, SegmentDuration: *segmentDuration, Logger: log.Default()},
		)
		if *requireManagementToken {
			// Production starts fail-closed. It can be enabled only by a live
			// authenticated control refresh or the encrypted snapshot from the last
			// refresh authenticated by this exact endpoint and credential.
			captureLoop.ApplyControl(false, stewardcompanion.CaptureOptions{
				Interval: *captureInterval, AFKThreshold: *afkThreshold, SegmentDuration: *segmentDuration,
			})
		}
	}
	controlCacheBinding := ""
	if managementToken != "" {
		controlCacheBinding, err = stewardcompanion.CaptureControlCacheBinding(*apiBase, managementToken)
		if err != nil {
			log.Fatalf("prepare authenticated capture-control cache: %v", err)
		}
	}
	applyControl := func(control stewardcompanion.CaptureControl) {
		flushEnabled.Store(control.FlushEnabled)
		if captureLoop != nil {
			captureLoop.ApplyControl(control.CaptureEnabled, stewardcompanion.CaptureOptions{
				Interval: control.Interval, AFKThreshold: *afkThreshold, SegmentDuration: *segmentDuration,
				Timezone: control.Timezone,
			})
		}
	}
	if controlCacheBinding != "" {
		cached, cacheErr := buffer.LoadAuthenticatedCaptureControl(ctx, controlCacheBinding)
		switch {
		case cacheErr == nil:
			applyControl(cached.Control)
			log.Printf("using authenticated capture-control cache from %s while refreshing the main service", cached.AuthenticatedAt.Format(time.RFC3339))
		case !errors.Is(cacheErr, stewardcompanion.ErrNoCachedCaptureControl):
			log.Printf("authenticated capture-control cache is unavailable: %v", cacheErr)
		}
	}
	lastControlError := ""
	refreshControl := func() {
		controlCtx, controlCancel := context.WithTimeout(ctx, 5*time.Second)
		defer controlCancel()
		control, controlErr := stewardcompanion.FetchCaptureControl(controlCtx, *apiBase, managementClient)
		runtimeHealth.recordControl(controlErr)
		if controlErr != nil {
			message := controlErr.Error()
			if message != lastControlError {
				log.Printf("companion control refresh failed; retaining last state: %v", controlErr)
				lastControlError = message
			}
			if *requireManagementToken && isCompanionAuthenticationError(message) {
				flushEnabled.Store(false)
				if captureLoop != nil {
					captureLoop.ApplyControl(false, stewardcompanion.CaptureOptions{
						Interval: *captureInterval, AFKThreshold: *afkThreshold, SegmentDuration: *segmentDuration,
					})
				}
			}
			return
		}
		if lastControlError != "" {
			log.Printf("companion control refresh recovered")
			lastControlError = ""
		}
		if cacheErr := cacheAuthenticatedCaptureControl(controlCtx, buffer, controlCacheBinding, managementToken != "", control, time.Now().UTC()); cacheErr != nil {
			runtimeHealth.recordControl(cacheErr)
			log.Printf("companion received live control but could not persist its authenticated cache: %v", cacheErr)
		}
		applyControl(control)
	}
	refreshControl()
	if captureLoop != nil {
		go captureLoop.Run(ctx)
	}
	go func() {
		interval := *controlInterval
		if interval <= 0 {
			interval = 15 * time.Second
		}
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				refreshControl()
			}
		}
	}()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		count, err := buffer.Pending(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		captureStatus := stewardcompanion.CaptureStatus{Enabled: false}
		if captureLoop != nil {
			captureStatus = captureLoop.Status()
		}
		status := runtimeHealth.statusPayload(flushEnabled.Load(), captureStatus, count, stewardcompanion.DefaultMaxPending, buffer.AllowedDataLevels(), *apiBase)
		writeJSON(w, http.StatusOK, status)
	})
	mux.HandleFunc("POST /observations", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 44<<20)
		var input steward.CreateObservationInput
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid observation JSON", http.StatusBadRequest)
			return
		}
		id, err := buffer.Enqueue(r.Context(), input)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, steward.ErrCredentialDataBlocked) {
				status = http.StatusForbidden
			} else if errors.Is(err, stewardcompanion.ErrBufferFull) {
				status = http.StatusInsufficientStorage
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "status": "buffered"})
	})
	mux.HandleFunc("POST /notification-feedback", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
		var input stewardcompanion.NotificationFeedbackEnvelope
		if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
			http.Error(w, "invalid notification feedback JSON", http.StatusBadRequest)
			return
		}
		input.CallbackToken = strings.TrimSpace(input.CallbackToken)
		input.Action = strings.ToLower(strings.TrimSpace(input.Action))
		if input.CallbackToken == "" || input.Action == "" {
			http.Error(w, "callback_token and action are required", http.StatusBadRequest)
			return
		}
		if input.OccurredAt.IsZero() {
			input.OccurredAt = time.Now().UTC()
		} else {
			input.OccurredAt = input.OccurredAt.UTC()
		}
		id, err := buffer.EnqueueEnvelope(r.Context(), stewardcompanion.EnvelopeNotificationFeedback, input.EventKey(), 1, input)
		if err != nil {
			status := http.StatusBadRequest
			if errors.Is(err, stewardcompanion.ErrBufferFull) {
				status = http.StatusInsufficientStorage
			}
			http.Error(w, err.Error(), status)
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]string{"id": id, "status": "buffered"})
	})
	mux.HandleFunc("POST /tools/execute", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 64<<20)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid tool request", http.StatusBadRequest)
			return
		}
		timestamp := strings.TrimSpace(r.Header.Get("X-Steward-Tool-Timestamp"))
		unix, parseErr := strconv.ParseInt(timestamp, 10, 64)
		if parseErr != nil || time.Since(time.Unix(unix, 0)).Abs() > 60*time.Second {
			http.Error(w, "stale tool request", http.StatusUnauthorized)
			return
		}
		expected := companionPayloadSignature(key, timestamp, payload)
		provided, decodeErr := hex.DecodeString(strings.TrimSpace(r.Header.Get("X-Steward-Tool-Signature")))
		expectedBytes, _ := hex.DecodeString(expected)
		if decodeErr != nil || !hmac.Equal(provided, expectedBytes) {
			http.Error(w, "invalid tool request signature", http.StatusUnauthorized)
			return
		}
		var input struct {
			Manifest   steward.ToolPackageManifest `json:"manifest"`
			PackageDir string                      `json:"package_dir"`
			Input      map[string]any              `json:"input"`
		}
		if err := json.NewDecoder(bytes.NewReader(payload)).Decode(&input); err != nil {
			http.Error(w, "invalid tool JSON", http.StatusBadRequest)
			return
		}
		cacheRoot, cacheErr := os.UserCacheDir()
		if cacheErr != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": cacheErr.Error(), "output": map[string]any{}, "evidence": []any{}})
			return
		}
		packageDir, cacheErr := steward.PrepareCompanionToolPackage(input.Manifest, filepath.Join(cacheRoot, "MongojsonSteward", "session-tools"))
		if cacheErr != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": cacheErr.Error(), "output": map[string]any{}, "evidence": []any{}})
			return
		}
		result, err := steward.ExecuteCompanionToolPackage(r.Context(), input.Manifest, packageDir, input.Input)
		if err != nil {
			writeJSON(w, http.StatusOK, map[string]any{"ok": false, "error": err.Error(), "output": map[string]any{}, "evidence": []any{}})
			return
		}
		evidence := make([]map[string]any, 0, len(result.Evidence))
		for _, item := range result.Evidence {
			evidence = append(evidence, map[string]any{"kind": item.Kind, "summary": item.Summary, "payload": item.Payload})
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true, "output": result.Output, "evidence": evidence})
	})
	mux.HandleFunc("POST /notifications", func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		payload, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, "invalid notification request", http.StatusBadRequest)
			return
		}
		if err := verifyCompanionSignedPayload(key, r, payload); err != nil {
			http.Error(w, err.Error(), http.StatusUnauthorized)
			return
		}
		var input systemNotification
		if err := json.Unmarshal(payload, &input); err != nil {
			http.Error(w, "invalid notification JSON", http.StatusBadRequest)
			return
		}
		if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Body) == "" {
			http.Error(w, "notification title and body are required", http.StatusBadRequest)
			return
		}
		providerID, err := showSystemNotification(r.Context(), input)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": err.Error(), "recovery": "确认登录会话允许系统通知，并检查 Session Companion 日志。"})
			return
		}
		writeJSON(w, http.StatusAccepted, map[string]any{"status": "accepted", "provider_message_id": providerID})
	})

	server := &http.Server{Handler: mux, ReadHeaderTimeout: 5 * time.Second}
	listener, err := companionPipeListener(*pipe, *serviceName)
	if err != nil {
		log.Fatal(err)
	}
	go func() {
		log.Printf("steward companion listening on named pipe %s", *pipe)
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("companion named pipe server failed: %v", err)
			cancel()
		}
	}()
	var loopbackServer *http.Server
	if *listen != "" {
		loopbackServer = &http.Server{Addr: *listen, Handler: mux, ReadHeaderTimeout: 5 * time.Second}
		go func() {
			log.Printf("steward companion optional loopback listener on %s", *listen)
			if err := loopbackServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("companion loopback server failed: %v", err)
			}
		}()
	}
	go func() {
		ticker := time.NewTicker(*flushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !flushEnabled.Load() {
					continue
				}
				result, err := buffer.Flush(ctx, *apiBase, managementClient, 200)
				runtimeHealth.recordFlush(result, err)
				if err != nil {
					log.Printf("companion flush failed: %v", err)
				} else if result.Submitted > 0 || result.Failed > 0 {
					log.Printf("companion flush submitted=%d failed=%d pending=%d", result.Submitted, result.Failed, result.Pending)
				}
			}
		}
	}()
	<-ctx.Done()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = server.Shutdown(shutdownCtx)
	if loopbackServer != nil {
		_ = loopbackServer.Shutdown(shutdownCtx)
	}
}

func cacheAuthenticatedCaptureControl(ctx context.Context, buffer *stewardcompanion.Buffer, binding string, authenticated bool, control stewardcompanion.CaptureControl, now time.Time) error {
	if !authenticated {
		return nil
	}
	if buffer == nil {
		return fmt.Errorf("companion buffer is required for authenticated capture-control caching")
	}
	return buffer.SaveAuthenticatedCaptureControl(ctx, binding, control, now)
}

func defaultManagementAccessTokenFile() string {
	executable, err := os.Executable()
	if err != nil || strings.TrimSpace(executable) == "" {
		return ""
	}
	return filepath.Join(filepath.Dir(executable), "management-access-token.txt")
}

func readSingleLineSecret(path string, required bool) (string, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		if required {
			return "", fmt.Errorf("management access token file is required")
		}
		return "", nil
	}
	raw, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		if required {
			return "", fmt.Errorf("management access token file does not exist: %s", path)
		}
		// Explicit unauthenticated development can deliberately run without the
		// local management bearer token.
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("read management access token file: %w", err)
	}
	value := strings.TrimSpace(string(raw))
	if value == "" {
		return "", fmt.Errorf("management access token file is empty: %s", path)
	}
	if strings.ContainsAny(value, "\r\n") {
		return "", fmt.Errorf("management access token file must contain one line: %s", path)
	}
	return value, nil
}

func readOptionalSingleLineSecret(path string) (string, error) {
	return readSingleLineSecret(path, false)
}

func configureCompanionLogging() *os.File {
	root, err := os.UserCacheDir()
	if err != nil || strings.TrimSpace(root) == "" {
		return nil
	}
	directory := filepath.Join(root, "MongojsonSteward", "logs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return nil
	}
	file, err := os.OpenFile(filepath.Join(directory, "companion.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return nil
	}
	log.SetOutput(file)
	return file
}

func companionKey() ([]byte, error) {
	value := strings.TrimSpace(os.Getenv("STEWARD_LOCAL_ENCRYPTION_KEY"))
	if value == "" {
		return nil, fmt.Errorf("STEWARD_LOCAL_ENCRYPTION_KEY is required")
	}
	key, err := base64.StdEncoding.DecodeString(value)
	if err != nil || len(key) != 32 {
		return nil, fmt.Errorf("STEWARD_LOCAL_ENCRYPTION_KEY must be base64-encoded 32 bytes")
	}
	return key, nil
}

func companionPayloadSignature(key []byte, timestamp string, payload []byte) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(timestamp))
	_, _ = mac.Write([]byte("\n"))
	_, _ = mac.Write(payload)
	return hex.EncodeToString(mac.Sum(nil))
}

func verifyCompanionSignedPayload(key []byte, request *http.Request, payload []byte) error {
	timestamp := strings.TrimSpace(request.Header.Get("X-Steward-Tool-Timestamp"))
	unix, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil || time.Since(time.Unix(unix, 0)).Abs() > 60*time.Second {
		return fmt.Errorf("stale companion request")
	}
	expected, _ := hex.DecodeString(companionPayloadSignature(key, timestamp, payload))
	provided, err := hex.DecodeString(strings.TrimSpace(request.Header.Get("X-Steward-Tool-Signature")))
	if err != nil || !hmac.Equal(provided, expected) {
		return fmt.Errorf("invalid companion request signature")
	}
	return nil
}

func validateLoopbackListen(value string) error {
	host, _, err := net.SplitHostPort(strings.TrimSpace(value))
	if err != nil {
		return fmt.Errorf("invalid companion listen address: %w", err)
	}
	if strings.EqualFold(host, "localhost") {
		return nil
	}
	ip := net.ParseIP(host)
	if ip == nil || !ip.IsLoopback() {
		return fmt.Errorf("companion must listen on a loopback address")
	}
	return nil
}

func envOrDefault(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func durationEnvOrDefault(key string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func boolEnvOrDefault(key string, fallback bool) bool {
	value := strings.TrimSpace(os.Getenv(key))
	if value == "" {
		return fallback
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return fallback
	}
	return parsed
}

func companionDataLevels() []string {
	value := strings.TrimSpace(os.Getenv("STEWARD_COMPANION_COLLECT_DATA_LEVELS"))
	if value == "" {
		return nil
	}
	return strings.FieldsFunc(value, func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n'
	})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
