package downloader

import (
	"context"
	"downpour/internal/utils"
	"fmt"
	"math"
	"mime"
	"net/http"
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

const bufferSize = 64 * 1024          // 64KB
const minChunkSize = 256 * 1024       // 256KB
const maxChunkSize = 16 * 1024 * 1024 // 16MB
const workerLimit = 32

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
	NormalQueue         chan *ChunkTask
	WriterPool          *sync.Pool
	NormalWorkerWg      *sync.WaitGroup
	Workers             Workers
	TotalChunks         int64
	Chunks              []*ChunkTask
	ChunkSize           int64
	TotalSize           int64
	BytesWritten        *atomic.Int64
	ReqURL              string
	Filename            string
	DirName             string
	File                *os.File
	StatusFlags         StatusFlags
	Checksum            *ChecksumInfo
	WorkerBaselineSpeed float64
	Logger              *Logger
}

func InitRangeDownloadInfo(filename string, totalSize int64, reqURl string, statusFlags StatusFlags) (*RangeDownloadInfo, error) {
	chunkSize := int64(maxChunkSize)
	if totalSize < minChunkSize {
		chunkSize = totalSize
	}

	totalChunks := int64(math.Ceil(float64(totalSize) / float64(chunkSize)))

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
		worker := &WorkerInfo{
			ID: i,
			// Chunk:             &ChunkInfo{},
			Status:            WorkerStatusIdle,
			HttpClient:        newWorkerClient(),
			RestartWorkerChan: make(chan struct{}, 1),
		}
		workerSlice[i] = worker
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
		NormalQueue:         make(chan *ChunkTask),
		WriterPool:          pool,
		NormalWorkerWg:      &wg,
		Workers:             workers,
		ChunkSize:           chunkSize,
		TotalChunks:         totalChunks,
		Chunks:              make([]*ChunkTask, totalChunks),
		TotalSize:           totalSize,
		BytesWritten:        &bytesWritten,
		ReqURL:              reqURl,
		Filename:            filename,
		DirName:             dirName,
		File:                file,
		StatusFlags:         statusFlags,
		WorkerBaselineSpeed: 0,
	}

	return rdi, nil
}

func (rdi *RangeDownloadInfo) RangeDownload(onDone DoneFunc, onVerify VerifyFunc, onError ErrorFunc) {
	if rdi.TotalSize == 0 || rdi.Filename == "" {
		onError(fmt.Errorf("Missing Information In the Provided Range Download Information"))
		return
	}

	// Create all loggers and log files
	logger, logFiles, logErr := rdi.getLoggers()
	for _, logFile := range logFiles {
		defer logFile.Close()
	}
	if logErr != nil {
		onError(fmt.Errorf("Could not create and start all the required loggers"))
	}
	rdi.Logger = logger

	// spawn downloader go routines and wait for completion
	rdi.NormalWorkerWg.Add(rdi.Workers.Limit)
	for i := 0; i < rdi.Workers.Limit; i++ {
		go rdi.rangeDownloadWorker(rdi.Workers.Slice[i], onError)
	}
	go func() {
		for chunkIndex := int64(0); chunkIndex < rdi.TotalChunks; chunkIndex++ {
			startPos := chunkIndex * rdi.ChunkSize
			endPos := min(rdi.TotalSize, ((chunkIndex + 1) * rdi.ChunkSize))

			chunkCtx, cancelChunk := context.WithCancel(context.Background())

			ct := &ChunkTask{
				Index: chunkIndex,
				Start: startPos,
				End:   endPos,

				Ctx:    chunkCtx,
				Cancel: cancelChunk,
			}

			rdi.Chunks[chunkIndex] = ct

			rdi.NormalQueue <- ct
		}
		close(rdi.NormalQueue)
	}()
	rdi.NormalWorkerWg.Wait()
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

func (rdi *RangeDownloadInfo) rangeDownloadWorker(worker *WorkerInfo, onError ErrorFunc) {
	worker.StartedAt = time.Now()
	worker.RestartedAt = time.Now()

	ctx, cancelWorker := context.WithCancel(context.Background())
	worker.KillWorker = cancelWorker
	worker.WorkerCtx = ctx

	// gracefully exit worker once it completes downloading all the chunk tasks assigned to it
	defer func() {
		worker.Status = WorkerStatusDone
		rdi.NormalWorkerWg.Done()
	}()

	for {

		// check for a restart signal from health monitor
		select {
		case <-worker.RestartWorkerChan:
			worker.Status = WorkerStatusRestarting
			worker.HttpClient = newWorkerClient()
			if rdi.StatusFlags.EnableTrace {
				rdi.Logger.Restarts.Printf("[Worker %2d] RESTARTED | Worker Speed was: %s | Workers Baseline Speed: %s",
					worker.ID,
					utils.FormatSpeedString(worker.Speed, "B/s"),
					utils.FormatSpeedString(rdi.WorkerBaselineSpeed, "B/s"))
			}
			worker.Status = WorkerStatusIdle
		default:
		}

		// if no signal from health monitor continue with downloading the chunk
		// ask for a chunk task from the scheduler
		chunkTask, ok := rdi.pickTaskForWorker()
		if !ok {
			worker.KillWorker()
			return
		}
		if chunkTask == nil {
			continue
		}

		err := worker.downloadChunk(chunkTask, rdi)
		if err != nil {
			onError(err)
		}

		rdi.Logger.Writes.Printf("[Worker %2d::Chunk %4d] WROTE CHUNK | Start: %d | End: %d",
			worker.ID,
			chunkTask.Index,
			chunkTask.Start,
			chunkTask.End-1,
		)
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
