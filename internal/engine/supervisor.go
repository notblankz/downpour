package engine

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"golang.org/x/sync/errgroup"
)

const pollInterval = 500 * time.Millisecond

// Snapshot is an immutable read-model of download progress, published by the
// Supervisor and read by observers via Supervisor.Snapshot
type Snapshot struct {
	Downloaded int64
	Total      int64
	Speed      float64
	ETA        time.Duration
}

// Supervisor orchestrates a single ranged, concurrent download: it probes the
// URL, plans the pieces, owns the worker pool and observes their progress
type Supervisor struct {
	cfg      Config
	logger   *slog.Logger
	file     *os.File
	progress atomic.Int64
	workers  []*Worker
	total    int64

	snap           atomic.Pointer[Snapshot]
	lastDownloaded int64
	lastAt         time.Time
	speed          float64
}

// NewSupervisor returns a new Supervisor
func NewSupervisor(cfg Config, logger *slog.Logger) *Supervisor {
	return &Supervisor{
		cfg:    cfg,
		logger: logger,
	}
}

// Snapshot returns the most recently published progress Snapshot struct
func (s *Supervisor) Snapshot() *Snapshot {
	return s.snap.Load()
}

// Run performs the whole download i.e. probe the URL, split into pieces,
// spawn the worker pool and wait for completion or the first fatal error
func (s *Supervisor) Run(ctx context.Context) error {
	start := time.Now()

	s.logger.Debug("probing", "url", s.cfg.URL)
	ps := time.Now()
	size, ranged, err := s.probe(ctx)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	s.logger.Debug("probe complete", "size", size, "ranged", ranged, "took", time.Since(ps).Round(time.Millisecond))

	if !ranged {
		s.logger.Info("ranged requests unsupported, streaming", "url", s.cfg.URL)
		return s.stream(ctx)
	}
	s.total = size

	f, err := os.Create(s.cfg.Output)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("preallocate: %w", err)
	}
	s.file = f

	pieces := planPieces(size, s.cfg.ChunkSize)
	queue := make(chan PieceInfo, len(pieces))
	for _, p := range pieces {
		queue <- p
	}
	close(queue)

	s.logger.Info("download started",
		"url", s.cfg.URL, "size", size, "pieces", len(pieces),
		"workers", s.cfg.Workers, "chunk_size", s.cfg.ChunkSize,
	)

	s.workers = make([]*Worker, s.cfg.Workers)
	for i := range s.workers {
		s.workers[i] = &Worker{
			ID:       i,
			Client:   newClient(),
			url:      s.cfg.URL,
			file:     s.file,
			progress: &s.progress,
			queue:    queue,
			logger:   s.logger.With("worker", i),
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, w := range s.workers {
		g.Go(func() error { return w.Run(gctx) })
	}

	go s.observe(gctx)

	if err := g.Wait(); err != nil {
		s.logger.Error("download failed", "url", s.cfg.URL, "err", err)
		return err
	}

	s.logger.Info("download complete", "bytes", s.progress.Load(), "duration", time.Since(start).Round(time.Millisecond))

	return nil
}

// observe polls the shared progress counter on a fixed interval, smooths the
// speed with an EWMA and publishes a Snapshot each tick until ctx is cancelled
func (s *Supervisor) observe(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()

	s.lastAt = time.Now()
	s.lastDownloaded = s.progress.Load()

	for {
		select {
		case <-ctx.Done():
			s.publish()
			return
		case now := <-t.C:
			downloaded := s.progress.Load()
			if dt := now.Sub(s.lastAt).Seconds(); dt > 0 {
				instant := float64(downloaded-s.lastDownloaded) / dt
				if s.speed == 0 {
					s.speed = instant
				} else {
					s.speed = 0.3*instant + 0.7*s.speed
				}
			}
			s.lastDownloaded = downloaded
			s.lastAt = now
			s.publish()
		}
	}
}

// publish computes ETA from the current speed and stores a fresh Snapshot
func (s *Supervisor) publish() {
	downloaded := s.progress.Load()
	var eta time.Duration
	if s.speed > 0 && s.total > downloaded {
		eta = time.Duration(float64(s.total-downloaded) / s.speed * float64(time.Second))
	}
	s.snap.Store(&Snapshot{
		Downloaded: downloaded,
		Total:      s.total,
		Speed:      s.speed,
		ETA:        eta,
	})
}

// probe sends a bytes=0-0 request to the server to find out the total file size
// and if the server supports ranged requests
func (s *Supervisor) probe(ctx context.Context) (size int64, ranged bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.URL, nil)
	if err != nil {
		return 0, false, err
	}
	req.Header.Set("Range", "bytes=0-0")

	resp, err := newClient().Do(req)
	if err != nil {
		return 0, false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusPartialContent {
		contentRange := resp.Header.Get("Content-Range")
		if i := strings.LastIndex(contentRange, "/"); i >= 0 {
			size, err = strconv.ParseInt(contentRange[i+1:], 10, 64)
			if err != nil {
				return 0, false, fmt.Errorf("parse content-range %q: %w", contentRange, err)
			}
		}

		if size == 0 {
			return 0, false, fmt.Errorf("no content-range size")
		}

		return size, true, nil
	}
	return resp.ContentLength, false, nil
}

// stream is the fallback for servers that do not support ranged requests
func (s *Supervisor) stream(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.URL, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("stream: unexpected status %d", resp.StatusCode)
	}

	f, err := os.Create(s.cfg.Output)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
