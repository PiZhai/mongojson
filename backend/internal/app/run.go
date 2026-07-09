package app

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"
)

// Run starts the HTTP API and keeps it alive until ctx is canceled.
func Run(ctx context.Context) error {
	appServer, err := NewServer()
	if err != nil {
		return fmt.Errorf("bootstrap server: %w", err)
	}

	listeners := []httpListener{{
		role: "management",
		server: &http.Server{
			Addr:              appServer.Config.HTTPAddr,
			Handler:           appServer.ManagementRouter,
			ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout:       2 * time.Minute,
			WriteTimeout:      2 * time.Minute,
			IdleTimeout:       2 * time.Minute,
			MaxHeaderBytes:    1 << 20,
		},
	}}
	if strings.TrimSpace(appServer.Config.PeerHTTPAddr) != "" {
		listeners = append(listeners, httpListener{
			role: "peer",
			server: &http.Server{
				Addr:              appServer.Config.PeerHTTPAddr,
				Handler:           appServer.PeerRouter,
				ReadHeaderTimeout: 10 * time.Second,
				ReadTimeout:       30 * time.Second,
				WriteTimeout:      30 * time.Second,
				IdleTimeout:       90 * time.Second,
				MaxHeaderBytes:    1 << 20,
			},
		})
	}

	errCh := make(chan error, len(listeners))
	for _, listener := range listeners {
		listener := listener
		go func() {
			log.Printf("backend %s API listening on %s", listener.role, listener.server.Addr)
			err := listener.server.ListenAndServe()
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				errCh <- fmt.Errorf("%s listener: %w", listener.role, err)
				return
			}
			errCh <- nil
		}()
	}

	var runErr error
	select {
	case <-ctx.Done():
	case err := <-errCh:
		if err != nil {
			runErr = fmt.Errorf("listen: %w", err)
		} else {
			runErr = fmt.Errorf("HTTP listener stopped unexpectedly")
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var shutdownErrs []error
	for _, listener := range listeners {
		if err := listener.server.Shutdown(shutdownCtx); err != nil {
			shutdownErrs = append(shutdownErrs, fmt.Errorf("shutdown %s listener: %w", listener.role, err))
			_ = listener.server.Close()
		}
	}
	appServer.Shutdown(shutdownCtx)
	shutdownErrs = append(shutdownErrs, runErr)
	return errors.Join(shutdownErrs...)
}

type httpListener struct {
	role   string
	server *http.Server
}
