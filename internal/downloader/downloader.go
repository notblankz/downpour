package downloader

import (
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// callback types
type ProgressFunc func(n int64)
type DoneFunc func()
type ErrorFunc func(err error)
type VerifyFunc func()

const bufferSize = 64 * 1024         // 64KB
const minChunkSize = 256 * 1024      // 256KB
const maxChunkSize = 1 * 1024 * 1024 // 1MB
const workerLimit = 8

func StreamDownload(u url.URL, onProgress ProgressFunc, onDone DoneFunc, onError ErrorFunc) {
	req, err := http.NewRequest("GET", u.String(), nil)
	if err != nil {
		onError(err)
		return
	}
	req.Header.Set("User-Agent", "Mozilla/5.0")

	client := &http.Client{}
	resp, err := client.Do(req)
	if err != nil {
		onError(err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		onError(fmt.Errorf("bad status: %s", resp.Status))
		return
	}

	filename := GetFileName(&u, resp)

	file, err := os.Create(filename)
	if err != nil {
		onError(err)
		return
	}

	_, err = streamCopy(resp.Body, file, onProgress)
	if err != nil {
		onError(err)
		return
	}

	onDone()
	file.Close()
}

type StatusFlags struct {
	EnableTrace     bool
	EnableTelemetry bool
}

type Workers struct {
	Limit int
	Slice []*WorkerInfo
}
type RangeDownloadInfo struct {
	ChunkChan    chan int
	Client       *http.Client
	WriterPool   *sync.Pool
	Wg           *sync.WaitGroup
	Workers      Workers
	TotalChunks  int64
	ChunkSize    int64
	TotalSize    int64
	BytesWritten *atomic.Int64
	ReqURL       string
	Filename     string
	DirName      string
	File         *os.File
	StatusFlags  StatusFlags
	Checksum     *ChecksumInfo
}

func InitRangeDownloadInfo(filename string, totalSize int64, reqURl string, statusFlags StatusFlags) (*RangeDownloadInfo, error) {
	chunkSize := int64(maxChunkSize)
	if totalSize < minChunkSize {
		chunkSize = totalSize
	}

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

	var dirName string
	if statusFlags.EnableTrace || statusFlags.EnableTelemetry {
		dirName = filename[:(strings.LastIndex(filename, "."))]
		err := os.MkdirAll(dirName, os.ModePerm)
		if err != nil {
			return nil, err
		}
		filename = filepath.Join(dirName, filename)
	}

	// pre-allocate file with TotalSize
	file, err := os.Create(filename)
	if err != nil {
		return nil, err
	}

	err = file.Truncate(int64(totalSize))
	if err != nil {
		return nil, err
	}

	// create a WaitGroup and a Atomic Int64 Variable
	var wg sync.WaitGroup
	var bytesWritten atomic.Int64

	workerSlice := make([]*WorkerInfo, workerLimit)
	for i := range workerSlice {
		workerInfo := &WorkerInfo{
			ID:     i,
			Status: WorkerStatusIdle,
		}
		workerSlice[i] = workerInfo
	}
	workers := Workers{
		Limit: workerLimit,
		Slice: workerSlice,
	}

	// create a new pool for the Workers
	pool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 512*1024)
			return &chunkWriter{
				buf:                buf,
				file:               file,
				globalBytesWritten: &bytesWritten,
			}
		},
	}

	rdi := &RangeDownloadInfo{
		ChunkChan:    make(chan int),
		Client:       client,
		WriterPool:   pool,
		Wg:           &wg,
		Workers:      workers,
		ChunkSize:    chunkSize,
		TotalChunks:  int64(math.Ceil(float64(totalSize) / float64(chunkSize))),
		TotalSize:    totalSize,
		BytesWritten: &bytesWritten,
		ReqURL:       reqURl,
		Filename:     filename,
		DirName:      dirName,
		File:         file,
		StatusFlags:  statusFlags,
	}

	return rdi, nil
}

