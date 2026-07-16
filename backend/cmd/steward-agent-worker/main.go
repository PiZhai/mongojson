package main

import (
	"context"
	"encoding/base64"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/google/uuid"

	"mongojson/backend/internal/platform/database"
	"mongojson/backend/internal/service/steward"
)

func main() {
	agentID := flag.String("agent", strings.TrimSpace(os.Getenv("STEWARD_WORKER_AGENT_ID")), "registered R4 Agent id")
	workerID := flag.String("worker-id", strings.TrimSpace(os.Getenv("STEWARD_WORKER_ID")), "stable worker process id")
	poll := flag.Duration("poll", durationEnv("STEWARD_WORKER_POLL_INTERVAL", 250*time.Millisecond), "mailbox poll interval")
	printVerifyKey := flag.Bool("print-verify-key", false, "print the public verification key derived from STEWARD_ORCHESTRATION_SIGNING_KEY")
	flag.Parse()
	if *printVerifyKey {
		seed, err := base64.StdEncoding.DecodeString(strings.TrimSpace(os.Getenv("STEWARD_ORCHESTRATION_SIGNING_KEY")))
		if err != nil || len(seed) != 32 {
			log.Fatal("STEWARD_ORCHESTRATION_SIGNING_KEY must contain a base64 Ed25519 seed")
		}
		fmt.Println(base64.StdEncoding.EncodeToString(steward.OrchestrationVerifyKeyFromSeed(seed)))
		return
	}
	if strings.TrimSpace(*agentID) == "" {
		log.Fatal("--agent or STEWARD_WORKER_AGENT_ID is required")
	}
	if strings.TrimSpace(*workerID) == "" {
		hostname, _ := os.Hostname()
		*workerID = fmt.Sprintf("%s-%d-%s", defaultString(hostname, "local"), os.Getpid(), uuid.NewString()[:8])
	}
	dsn := strings.TrimSpace(os.Getenv("DATABASE_URL"))
	if dsn == "" {
		log.Fatal("DATABASE_URL is required")
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	db, err := database.Connect(ctx, dsn)
	if err != nil {
		log.Fatalf("connect database: %v", err)
	}
	defer db.Close()
	service := steward.NewService(db,
		steward.WithRuntimeV2Enabled(true),
		steward.WithOrchestrationR4Enabled(true),
		steward.WithOrchestrationWorkersEnabled(true),
		steward.WithRuntimeWorkerID(*workerID),
	)
	if _, err := service.RegisterAgentWorker(ctx, *agentID, *workerID, os.Getpid()); err != nil {
		log.Fatalf("register Agent worker: %v", err)
	}
	defer func() {
		stopCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = service.StopAgentWorker(stopCtx, *workerID)
	}()
	log.Printf("steward Agent worker started agent=%s worker=%s", *agentID, *workerID)
	if *poll < 50*time.Millisecond {
		*poll = 50 * time.Millisecond
	}
	ticker := time.NewTicker(*poll)
	defer ticker.Stop()
	for {
		message, claimed, err := service.ClaimAgentMessage(ctx, *agentID, *workerID)
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			log.Printf("claim Agent message: %v", err)
		} else if claimed {
			if err := service.ExecuteAgentMessage(ctx, message, *workerID); err != nil && ctx.Err() == nil {
				log.Printf("execute Agent message %s: %v", message.ID, err)
			}
		} else if err := service.HeartbeatAgentWorker(ctx, *workerID); err != nil && ctx.Err() == nil {
			log.Printf("heartbeat Agent worker: %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

func durationEnv(name string, fallback time.Duration) time.Duration {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback
	}
	parsed, err := time.ParseDuration(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) == "" {
		return fallback
	}
	return strings.TrimSpace(value)
}
