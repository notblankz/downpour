package downloader

import (
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

	globalBytesWritten *atomic.Int64
}

func (cw *chunkWriter) Write(p []byte) (int, error) {
	fileOffest := cw.curTask.Start + cw.curTask.CommittedBytes.Load()
	nwrite, err := cw.file.WriteAt(p, fileOffest)
	if err != nil {
		return nwrite, err
	}

	cw.worker.TotalBytesWritten += int64(nwrite)
	cw.curTask.CommittedBytes.Add(int64(nwrite))
	cw.globalBytesWritten.Add(int64(nwrite))

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
