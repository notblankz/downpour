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

// Downloader coordinates a single ranged, concurrent download
type Downloader struct {
	cfg      Config
	logger   *slog.Logger
	file     *os.File
	progress atomic.Int64
	workers  []*Worker
}

// NewDownloader returns a Downloader configured by cfg
func NewDownloader(cfg Config, logger *slog.Logger) *Downloader {
	return &Downloader{
		cfg:    cfg,
		logger: logger,
	}
}

// Run performs the whole download i.e. probe the URL, split into pieces,
// spawn the worker pool and wait for completion or the first fatal error
func (d *Downloader) Run(ctx context.Context) error {
	start := time.Now()

	d.logger.Debug("probing", "url", d.cfg.URL)
	ps := time.Now()
	size, ranged, err := d.probe(ctx)
	if err != nil {
		return fmt.Errorf("probe: %w", err)
	}
	d.logger.Debug("probe complete", "size", size, "ranged", ranged, "took", time.Since(ps).Round(time.Millisecond))

	if !ranged {
		d.logger.Info("ranged requests unsupported, streaming", "url", d.cfg.URL)
		return d.stream(ctx)
	}

	f, err := os.Create(d.cfg.Output)
	if err != nil {
		return fmt.Errorf("create output file: %w", err)
	}
	defer f.Close()

	if err := f.Truncate(size); err != nil {
		return fmt.Errorf("preallocate: %w", err)
	}
	d.file = f

	pieces := planPieces(size, d.cfg.ChunkSize)
	queue := make(chan PieceInfo, len(pieces))
	for _, p := range pieces {
		queue <- p
	}
	close(queue)

	d.logger.Info("download started",
		"url", d.cfg.URL, "size", size, "pieces", len(pieces),
		"workers", d.cfg.Workers, "chunk_size", d.cfg.ChunkSize,
	)

	d.workers = make([]*Worker, d.cfg.Workers)
	for i := range d.workers {
		d.workers[i] = &Worker{
			ID:       i,
			Client:   newClient(),
			url:      d.cfg.URL,
			file:     d.file,
			progress: &d.progress,
			queue:    queue,
			logger:   d.logger.With("worker", i),
		}
	}

	g, gctx := errgroup.WithContext(ctx)
	for _, w := range d.workers {
		g.Go(func() error { return w.Run(gctx) })
	}

	if err := g.Wait(); err != nil {
		d.logger.Error("download failed", "url", d.cfg.URL, "err", err)
		return err
	}

	d.logger.Info("download complete", "bytes", d.progress.Load(), "duration", time.Since(start).Round(time.Millisecond))

	return nil
}

// probe sends a bytes=0-0 request to the server to find out the total file size
// and if the server supports ranged requests
func (d *Downloader) probe(ctx context.Context) (size int64, ranged bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.cfg.URL, nil)
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
func (d *Downloader) stream(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, d.cfg.URL, nil)
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

	f, err := os.Create(d.cfg.Output)
	if err != nil {
		return err
	}
	defer f.Close()

	_, err = io.Copy(f, resp.Body)
	return err
}
