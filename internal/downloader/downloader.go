package downloader

import (
	"fmt"
	"io"
	"log"
	"math"
	"mime"
	"net/http"
	"net/http/httptrace"
	"net/url"
	"os"
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

const BufferSize = 64 * 1024

// struct to implement io.Writer for custom use of WriteAt() instead of Write() in io.Copy()
type OffsetWriter struct {
	file         *os.File
	offset       int64
	bytesWritten *atomic.Int64
}

func (ow *OffsetWriter) Write(p []byte) (int, error) {
	nwrite, err := ow.file.WriteAt(p, int64(ow.offset))
	if err != nil {
		return nwrite, fmt.Errorf("Could not write to file at offset %v - %v", ow.offset, err)
	}
	ow.offset += int64(nwrite)
	ow.bytesWritten.Add(int64(nwrite))
	return nwrite, nil
}

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

	_, err = copy(resp.Body, file, onProgress)
	if err != nil {
		onError(err)
		return
	}

	onDone()
	file.Close()
}

type RangeDownloadInfo struct {
	ChunkChan       chan int
	Client          *http.Client
	BufPool         *sync.Pool
	Wg              *sync.WaitGroup
	WorkerLimit     int
	TotalChunks     int64
	ChunkSize       int64
	TotalSize       int64
	BytesWritten    *atomic.Int64
	ReqURL          string
	Filename        string
	DirName         string
	File            *os.File
	EnableTrace     bool
	EnableTelemetry bool
	Checksum        *ChecksumInfo
}

