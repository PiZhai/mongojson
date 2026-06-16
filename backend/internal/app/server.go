package app

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/cors"

	"mongojson/backend/internal/config"
	"mongojson/backend/internal/httpapi"
	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/platform/storage"
	"mongojson/backend/internal/service/filemeta"
	"mongojson/backend/internal/service/jobs"
	"mongojson/backend/internal/service/presets"
)

type Server struct {
	Config config.Config
	Router http.Handler

	db        *database.DB
	jobWorker *jobs.Worker
	cleanup   *jobs.CleanupLoop
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

	worker := jobs.NewWorker(jobService, cfg)
	worker.Start(context.Background())

	cleanup := jobs.NewCleanupLoop(jobService, 30*time.Minute)
	cleanup.Start(context.Background())

	router := chi.NewRouter()
	router.Use(cors.Handler(cors.Options{
		AllowedOrigins:   []string{"*"},
		AllowedMethods:   []string{"GET", "POST", "OPTIONS"},
		AllowedHeaders:   []string{"Accept", "Authorization", "Content-Type"},
		AllowCredentials: false,
		MaxAge:           300,
	}))

	httpapi.RegisterRoutes(router, httpapi.Dependencies{
		Config:        cfg,
		FileService:   fileService,
		JobService:    jobService,
		PresetService: presetService,
	})

	return &Server{
		Config:    cfg,
		Router:    router,
		db:        db,
		jobWorker: worker,
		cleanup:   cleanup,
	}, nil
}

func (s *Server) Shutdown(ctx context.Context) {
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
