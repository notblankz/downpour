package downloader

import (
	"context"
	"downpour/internal/utils"
	"fmt"
	"io"
	"log"
	"os"
	"strings"
	"sync/atomic"
	"time"
)

// struct to implement io.Writer for custom use of WriteAt() instead of Write() in io.Copy()
type chunkWriter struct {
	buf         []byte
	worker      *WorkerInfo
	curTask     *ChunkTask
	chunkLogger *log.Logger
	file        *os.File

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
	delta, done := cw.curTask.advanceCommitedBytes(newCommittedBytes)
	if delta > 0 {
		cw.curTask.addWorkerContribution(cw.worker, delta)
		cw.globalBytesWritten.Add(delta)
	}

	if done && cw.chunkLogger != nil && cw.curTask.LoggedOnce.CompareAndSwap(false, true) {
		entries := cw.curTask.getWorkerContributions()
		logMsg := strings.Builder{}

		var durationStr string
		if startedAt, ok := cw.curTask.getStartTime(); ok {
			durationStr = utils.FormatDuration(time.Since(startedAt))
		} else {
			durationStr = "unknown"
		}

		fmt.Fprintf(&logMsg, "[CHUNK %04d] CHUNK DONE (in %s)", cw.curTask.Index, durationStr)

		for _, entry := range entries {
			role := "Normal"
			if entry.IsHedging {
				role = "Hedge"
			}
			fmt.Fprintf(&logMsg, ", [Worker %02d (%s) : %s]", entry.WorkerID, role, utils.FormatBytes(entry.CommittedBytes))
		}

		cw.chunkLogger.Printf("[SUCCESS] %s", logMsg.String())
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
