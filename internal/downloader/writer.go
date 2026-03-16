package downloader

import (
	"fmt"
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
	buf                []byte
	worker             *WorkerInfo
	curTask            *ChunkTask
	file               *os.File
	localWriteHead     int64
	globalBytesWritten *atomic.Int64
}

func (cw *chunkWriter) Write(p []byte) (int, error) {
	nwrite, err := cw.file.WriteAt(p, int64(cw.localWriteHead))
	if err != nil {
		return nwrite, fmt.Errorf("Could not write to file at offset %d - %v", cw.localWriteHead, err)
	}

	cw.localWriteHead += int64(nwrite)
	cw.worker.TotalBytesWritten += int64(nwrite)

	// update this chunk writer's write head to check if this chunk writer with it's worker actually did some work
	updatedLocalWriteHead := cw.localWriteHead

	// Hardening of CAS instead of using One-Shot CAS - Suggestion by GPT-5.3-Codex
	// This loop is to make sure that we check there are no new bytes that need to be updated into globalBytesWritten
	// Consider this scenario Worker A performs CAS(1000 -> 1500) then Worker B tries to perform CAS(1000 -> 1800) globalBytesWritten
	// will only move forward 500 Bytes and ignore Worker B's remaining 300 Bytes
	// with the loop since none of the if stmnts execute we loop back and now oldFrontier becomes 1500 and hence Worker B's remaining 300 Bytes is counted
	for {
		sharedTaskWriteHead := cw.curTask.WriteHead.Load()
		if updatedLocalWriteHead <= sharedTaskWriteHead {
			break
		}
		if cw.curTask.WriteHead.CompareAndSwap(sharedTaskWriteHead, updatedLocalWriteHead) {
			cw.globalBytesWritten.Add(updatedLocalWriteHead - sharedTaskWriteHead)
			break
		}
	}

	// Check completition of Chunk
	if cw.curTask.WriteHead.Load() >= cw.curTask.End {
		cw.curTask.Done.Store(true)
		cw.curTask.Cancel()
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