func InitRangeDownloadInfo(filename string, totalSize int64, reqURl string, enableTrace bool, enableTelemetry bool) (*RangeDownloadInfo, error) {
	workerLimit := 12
	chunkSize := calculateChunkSize(totalSize, workerLimit)

	tr := http.Transport{
		MaxIdleConnsPerHost: workerLimit,
		MaxConnsPerHost:     0,
		DisableKeepAlives:   false,
		IdleConnTimeout:     90 * time.Second,
		ReadBufferSize:      BufferSize,
		WriteBufferSize:     BufferSize,
	}
	client := &http.Client{Transport: &tr}

	// create a new pool for the Workers
	pool := &sync.Pool{
		New: func() any {
			buf := make([]byte, 128*1024)
			return &buf
		},
	}

	var dirName string
	if enableTrace || enableTelemetry {
		dirName = filename[:(strings.LastIndex(filename, ".") + 1)]
		err := os.MkdirAll(dirName, os.ModePerm)
		if err != nil {
			return nil, err
		}
		filename = fmt.Sprintf("%s/%s", dirName, filename)
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

	// create a WaitGroup
	var wg sync.WaitGroup

	rdi := &RangeDownloadInfo{
		ChunkChan:       make(chan int),
		Client:          client,
		BufPool:         pool,
		Wg:              &wg,
		WorkerLimit:     workerLimit,
		ChunkSize:       chunkSize,
		TotalChunks:     int64(math.Ceil(float64(totalSize) / float64(chunkSize))),
		TotalSize:       totalSize,
		BytesWritten:    &atomic.Int64{},
		ReqURL:          reqURl,
		Filename:        filename,
		DirName:         dirName,
		File:            file,
		EnableTrace:     enableTrace,
		EnableTelemetry: enableTelemetry,
	}

	return rdi, nil
}

func (rdi *RangeDownloadInfo) RangeDownload(onDone DoneFunc, onVerify VerifyFunc, onError ErrorFunc) {
	if rdi.TotalSize == 0 || rdi.Filename == "" {
		onError(fmt.Errorf("Missing Information In the Provided Range Download Information"))
		return
	}

	// spawn downloader go routines and wait for completion
	rdi.Wg.Add(rdi.WorkerLimit)
	for i := 0; i < rdi.WorkerLimit; i++ {
		go rdi.rangeDownloadWorker(onError)
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

func (rdi *RangeDownloadInfo) rangeDownloadWorker(onError ErrorFunc) {
	defer rdi.Wg.Done()

	var logger *log.Logger
	var logFile *os.File

	if rdi.EnableTrace {
		logger, logFile = rdi.getTraceLogger()
		defer logFile.Close()
	}

	for chunkIndex := range rdi.ChunkChan {
		startPos := ((int64(chunkIndex)) * rdi.ChunkSize)
		endPos := int64(math.Min(float64(((int64(chunkIndex)+1)*rdi.ChunkSize)-1), float64(rdi.TotalSize-1)))

		req, err := http.NewRequest("GET", rdi.ReqURL, nil)
		if err != nil {
			onError(err)
			continue
		}
		req.Header.Add("Range", fmt.Sprintf("bytes=%v-%v", startPos, endPos))

		if rdi.EnableTrace {
			trace := &httptrace.ClientTrace{
				GotConn: func(connInfo httptrace.GotConnInfo) {
					if connInfo.Reused {
						logger.Printf("[Chunk %d] Connection Reused | IdleTime: %v", chunkIndex, connInfo.IdleTime)
					} else {
						logger.Printf("[Chunk %d] NEW Connection Dialed | Addr: %v", chunkIndex, connInfo.Conn.RemoteAddr())
					}
				},
				WroteRequest: func(info httptrace.WroteRequestInfo) {
					if info.Err != nil {
						logger.Printf("[Chunk %d] Write Error: %v", chunkIndex, info.Err)
					}
				},
			}

			req = req.WithContext(httptrace.WithClientTrace(req.Context(), trace))
		}

		// Chunk Retry
		const maxRetries = 5
		var resp *http.Response
		var doErr error
		var success bool

		for range maxRetries {
			resp, doErr = rdi.Client.Do(req)
			if doErr == nil && resp.StatusCode == http.StatusPartialContent {
				success = true
				break
			}

			if resp != nil {
				resp.Body.Close()
			}

			// implement exponential backoff
			time.Sleep(time.Second * 1)
		}

		if !success {
			onError(fmt.Errorf("FATAL: chunk %d failed after %d retries. Last error: %v", chunkIndex, maxRetries, doErr))
			continue
		}

		// write to file
		writer := &OffsetWriter{
			file:         rdi.File,
			offset:       startPos,
			bytesWritten: rdi.BytesWritten,
		}
		buf := rdi.BufPool.Get().(*[]byte)
		_, copyErr := io.CopyBuffer(writer, resp.Body, *buf)
		if copyErr != nil {
			resp.Body.Close()
			rdi.BufPool.Put(buf)
			onError(copyErr)
			continue
		}
		resp.Body.Close()
		rdi.BufPool.Put(buf)
	}
}

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

// <== Helper Functions ==>
// copy with progress callback
func copy(src io.Reader, dst io.Writer, onProgress ProgressFunc) (int64, error) {
	var totalWritten, chunkWritten int64
	buf := make([]byte, BufferSize)

	for {
		nread, rerr := src.Read(buf)
		if nread > 0 {
			chunkWritten = 0
			for chunkWritten < int64(nread) {
				nwrite, werr := dst.Write(buf[chunkWritten:nread])
				if werr != nil {
					return totalWritten, werr
				}
				chunkWritten += int64(nwrite)
			}
			totalWritten += chunkWritten
			onProgress(chunkWritten)
		}

		if rerr != nil {
			if rerr == io.EOF {
				return totalWritten, nil
			}
			return totalWritten, rerr
		}
	}
}

func calculateChunkSize(totalSize int64, workerLimit int) int64 {
	const minChunkSize = 1 * 1024 * 1024  // 1MB
	const maxChunkSize = 64 * 1024 * 1024 // 64MB
	const targetChunksPerWorker = 4

	if totalSize <= 0 {
		return maxChunkSize
	}

	chunkSize := totalSize / (int64(workerLimit) * targetChunksPerWorker)

	if chunkSize < minChunkSize {
		return minChunkSize
	}
	if chunkSize > maxChunkSize {
		return maxChunkSize
	}
	return chunkSize
}

func (rdi *RangeDownloadInfo) getTraceLogger() (*log.Logger, *os.File) {
	f, err := os.OpenFile(fmt.Sprintf("%s/httptrace.log", rdi.DirName), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	return log.New(f, "", log.LstdFlags|log.Lmicroseconds), f
}
