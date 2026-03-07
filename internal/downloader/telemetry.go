package downloader

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (rdi *RangeDownloadInfo) StartTelemetry(ctx context.Context) error {
	f, err := os.Create(filepath.Join(rdi.DirName, "telemetry.csv"))
	if err != nil {
		return fmt.Errorf("[WARNING] Could not create the telemetry CSV file")
	}
	defer f.Close()

	// Build worker's speed headers
	var workersSpeedHeader strings.Builder
	for _, workerInfo := range rdi.Workers.Slice {
		fmt.Fprintf(&workersSpeedHeader, "W%d(B/s),", workerInfo.ID)
	}
	fmt.Fprintf(f, "Timestamp(s),TotalBytes,Speed(B/s),%s\n", workersSpeedHeader.String())

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastDownloaded int64
	startTime := time.Now()
	lastWorkerBytes := make([]int64, rdi.Workers.Limit)
	lastWorkerSnapshot := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil
		case t := <-ticker.C:
			// download data
			currentTotal := rdi.BytesWritten.Load()

			delta := currentTotal - lastDownloaded

			lastDownloaded = currentTotal

			elapsed := t.Sub(startTime).Seconds()

			// worker details
			var workersSpeed strings.Builder
			currTime := time.Now()
			for i, workerInfo := range rdi.Workers.Slice {
				elapsedWorkerTime := time.Since(lastWorkerSnapshot)
				delta := workerInfo.TotalBytesWritten - lastWorkerBytes[i]
				currWorkerSpeed := float64(delta) / elapsedWorkerTime.Seconds()
				lastWorkerBytes[i] = workerInfo.TotalBytesWritten
				fmt.Fprintf(&workersSpeed, "%.0f,", currWorkerSpeed)
			}
			lastWorkerSnapshot = currTime
			fmt.Fprintf(f, "%.0f,%d,%.0f,%s\n", elapsed, currentTotal, float64(delta), workersSpeed.String())
		}
	}
}

func (rdi *RangeDownloadInfo) getTraceLogger() (*log.Logger, *os.File) {
	f, err := os.OpenFile(filepath.Join(rdi.DirName, "httptrace.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		panic(err)
	}
	return log.New(f, "", log.LstdFlags|log.Lmicroseconds), f
}
