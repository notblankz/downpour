package downloader

import (
	"context"
	"crypto/tls"
	"downpour/internal/utils"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"time"
)

type WorkerStatus string

const (
	WorkerStatusIdle        WorkerStatus = "idle"
	WorkerStatusRequesting  WorkerStatus = "requesting"
	WorkerStatusDownloading WorkerStatus = "downloading"
	WorkerStatusRetrying    WorkerStatus = "retrying"
	WorkerStatusDone        WorkerStatus = "done"
	WorkerStatusRestarting  WorkerStatus = "restarting"
)

type WorkerInfo struct {
	ID         int
	WorkerCtx  context.Context
	KillWorker context.CancelFunc
	// Chunk             *ChunkInfo
	CurTask           *ChunkTask
	Speed             float64
	TotalBytesWritten int64
	LastBytes         int64
	LastSample        time.Time
	Status            WorkerStatus
	StartedAt         time.Time
	RestartedAt       time.Time
	RestartWorkerChan chan struct{}
	Mirror            *MirrorInfo
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

func (workerInfo *WorkerInfo) downloadChunk(currentTask *ChunkTask, rdi *RangeDownloadInfo) error {
	startTime := time.Now()
	workerInfo.Status = WorkerStatusIdle

	// Bind this worker to the current task
	workerInfo.CurTask = currentTask
	unbindTaskFromWorker := func() {
		workerInfo.CurTask = nil
	}
	defer unbindTaskFromWorker()

	const maxRetries = 5
	var resp *http.Response
	var doErr error
	var success bool

	for range maxRetries {
		workerInfo.Status = WorkerStatusRequesting
		req, err := http.NewRequestWithContext(currentTask.Ctx, "GET", workerInfo.Mirror.URL, nil)
		if err != nil {
			return err
		}
		req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", currentTask.Start, currentTask.End-1))

		if rdi.StatusFlags.EnableTrace {
			trace := &httptrace.ClientTrace{
				GotConn: func(connInfo httptrace.GotConnInfo) {
					if !connInfo.Reused {
						addr := ""
						if connInfo.Conn != nil {
							addr = connInfo.Conn.RemoteAddr().String()
						}
						rdi.Logger.HttpTrace.Printf("[INFO] [Worker %02d::Chunk %04d] NEW CONN | addr=%s", workerInfo.ID, currentTask.Index, addr)
					}
				},
				WroteRequest: func(info httptrace.WroteRequestInfo) {
					if info.Err != nil {
						rdi.Logger.HttpTrace.Printf("[ERROR] [Worker %02d::Chunk %04d] WRITE ERROR | err=%v", workerInfo.ID, currentTask.Index, info.Err)
					}
				},
			}

			req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
		}

		resp, doErr = workerInfo.HttpClient.Do(req)
		if doErr == nil && resp.StatusCode == http.StatusPartialContent {
			success = true
			break
		}

		// close the response body if we received some other Reponse apart from StatusPartialContent
		if resp != nil {
			resp.Body.Close()
		}

		workerInfo.Status = WorkerStatusRetrying

		// TODO: implement exponential backoff
		time.Sleep(time.Second * 1)
	}

	if !success {
		host := mirrorHostname(workerInfo.Mirror.URL)
		rdi.Logger.Writes.Printf("[ERROR] [Worker %02d::Chunk %04d] CHUNK FAILED | mirror=%s | err=%v",
			workerInfo.ID,
			currentTask.Index,
			host,
			doErr,
		)
		if errors.Is(doErr, context.Canceled) {
			return nil
		}
		workerInfo.Mirror.Failures.Add(1)
		if workerInfo.Mirror.Failures.Load() >= 3 {
			if workerInfo.Mirror.Dead.CompareAndSwap(false, true) {
				rdi.Logger.Mirrors.Printf("[INFO] [Mirror] DEAD | url=%s | failures=%d", host, workerInfo.Mirror.Failures.Load())
			}
		}
		return fmt.Errorf("FATAL: worker %d failed on chunk %d after %d retries. Last error: %v", workerInfo.ID, currentTask.Index, maxRetries, doErr)
	}

	// write to file
	workerInfo.Status = WorkerStatusDownloading

	cw := rdi.WriterPool.Get().(*chunkWriter)
	cw.worker = workerInfo
	cw.curTask = currentTask

	_, copyErr := io.CopyBuffer(cw, resp.Body, cw.buf)
	resp.Body.Close()
	rdi.WriterPool.Put(cw)

	if copyErr != nil {
		if errors.Is(copyErr, context.Canceled) {
			return nil
		}
		return copyErr
	}

	workerInfo.Status = WorkerStatusIdle
	rdi.Logger.Writes.Printf("[INFO] [Worker %02d::Chunk %04d] CHUNK DONE | mirror=%s | bytes=%d | duration=%s",
		workerInfo.ID,
		currentTask.Index,
		mirrorHostname(workerInfo.Mirror.URL),
		currentTask.CommittedBytes.Load(),
		utils.FormatDuration(time.Since(startTime)),
	)

	return nil
}

func mirrorHostname(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return rawURL
	}
	host := parsed.Hostname()
	if host == "" {
		return rawURL
	}
	return host
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
