package downloader

import (
	"fmt"
	"io"
	"math"
	"mime"
	"net/http"
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
	ChunkChan    chan int
	WorkerLimit  int
	TotalChunks  int64
	ChunkSize    int64
	TotalSize    int64
	BytesWritten *atomic.Int64
	ReqURL       string
	Filename     string
	File         *os.File
}

func InitRangeDownloadInfo(filename string, totalSize int64, reqURl string) *RangeDownloadInfo {
	workerLimit := 12
	chunkSize := calculateChunkSize(totalSize, workerLimit)
	return &RangeDownloadInfo{
		ChunkChan:    make(chan int),
		WorkerLimit:  workerLimit,
		ChunkSize:    chunkSize,
		TotalChunks:  int64(math.Ceil(float64(totalSize) / float64(chunkSize))),
		TotalSize:    totalSize,
		BytesWritten: &atomic.Int64{},
		ReqURL:       reqURl,
		Filename:     filename,
	}
}

func (rdi *RangeDownloadInfo) RangeDownload(onDone DoneFunc, onError ErrorFunc) {
	if rdi.TotalSize == 0 || rdi.Filename == "" {
		onError(fmt.Errorf("Missing Information In the Provided Range Download Information"))
	}

	var wg sync.WaitGroup
	rdi.TotalChunks = int64(math.Ceil(float64(rdi.TotalSize) / float64(rdi.ChunkSize)))

	// pre-allocate file with TotalSize
	file, err := os.Create(rdi.Filename)
	if err != nil {
		onError(err)
		return
	}
	rdi.File = file
	defer file.Close()

	err = rdi.File.Truncate(int64(rdi.TotalSize))
	if err != nil {
		onError(err)
		return
	}

	tr := http.Transport{
		MaxIdleConnsPerHost: rdi.WorkerLimit,
		MaxConnsPerHost:     0,
		DisableKeepAlives:   false,
		IdleConnTimeout:     90 * time.Second,
		ReadBufferSize:      BufferSize,
		WriteBufferSize:     BufferSize,
	}
	client := &http.Client{Transport: &tr}

	// spawn go routines and wait for completion
	wg.Add(rdi.WorkerLimit)
	for i := 0; i < rdi.WorkerLimit; i++ {
		go rdi.rangeDownloadWorker(&wg, client, onError)
	}
	go func() {
		for chunkIndex := 0; int64(chunkIndex) < rdi.TotalChunks; chunkIndex++ {
			rdi.ChunkChan <- chunkIndex
		}
		close(rdi.ChunkChan)
	}()
	wg.Wait()
	onDone()
}

func (rdi *RangeDownloadInfo) rangeDownloadWorker(wg *sync.WaitGroup, client *http.Client, onError ErrorFunc) {
	defer wg.Done()

	for chunkIndex := range rdi.ChunkChan {
		startPos := ((int64(chunkIndex)) * rdi.ChunkSize)
		endPos := int64(math.Min(float64(((int64(chunkIndex)+1)*rdi.ChunkSize)-1), float64(rdi.TotalSize-1)))

		req, err := http.NewRequest("GET", rdi.ReqURL, nil)
		if err != nil {
			onError(err)
			continue
		}
		req.Header.Add("Range", fmt.Sprintf("bytes=%v-%v", startPos, endPos))

		resp, err := client.Do(req)
		if err != nil {
			onError(err)
			continue
		}

		if resp.StatusCode == http.StatusPartialContent {
			// write to file
			writer := &OffsetWriter{
				file:         rdi.File,
				offset:       startPos,
				bytesWritten: rdi.BytesWritten,
			}
			_, err := io.Copy(writer, resp.Body)
			if err != nil {
				onError(err)
				resp.Body.Close()
				continue
			}
			resp.Body.Close()
		} else {
			onError(fmt.Errorf("Bad Status Code Received - %v", resp.StatusCode))
			resp.Body.Close()
			continue
		}
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
