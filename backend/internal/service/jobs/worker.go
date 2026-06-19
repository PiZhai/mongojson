package jobs

import (
	"context"
	"sync"
	"sync/atomic"

	"mongojson/backend/internal/config"
)

type Worker struct {
	service *Service
	config  config.Config
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	running atomic.Bool
}

func NewWorker(service *Service, cfg config.Config) *Worker {
	return &Worker{service: service, config: cfg}
}

func (w *Worker) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	w.cancel = cancel
	w.running.Store(true)
	w.wg.Add(1)
	go func() {
		defer func() {
			w.running.Store(false)
			w.wg.Done()
		}()
		for {
			select {
			case <-ctx.Done():
				return
			case id := <-w.service.Queue():
				w.service.Process(ctx, w.config, id)
			}
		}
	}()
}

func (w *Worker) IsRunning() bool {
	return w != nil && w.running.Load()
}

func (w *Worker) Stop() {
	if w.cancel != nil {
		w.cancel()
	}
	w.wg.Wait()
}