func (rdi *RangeDownloadInfo) RangeDownload(onDone DoneFunc, onVerify VerifyFunc, onError ErrorFunc) {
	if rdi.TotalSize == 0 || rdi.Filename == "" {
		onError(fmt.Errorf("Missing Information In the Provided Range Download Information"))
		return
	}

	// spawn downloader go routines and wait for completion
	rdi.Wg.Add(rdi.Workers.Limit)
	for i := 0; i < rdi.Workers.Limit; i++ {
		go rdi.rangeDownloadWorker(rdi.Workers.Slice[i], onError)
	}
	go func() {
		for chunkIndex := 0; int64(chunkIndex) < rdi.TotalChunks; chunkIndex++ {
			rdi.ChunkChan <- chunkIndex
		}
		close(rdi.ChunkChan)
	}()
	rdi.Wg.Wait()
	rdi.File.Close()

	if rdi.Checksum != nil {
		onVerify()
		err := VerifyFile(rdi.Filename, rdi.Checksum.ExpectedHash, rdi.Checksum.Algo)
		if err != nil {
			onError(err)
			return
		}
	}

	onDone()
}

func (rdi *RangeDownloadInfo) rangeDownloadWorker(workerInfo *WorkerInfo, onError ErrorFunc) {
	workerInfo.StartedAt = time.Now()
	defer rdi.Wg.Done()

	var logger *log.Logger
	var logFile *os.File

	if rdi.StatusFlags.EnableTrace {
		logger, logFile = rdi.getTraceLogger()
		defer logFile.Close()
	}

	for chunkIndex := range rdi.ChunkChan {
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
				onError(err)
				break
			}
			req.Header.Add("Range", fmt.Sprintf("bytes=%v-%v", startPos, endPos))

			if rdi.StatusFlags.EnableTrace {
				trace := &httptrace.ClientTrace{
					GotConn: func(connInfo httptrace.GotConnInfo) {
						if connInfo.Reused {
							logger.Printf("[Worker %d::Chunk %d] Connection Reused | IdleTime: %v", workerInfo.ID, workerInfo.Chunk.Index, connInfo.IdleTime)
						} else {
							logger.Printf("[Worker %d::Chunk %d] NEW Connection Dialed | Addr: %v", workerInfo.ID, workerInfo.Chunk.Index, connInfo.Conn.RemoteAddr())
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

			resp, doErr = rdi.Client.Do(req)
			if doErr == nil && resp.StatusCode == http.StatusPartialContent {
				success = true
				break
			}

			// close the response body if we received some other Reponse apart from StatusPartialContent
			if resp != nil {
				resp.Body.Close()
			}

			workerInfo.Status = WorkerStatusRetrying

			// implement exponential backoff
			time.Sleep(time.Second * 1)
		}

		if !success {
			onError(fmt.Errorf("FATAL: worker %d failed on chunk %d after %d retries. Last error: %v", workerInfo.ID, workerInfo.Chunk.Index, maxRetries, doErr))
			continue
		}

		// write to file
		workerInfo.Status = WorkerStatusDownloading
		cw := rdi.WriterPool.Get().(*chunkWriter)
		cw.worker = workerInfo
		cw.offset = startPos
		ncopy, copyErr := io.CopyBuffer(cw, resp.Body, cw.buf)
		if copyErr != nil {
			resp.Body.Close()
			rdi.WriterPool.Put(cw)
			onError(copyErr)
			continue
		}
		rdi.WriterPool.Put(cw)
		resp.Body.Close()
		workerInfo.TotalBytesWritten += ncopy
		workerInfo.Status = WorkerStatusIdle
	}
}

// <== Helper Functions ==>
func GetFileName(u *url.URL, resp *http.Response) string {
	contentDisposition := resp.Header.Get("Content-Disposition")

	if contentDisposition != "" {
		_, params, err := mime.ParseMediaType(contentDisposition)
		if err == nil {
			if fname, ok := params["filename"]; ok {
				return fname
			}
			if fname, ok := params["filename*"]; ok {
				return fname
			}
		}
	}
	pathSlice := strings.Split(u.Path, "/")
	return pathSlice[len(pathSlice)-1]
}
