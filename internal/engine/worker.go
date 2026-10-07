package engine

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync/atomic"
	"time"
)

// Worker download pieces off the shared queue using its own HTTP client
type Worker struct {
	ID       int
	Client   *http.Client
	url      string
	file     *os.File
	progress *atomic.Int64
	queue    <-chan PieceInfo // receive-only: a worker can't send or close it
}

// newClient returns a new http.Client which is then attached to a worker
func newClient() *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			MaxIdleConnsPerHost: 1,
			DisableKeepAlives:   false,
			IdleConnTimeout:     10 * time.Second,
			ForceAttemptHTTP2:   false,
			TLSNextProto:        map[string]func(authority string, c *tls.Conn) http.RoundTripper{},
			ReadBufferSize:      64 << 10,
			WriteBufferSize:     64 << 10,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			if len(via) > 0 {
				req.Header.Set("Range", via[0].Header.Get("Range"))
			}
			return nil
		},
	}
}

// Run drains the queue until it's closed, downloading each piece
func (w *Worker) Run(ctx context.Context) error {
	for p := range w.queue {
		if err := w.Download(ctx, p); err != nil {
			return err
		}
	}
	return nil
}

// Download fetches one piece, retrying failures with backoff
// until it succeeds or the retry budget is expired
func (w *Worker) Download(ctx context.Context, p PieceInfo) error {
	const maxRetries = 5
	for att := 0; ; att++ {
		retry, wait, err := w.attempt(ctx, p)
		if err == nil {
			return nil
		}
		if !retry || att >= maxRetries-1 {
			fmt.Printf("%s [W%2d] piece %3d GAVE UP after %d attempts: %v\n",
				time.Now().Format("15:04:05.000"), w.ID, p.Index, att+1, err)
			return fmt.Errorf("piece %d: %w", p.Index, err)
		}
		if wait == 0 {
			wait = expBackoff(att)
		}

		fmt.Printf("%s [W%2d] piece %3d retry att=%d, backing off %v\n",
			time.Now().Format("15:04:05.000"), w.ID, p.Index, att, wait)

		if err := sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// attempt performs a single ranged request for the piece
func (w *Worker) attempt(ctx context.Context, p PieceInfo) (retry bool, wait time.Duration, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, w.url, nil)
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", p.Start, p.End-1))

	resp, err := w.Client.Do(req)
	if err != nil {
		return true, 0, err
	}
	defer resp.Body.Close()

	fmt.Printf("%s [W%2d] piece %3d → %d (retry-after=%q)\n",
		time.Now().Format("15:04:05.000"), w.ID, p.Index, resp.StatusCode, resp.Header.Get("Retry-After"))

	if resp.StatusCode != http.StatusPartialContent {
		io.Copy(io.Discard, io.LimitReader(resp.Body, 16<<10))
		err := fmt.Errorf("status %d %s", resp.StatusCode, http.StatusText(resp.StatusCode))
		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode >= 500 {
			// use server responded "Retry-After" if set
			return true, parseRetryAfter(resp.Header.Get("Retry-After")), err
		}
		return false, 0, err
	}

	pw := NewPieceWriter(w.file, p.Start, w.progress)
	bp := GetBuffer()
	defer PutBuffer(bp)

	copied, err := io.CopyBuffer(pw, resp.Body, *bp)
	if err != nil {
		return true, 0, err
	}

	if want := p.End - p.Start; copied != want {
		return true, 0, fmt.Errorf("short read: got %d want %d", copied, want)
	}

	return false, 0, nil
}

// parseRetryAfter reads a Retry-After header (delay in seeconds); returns 0 if absent or invalid
func parseRetryAfter(v string) time.Duration {
	if secs, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && secs > 0 {
		return time.Duration(secs) * time.Second
	}
	return 0
}

// expBackoff returns an exponentially growing, jittered delay (capped at 10s) for attempt att
func expBackoff(att int) time.Duration {
	const base, maxDelay = 500 * time.Millisecond, 10 * time.Second
	d := min(base<<att, maxDelay)
	return d/2 + time.Duration(rand.Int64N(int64(d/2)))
}

// sleep blocks for d or until ctx is cancelled, whichever comes first
func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}
