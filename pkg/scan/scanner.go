// Package scan collects a point-in-time Snapshot of Docker disk usage in
// parallel from the daemon, with bounded log-file stat calls.
package scan

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"golang.org/x/sync/errgroup"

	"github.com/tyutyutyu/dodu/pkg/docker"
)

// Scanner collects a Snapshot from a Docker daemon in parallel.
type Scanner struct {
	client docker.Client
	logger *slog.Logger

	// LogConcurrency caps how many log file stat() calls run in parallel.
	// Defaults to 16.
	LogConcurrency int

	// LogStatTimeout bounds each LogFileSize call. Defaults to 1 second.
	LogStatTimeout time.Duration
}

// New constructs a Scanner.
func New(client docker.Client, logger *slog.Logger) *Scanner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Scanner{
		client:         client,
		logger:         logger,
		LogConcurrency: 16,
		LogStatTimeout: time.Second,
	}
}

// Scan collects images, containers, volumes, build cache, and per-container
// log file sizes in parallel. Partial collector failures are recorded in
// Snapshot.Errors but do not abort the scan; only ctx cancellation or a Ping
// failure returns an error.
func (s *Scanner) Scan(ctx context.Context) (*Snapshot, error) {
	start := time.Now()
	snap := &Snapshot{
		LogSizes:   map[string]int64{},
		CapturedAt: start,
	}

	info, err := s.client.Ping(ctx)
	if err != nil {
		return nil, fmt.Errorf("ping docker daemon: %w", err)
	}
	snap.Daemon = info
	s.logger.Debug("scan: ping ok", "daemon_id", info.ID, "version", info.ServerVersion)

	var (
		mu     sync.Mutex
		errsMu sync.Mutex
	)
	addErr := func(stage string, err error) {
		errsMu.Lock()
		defer errsMu.Unlock()
		snap.Errors = append(snap.Errors, fmt.Errorf("%s: %w", stage, err))
		s.logger.Warn("scan: partial failure", "stage", stage, "err", err)
	}

	g, gctx := errgroup.WithContext(ctx)

	containersDone := make(chan struct{})
	usage, usageErr := s.client.DiskUsage(ctx)
	if usageErr == nil {
		snap.Images, snap.Containers = usage.Images, usage.Containers
		snap.Volumes, snap.BuildCache = usage.Volumes, usage.BuildCache
		snap.LayersSize, snap.LayersSizeKnown = usage.LayersSize, usage.LayersSize >= 0
		close(containersDone)
	} else {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		addErr("disk_usage", usageErr)
		g.Go(func() error {
			t := time.Now()
			images, err := s.client.ListImages(gctx)
			if err != nil {
				addErr("images", err)
				return nil
			}
			mu.Lock()
			snap.Images = images
			mu.Unlock()
			s.logger.Debug("scan: images", "count", len(images), "took", time.Since(t))
			return nil
		})

		g.Go(func() error {
			defer close(containersDone)
			t := time.Now()
			containers, err := s.client.ListContainers(gctx, true)
			if err != nil {
				addErr("containers", err)
				return nil
			}
			mu.Lock()
			snap.Containers = containers
			mu.Unlock()
			s.logger.Debug("scan: containers", "count", len(containers), "took", time.Since(t))
			return nil
		})

		g.Go(func() error {
			t := time.Now()
			volumes, err := s.client.ListVolumes(gctx)
			if err != nil {
				addErr("volumes", err)
				return nil
			}
			mu.Lock()
			snap.Volumes = volumes
			mu.Unlock()
			s.logger.Debug("scan: volumes", "count", len(volumes), "took", time.Since(t))
			return nil
		})

		g.Go(func() error {
			t := time.Now()
			bc, err := s.client.BuildCacheUsage(gctx)
			if err != nil {
				addErr("build_cache", err)
				return nil
			}
			mu.Lock()
			snap.BuildCache = bc
			mu.Unlock()
			s.logger.Debug("scan: build_cache", "count", len(bc), "took", time.Since(t))
			return nil
		})

	}
	// Log sizes depend on the container list — run after that goroutine.
	g.Go(func() error {
		select {
		case <-containersDone:
		case <-gctx.Done():
			return gctx.Err()
		}
		mu.Lock()
		ids := make([]string, 0, len(snap.Containers))
		for _, c := range snap.Containers {
			ids = append(ids, c.ID)
		}
		mu.Unlock()

		s.collectLogSizes(gctx, ids, snap, addErr)
		return nil
	})

	if err := g.Wait(); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	snap.Duration = time.Since(start)
	s.logger.Info("scan: done",
		"duration", snap.Duration,
		"images", len(snap.Images),
		"containers", len(snap.Containers),
		"volumes", len(snap.Volumes),
		"build_cache", len(snap.BuildCache),
		"errors", len(snap.Errors),
	)
	return snap, nil
}

func (s *Scanner) collectLogSizes(ctx context.Context, ids []string, snap *Snapshot, addErr func(string, error)) {
	if len(ids) == 0 {
		return
	}
	conc := s.LogConcurrency
	if conc <= 0 {
		conc = 16
	}
	sem := make(chan struct{}, conc)
	var wg sync.WaitGroup
	defer wg.Wait()
	var mu sync.Mutex

	for _, id := range ids {
		select {
		case <-ctx.Done():
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			defer func() { <-sem }()

			callCtx := ctx
			if s.LogStatTimeout > 0 {
				var cancel context.CancelFunc
				callCtx, cancel = context.WithTimeout(ctx, s.LogStatTimeout)
				defer cancel()
			}
			size, err := s.client.LogFileSize(callCtx, id)
			if err != nil {
				addErr("log_size:"+id, err)
				return
			}
			if size > 0 {
				mu.Lock()
				snap.LogSizes[id] = size
				mu.Unlock()
			}
		}(id)
	}
}
