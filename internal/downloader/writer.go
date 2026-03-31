package downloader

import (
	"context"
	"io"
	"os"
	"sync/atomic"
)

// To be implemented after implementation of multi mirror downloading
// type writeJob struct {
// 	buf    *[]byte
// 	offset int64
// 	n      int64
// }

// func writerWorker(jobQueue chan writeJob, f *os.File, pool *sync.Pool, bytesWrittern *atomic.Int64, onError ErrorFunc) {

// }

// struct to implement io.Writer for custom use of WriteAt() instead of Write() in io.Copy()

type chunkWriter struct {
	buf     []byte
	worker  *WorkerInfo
	curTask *ChunkTask
	file    *os.File

	requestStart int64
	requestEnd   int64
	localWritten int64

	globalBytesWritten *atomic.Int64
}

func (cw *chunkWriter) Write(toWrite []byte) (int, error) {
	if cw.curTask == nil {
		return 0, nil
	}

	// check for context cancellation before proceeding
	select {
	case <-cw.curTask.Ctx.Done():
		return 0, cw.curTask.Ctx.Err()
	default:
	}

	fileOffset := cw.requestStart + cw.localWritten
	remaining := cw.requestEnd - fileOffset
	if remaining <= 0 {
		return 0, context.Canceled
	}

	if int64(len(toWrite)) > remaining {
		toWrite = toWrite[:remaining]
	}

	nwrite, err := cw.file.WriteAt(toWrite, fileOffset)
	if err != nil {
		return nwrite, err
	}
	cw.localWritten += int64(nwrite)

	cw.worker.TotalBytesWritten += int64(nwrite)
	newCommittedBytes := (fileOffset + int64(nwrite)) - cw.curTask.Start
	delta, _ := cw.curTask.advanceCommitedBytes(newCommittedBytes)
	if delta > 0 {
		cw.globalBytesWritten.Add(delta)
	}

	return nwrite, nil
}

// copy with progress callback
func streamCopy(src io.Reader, dst io.Writer, onProgress ProgressFunc) (int64, error) {
	var totalWritten, chunkWritten int64
	buf := make([]byte, bufferSize)

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
