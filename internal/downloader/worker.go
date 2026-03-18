package downloader

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptrace"
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
	HttpClient        *http.Client
}

type PickMode uint8

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

func (workerInfo *WorkerInfo) downloadChunk(currentTask *ChunkTask, rdi *RangeDownloadInfo, mode ChunkDownloadMode) error {
	workerInfo.Status = WorkerStatusIdle

	// Bind this worker to the current task
	workerInfo.CurTask = currentTask
	unbindTaskFromWorker := func() {
		workerInfo.CurTask = nil
	}
	defer unbindTaskFromWorker()

	const maxRetries = 5
	const maxHedgeReserveWindow = int64(512 * 1024)
	var resp *http.Response
	var doErr error
	var success bool

	var rangeStart, rangeEnd int64

	switch mode {
	case NormalMode:
		rangeStart = currentTask.Start
		rangeEnd = currentTask.End
	case HedgeMode:
		reservedStart, reservedEnd, ok := currentTask.reserveRange(maxHedgeReserveWindow)
		rangeStart = reservedStart
		rangeEnd = reservedEnd
		if !ok || reservedEnd <= reservedStart {
			return nil
		}
	}

	for range maxRetries {
		workerInfo.Status = WorkerStatusRequesting
		req, err := http.NewRequestWithContext(currentTask.Ctx, "GET", rdi.ReqURL, nil)
		if err != nil {
			return err
		}
		req.Header.Add("Range", fmt.Sprintf("bytes=%d-%d", rangeStart, rangeEnd-1))

		if rdi.StatusFlags.EnableTrace {
			trace := &httptrace.ClientTrace{
				GotConn: func(connInfo httptrace.GotConnInfo) {
					if connInfo.Reused {
						rdi.Logger.HttpTrace.Printf("[Worker %2d::Chunk %4d] Connection Reused | Hedged Chunk: %v | IdleTime: %v", workerInfo.ID, currentTask.Index, currentTask.Hedged.Load(), connInfo.IdleTime)
					} else {
						rdi.Logger.HttpTrace.Printf("[Worker %2d::Chunk %4d] NEW Connection Dialed | Hedged Chunk: %v | Addr: %v", workerInfo.ID, currentTask.Index, currentTask.Hedged.Load(), connInfo.Conn.RemoteAddr())
					}
				},
				WroteRequest: func(info httptrace.WroteRequestInfo) {
					if info.Err != nil {
						rdi.Logger.HttpTrace.Printf("[Worker %d::Chunk %d] Hedged Chunk: %v | Write Error: %v", workerInfo.ID, currentTask.Index, currentTask.Hedged.Load(), info.Err)
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
		if errors.Is(doErr, context.Canceled) {
			if rdi.StatusFlags.EnableTrace {
				rdi.Logger.Hedging.Printf("[Worker %2d::Chunk %4d] CANCELLED | Chunk was hedged by another worker", workerInfo.ID, currentTask.Index)
			}
			return nil
		}
		return fmt.Errorf("FATAL: worker %d failed on chunk %d after %d retries. Last error: %v", workerInfo.ID, currentTask.Index, maxRetries, doErr)
	}

	// write to file
	workerInfo.Status = WorkerStatusDownloading

	cw := rdi.WriterPool.Get().(*chunkWriter)
	cw.worker = workerInfo
	cw.curTask = currentTask
	cw.reservedStart = rangeStart
	cw.reservedEnd = rangeEnd
	cw.writeCursor = 0

	_, copyErr := io.CopyBuffer(cw, resp.Body, cw.buf)
	resp.Body.Close()
	rdi.WriterPool.Put(cw)

	if copyErr == nil {
		bytesWritten := cw.writeCursor

		currentTask.CommittedBytes.Add(bytesWritten)

		chunkSize := currentTask.End - currentTask.Start
		if currentTask.CommittedBytes.Load() >= chunkSize {
			if currentTask.Done.CompareAndSwap(false, true) {
				currentTask.Cancel()
			}
		}
	} else {
		if errors.Is(copyErr, context.Canceled) {
			return nil
		}
	}

	workerInfo.Status = WorkerStatusIdle

	return nil
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
