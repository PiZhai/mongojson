package jobs

import (
	"context"
	"log"
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
				if err := c.service.Expire(ctx); err != nil {
					log.Printf("expire old tool files and jobs: %v", err)
				}
			}
		}
	}()
}

func (c *CleanupLoop) Stop() {
	if c.cancel != nil {
		c.cancel()
	}
}
