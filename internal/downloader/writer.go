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
	file               *os.File
	offset             int64
	chunkEndOffset     int64
	globalBytesWritten *atomic.Int64
}

func (cw *chunkWriter) Write(p []byte) (int, error) {
	nwrite, err := cw.file.WriteAt(p, int64(cw.offset))
	if err != nil {
		return nwrite, fmt.Errorf("Could not write to file at offset %v - %v", cw.offset, err)
	}
	cw.offset += int64(nwrite)
	cw.worker.Chunk.BytesDownloaded += int64(nwrite)
	cw.worker.TotalBytesWritten += int64(nwrite)
	updatedOffset := cw.offset
	oldSharedOffset := cw.worker.Chunk.SharedWriteOffset.Load()
	if updatedOffset > oldSharedOffset {
		if cw.worker.Chunk.SharedWriteOffset.CompareAndSwap(oldSharedOffset, updatedOffset) {
			newBytes := updatedOffset - oldSharedOffset
			cw.globalBytesWritten.Add(newBytes)
		}
	} else if oldSharedOffset >= cw.chunkEndOffset {
		cw.worker.Chunk.Cancel()
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
