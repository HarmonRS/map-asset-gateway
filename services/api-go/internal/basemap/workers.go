package basemap

import (
	"context"
	"log"
	"time"
)

const (
	// pushRetryInterval is how often due retries are looked for.
	pushRetryInterval = 15 * time.Second
	// tileJobInterval is how often the queue is checked for new jobs.
	tileJobInterval = 2 * time.Second
)

// StartBackgroundWorkers launches the periodic maintenance loops: resource
// health inspection, push retries and the tile slicing queue. Close stops them.
// Calling it more than once has no additional effect.
func (s *Store) StartBackgroundWorkers() {
	s.bgOnce.Do(func() {
		ctx, cancel := context.WithCancel(context.Background())
		s.bgCtx = ctx
		s.bgCancel = cancel

		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.healthLoop(ctx)
		}()

		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.pushRetryLoop(ctx)
		}()

		s.bgWG.Add(1)
		go func() {
			defer s.bgWG.Done()
			s.tileJobLoop(ctx)
		}()
	})
}

func (s *Store) healthLoop(ctx context.Context) {
	// Prime the catalog immediately so a resource that vanished since the last
	// scan shows up without waiting a full interval.
	if _, err := s.CheckHealth(ctx); err != nil && ctx.Err() == nil {
		log.Printf("health check failed: %v", err)
	}
	interval := s.cfg.HealthCheckInterval
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if _, err := s.CheckHealth(ctx); err != nil && ctx.Err() == nil {
				log.Printf("health check failed: %v", err)
			}
		}
	}
}

func (s *Store) pushRetryLoop(ctx context.Context) {
	ticker := time.NewTicker(pushRetryInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.dispatchPushRetries(ctx); err != nil && ctx.Err() == nil {
				log.Printf("push retry dispatch failed: %v", err)
			}
			if err := s.dispatchTileJobCallbacks(ctx); err != nil && ctx.Err() == nil {
				log.Printf("tile job callback dispatch failed: %v", err)
			}
		}
	}
}

func (s *Store) tileJobLoop(ctx context.Context) {
	ticker := time.NewTicker(tileJobInterval)
	defer ticker.Stop()
	for {
		s.dispatchTileJobs(ctx)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}
