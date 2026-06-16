package jobs

import (
	"context"
	"time"
)

type CleanupLoop struct {
	service *Service
	period  time.Duration
	cancel  context.CancelFunc
}

func NewCleanupLoop(service *Service, period time.Duration) *CleanupLoop {
	return &CleanupLoop{service: service, period: period}
}

func (c *CleanupLoop) Start(parent context.Context) {
	ctx, cancel := context.WithCancel(parent)
	c.cancel = cancel

	go func() {
		ticker := time.NewTicker(c.period)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				_ = c.service.Expire(ctx)
			}
		}
	}()
}

func (c *CleanupLoop) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}
