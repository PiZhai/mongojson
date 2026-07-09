package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"mongojson/backend/internal/config"
	"mongojson/backend/internal/httpapi"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/platform/storage"
	"mongojson/backend/internal/service/filemeta"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/memo"
	"mongojson/backend/internal/service/presets"
	"mongojson/backend/internal/service/steward"
	"mongojson/backend/internal/service/watchsync"
)

type Server struct {
	Config           config.Config
	ManagementRouter http.Handler
	PeerRouter       http.Handler

	db            *database.DB
	jobWorker     *jobs.Worker
	cleanup       *jobs.CleanupLoop
	stewardDaemon *steward.Daemon
}

func NewServer() (*Server, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if err := os.MkdirAll(filepath.Join(cfg.StorageDir, "uploads"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir uploads: %w", err)
	}
	if err := os.MkdirAll(filepath.Join(cfg.StorageDir, "outputs"), 0o755); err != nil {
		return nil, fmt.Errorf("mkdir outputs: %w", err)
	}

	db, err := database.Connect(context.Background(), cfg.DatabaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect database: %w", err)
	}
	if err := db.Migrate(context.Background()); err != nil {
		return nil, fmt.Errorf("migrate database: %w", err)
	}

	fileStore := storage.NewLocalStore(cfg.StorageDir)
	fileService := filemeta.NewService(db, fileStore, cfg.FileRetention)
	jobService := jobs.NewService(db, fileStore, cfg.FileRetention)
	presetService := presets.NewService(db)
	memoService := memo.NewService(db)
	stewardService := steward.NewService(db)
	watchSyncHub := watchsync.NewHub()
	if err := stewardService.EnsureDefaults(context.Background()); err != nil {
		return nil, fmt.Errorf("ensure steward defaults: %w", err)
	}

	stewardDaemon := steward.NewDaemon(stewardService, steward.DaemonOptionsFromEnv())
	stewardDaemon.Start(context.Background())

	worker := jobs.NewWorker(jobService, cfg)
	worker.Start(context.Background())

	cleanup := jobs.NewCleanupLoop(jobService, 30*time.Minute)
	cleanup.Start(context.Background())

	deps := httpapi.Dependencies{
		Config:         cfg,
		FileService:    fileService,
		JobService:     jobService,
		MemoService:    memoService,
		PresetService:  presetService,
		StewardService: stewardService,
		WatchSync:      watchSyncHub,
		Readiness:      readinessChecker(cfg, db, worker, stewardDaemon),
	}

	managementRouter := chi.NewRouter()
	managementRouter.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	httpapi.RegisterManagementRoutes(managementRouter, deps)
	managementHandler, err := withStaticWorkspace(managementRouter, cfg.StewardUIDir)
	if err != nil {
		return nil, err
	}

	peerRouter := chi.NewRouter()
	httpapi.RegisterPeerRoutes(peerRouter, httpapi.PeerDependencies{
		StewardService: stewardService,
		Readiness:      deps.Readiness,
	})

	return &Server{
		Config:           cfg,
		ManagementRouter: managementHandler,
		PeerRouter:       peerRouter,
		db:               db,
		jobWorker:        worker,
		cleanup:          cleanup,
		stewardDaemon:    stewardDaemon,
	}, nil
}

func readinessChecker(cfg config.Config, db *database.DB, worker *jobs.Worker, stewardDaemon *steward.Daemon) func(context.Context) (map[string]string, error) {
	return func(ctx context.Context) (map[string]string, error) {
		checks := map[string]string{}
		var failures []string

		if err := db.Ping(ctx); err != nil {
			checks["database"] = "error: " + err.Error()
			failures = append(failures, "database")
		} else {
			checks["database"] = "ok"
		}

		if err := checkStorage(cfg.StorageDir); err != nil {
			checks["storage"] = "error: " + err.Error()
			failures = append(failures, "storage")
		} else {
			checks["storage"] = "ok"
		}

		if worker.IsRunning() {
			checks["worker"] = "ok"
		} else {
			checks["worker"] = "error: not running"
			failures = append(failures, "worker")
		}

		if stewardDaemon.IsRunning() {
			checks["steward_daemon"] = "ok"
		} else {
			checks["steward_daemon"] = "error: not running"
			failures = append(failures, "steward_daemon")
		}

		if len(failures) > 0 {
			return checks, fmt.Errorf("readiness checks failed: %s", strings.Join(failures, ", "))
		}
		return checks, nil
	}
}

func checkStorage(root string) error {
	if root == "" {
		return fmt.Errorf("storage dir is empty")
	}

	for _, dir := range []string{root, filepath.Join(root, "uploads"), filepath.Join(root, "outputs")} {
		info, err := os.Stat(dir)
		if err != nil {
			return fmt.Errorf("stat %s: %w", dir, err)
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", dir)
		}
	}

	file, err := os.CreateTemp(root, ".readyz-*")
	if err != nil {
		return fmt.Errorf("create readiness probe file: %w", err)
	}
	name := file.Name()
	if _, err := file.Write([]byte("ok")); err != nil {
		_ = file.Close()
		_ = os.Remove(name)
		return fmt.Errorf("write readiness probe file: %w", err)
	}
	if err := file.Close(); err != nil {
		_ = os.Remove(name)
		return fmt.Errorf("close readiness probe file: %w", err)
	}
	if err := os.Remove(name); err != nil {
		return fmt.Errorf("remove readiness probe file: %w", err)
	}
	return nil
}

func (s *Server) Shutdown(ctx context.Context) {
	if s.stewardDaemon != nil {
		s.stewardDaemon.Stop()
	}
	if s.cleanup != nil {
		s.cleanup.Stop()
	}
	if s.jobWorker != nil {
		s.jobWorker.Stop()
	}
	if s.db != nil {
		s.db.Close()
	}
	select {
	case <-ctx.Done():
	default:
	}
}
