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

// WorkerSnapshot is a per-worker line in the read model
type WorkerSnapshot struct {
	ID    int
	Speed float64
}

// Snapshot is an immutable read-model of download progress, published by the
// Supervisor and read by observers via Supervisor.Snapshot
type Snapshot struct {
	Downloaded int64
	Total      int64
	Speed      float64
	ETA        time.Duration
	Workers    []WorkerSnapshot
}

// workerStat is the supervisor's derived view of one worker sampled each tick
type workerStat struct {
	lastDownloaded int64
	lastChangeAt   time.Time
	speed          float64
}

// Supervisor orchestrates a single ranged, concurrent download: it probes the
// URL, plans the pieces, owns the worker pool and observes their progress
type Supervisor struct {
	cfg         Config
	logger      *slog.Logger
	file        *os.File
	workerStats []workerStat
	workers     []*Worker
	total       int64

	snap           atomic.Pointer[Snapshot]
	lastDownloaded int64
	lastAt         time.Time
	speed          float64
}

// NewSupervisor returns a new Supervisor and seeds an initial zero snapshot
// so that Snapshot() is never nil
func NewSupervisor(cfg Config, logger *slog.Logger) *Supervisor {
	s := &Supervisor{
		cfg:    cfg,
		logger: logger,
	}
	s.publish()
	return s
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
	s.workerStats = make([]workerStat, s.cfg.Workers)
	now := time.Now()
	for i := range s.workers {
		s.workers[i] = &Worker{
			ID:     i,
			Client: newClient(),
			url:    s.cfg.URL,
			file:   s.file,
			queue:  queue,
			logger: s.logger.With("worker", i),
		}
		s.workerStats[i].lastChangeAt = now
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

	s.logger.Info("download complete", "bytes", s.total, "duration", time.Since(start).Round(time.Millisecond))

	return nil
}

// observe ticks on the poll interval, calling publish each tick and once more
// when ctx is cancelled
func (s *Supervisor) observe(ctx context.Context) {
	t := time.NewTicker(pollInterval)
	defer t.Stop()

	for {
		select {
		case <-ctx.Done():
			s.publish()
			return
		case <-t.C:
			s.publish()
		}
	}
}

// publish computes ETA from the current speed and stores a fresh Snapshot
func (s *Supervisor) publish() {
	now := time.Now()
	dt := now.Sub(s.lastAt).Seconds()
	downloaded, workerSnapshots := s.sampleWorkers(now, dt)
	s.updateSpeed(downloaded, dt)
	s.lastAt = now
	s.snap.Store(&Snapshot{
		Downloaded: downloaded,
		Total:      s.total,
		Speed:      s.speed,
		ETA:        s.getEta(downloaded),
		Workers:    workerSnapshots,
	})
}

// ewma folds an instantaneous sample into a running average
func ewma(prev, instant float64) float64 {
	if prev == 0 {
		return instant
	}
	return 0.3*instant + 0.7*prev
}

// sampleWorkers reads each worker's counter, advances its per-worker speed, stamps
// lastChangeAt on progress, and returns the aggregated total bytes downloaded plus
// a fresh per-worker snapshot
func (s *Supervisor) sampleWorkers(now time.Time, dt float64) (int64, []WorkerSnapshot) {
	var total int64
	workerSnapshots := make([]WorkerSnapshot, len(s.workers))
	for i := range s.workers {
		cur := s.workers[i].downloaded.Load()
		total += cur
		workerStat := &s.workerStats[i]
		if dt > 0 {
			workerStat.speed = ewma(workerStat.speed, float64(cur-workerStat.lastDownloaded)/dt)
		}
		if cur != workerStat.lastDownloaded {
			workerStat.lastChangeAt = now
		}
		workerStat.lastDownloaded = cur
		workerSnapshots[i] = WorkerSnapshot{
			ID:    i,
			Speed: workerStat.speed,
		}
	}
	return total, workerSnapshots
}

// updateSpeed advances the EWMA throughput sampler from the last sample
func (s *Supervisor) updateSpeed(downloaded int64, dt float64) {
	if dt <= 0 {
		return
	}
	s.speed = ewma(s.speed, float64(downloaded-s.lastDownloaded)/dt)
	s.lastDownloaded = downloaded
}

// eta estimates remaining time from the current smoothed speed
func (s *Supervisor) getEta(downloaded int64) time.Duration {
	if s.speed <= 0 || s.total <= downloaded {
		return 0
	}
	return time.Duration(float64(s.total-downloaded) / s.speed * float64(time.Second))
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
