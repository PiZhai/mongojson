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
	"syscall"
	"time"

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
	apiBase := fs.String("api", envOrDefault("STEWARD_API_BASE", "http://127.0.0.1:18080/api"), "local steward API base")
	dbPath := fs.String("db", filepath.Join(dataDir, "MongojsonSteward", "companion.db"), "encrypted row buffer SQLite path")
	flushInterval := fs.Duration("flush-interval", 10*time.Second, "buffer flush interval")
	_ = fs.Parse(os.Args[1:])
	if *listen != "" {
		if err := validateLoopbackListen(*listen); err != nil {
			log.Fatal(err)
		}
	}
	if strings.TrimSpace(*pipe) == "" {
		log.Fatal("companion named pipe is required")
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

	mux := http.NewServeMux()
	mux.HandleFunc("GET /status", func(w http.ResponseWriter, r *http.Request) {
		count, err := buffer.Pending(r.Context())
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ready", "pending": count, "capacity": stewardcompanion.DefaultMaxPending,
			"allowed_data_levels": buffer.AllowedDataLevels(),
		})
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
		result, err := steward.ExecuteCompanionToolPackage(r.Context(), input.Manifest, input.PackageDir, input.Input)
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
	listener, err := companionPipeListener(*pipe)
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
				result, err := buffer.Flush(ctx, *apiBase, nil, 200)
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
