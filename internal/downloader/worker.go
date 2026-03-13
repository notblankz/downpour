package downloader

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math"
	"net/http"
	"net/http/httptrace"
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
	WorkerStatusDone        WorkerStatus = "done"
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

func (workerInfo *WorkerInfo) downloadChunk(chunkIndex int, rdi *RangeDownloadInfo, logger *log.Logger) error {
	workerInfo.Status = WorkerStatusIdle
	startPos := ((int64(chunkIndex)) * rdi.ChunkSize)
	endPos := int64(math.Min(float64(((int64(chunkIndex)+1)*rdi.ChunkSize)-1), float64(rdi.TotalSize-1)))

	// Add information to the WorkerInfo
	workerInfo.Chunk.Index = int64(chunkIndex)
	workerInfo.Chunk.Size = endPos - startPos
	workerInfo.Chunk.BytesDownloaded = 0

	const maxRetries = 5
	var resp *http.Response
	var doErr error
	var success bool

	for range maxRetries {
		workerInfo.Status = WorkerStatusRequesting
		req, err := http.NewRequest("GET", rdi.ReqURL, nil)
		if err != nil {
			return err
		}
		req.Header.Add("Range", fmt.Sprintf("bytes=%v-%v", startPos, endPos))

		if rdi.StatusFlags.EnableTrace {
			trace := &httptrace.ClientTrace{
				GotConn: func(connInfo httptrace.GotConnInfo) {
					if connInfo.Reused {
						logger.Printf("[Worker %2d::Chunk %4d] Connection Reused | IdleTime: %v", workerInfo.ID, workerInfo.Chunk.Index, connInfo.IdleTime)
					} else {
						logger.Printf("[Worker %2d::Chunk %4d] NEW Connection Dialed | Addr: %v", workerInfo.ID, workerInfo.Chunk.Index, connInfo.Conn.RemoteAddr())
					}
				},
				WroteRequest: func(info httptrace.WroteRequestInfo) {
					if info.Err != nil {
						logger.Printf("[Worker %d::Chunk %d] Write Error: %v", workerInfo.ID, workerInfo.Chunk.Index, info.Err)
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
		return fmt.Errorf("FATAL: worker %d failed on chunk %d after %d retries. Last error: %v", workerInfo.ID, workerInfo.Chunk.Index, maxRetries, doErr)
	}

	// write to file
	workerInfo.Status = WorkerStatusDownloading
	cw := rdi.WriterPool.Get().(*chunkWriter)
	cw.worker = workerInfo
	cw.offset = startPos
	_, copyErr := io.CopyBuffer(cw, resp.Body, cw.buf)
	if copyErr != nil {
		resp.Body.Close()
		rdi.WriterPool.Put(cw)
		return copyErr
	}
	resp.Body.Close()
	rdi.WriterPool.Put(cw)
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
