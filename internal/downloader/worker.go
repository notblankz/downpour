package downloader

import (
	"crypto/tls"
	"fmt"
	"net/http"
	"time"
)

type ChunkInfo struct {
	Index           int64
	Size            int64
	BytesDownloaded int64
}

type WorkerStatus string

const (
	WorkerStatusIdle        WorkerStatus = "idle"
	WorkerStatusRequesting  WorkerStatus = "requesting"
	WorkerStatusDownloading WorkerStatus = "downloading"
	WorkerStatusRetrying    WorkerStatus = "retrying"
)

type WorkerInfo struct {
	ID                int
	Chunk             ChunkInfo
	Speed             float64
	TotalBytesWritten int64
	LastBytes         int64
	LastSample        time.Time
	Status            WorkerStatus
	StartedAt         time.Time
	RestartedAt       time.Time
	RestartWorkerChan chan struct{}
	HttpClient        *http.Client
}

func (info *WorkerInfo) UpdateSpeed() float64 {
	if info.LastSample.IsZero() {
		info.LastBytes = info.TotalBytesWritten
		info.LastSample = time.Now()
		return 0
	}
	deltaBytes := info.TotalBytesWritten - info.LastBytes
	info.LastBytes = info.TotalBytesWritten
	deltaTime := time.Since(info.LastSample).Seconds()
	info.LastSample = time.Now()
	curSpeed := float64(deltaBytes) / deltaTime
	info.Speed = (0.7 * info.Speed) + (0.3 * curSpeed)
	return curSpeed
}

func newWorkerClient() *http.Client {
	tr := http.Transport{
		MaxIdleConnsPerHost: workerLimit,
		MaxConnsPerHost:     0,
		DisableKeepAlives:   false,
		IdleConnTimeout:     10 * time.Second,
		ForceAttemptHTTP2:   false,
		TLSNextProto:        make(map[string]func(authority string, c *tls.Conn) http.RoundTripper),
		ReadBufferSize:      bufferSize,
		WriteBufferSize:     bufferSize,
	}
	client := &http.Client{
		Transport: &tr,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("Too many redirects")
			}
			// A small safeguard to make sure the correct range headers are copied over through the redirections
			req.Header.Set("Range", via[0].Header.Get("Range"))
			return nil
		},
	}
	return client
}
