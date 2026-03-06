package downloader

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"
)

func (rdi *RangeDownloadInfo) StartTelemetry(ctx context.Context) error {
	f, err := os.Create(filepath.Join(rdi.DirName, "telemetry.csv"))
	if err != nil {
		return fmt.Errorf("[WARNING] Could not create the telemetry CSV file")
	}
	defer f.Close()

	fmt.Fprintf(f, "Timestamp(s),TotalBytes,Speed(B/s)\n")

	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	var lastDownloaded int64
	startTime := time.Now()

	for {
		select {
		case <-ctx.Done():
			return nil
		case t := <-ticker.C:
			currentTotal := rdi.BytesWritten.Load()

			delta := currentTotal - lastDownloaded

			lastDownloaded = currentTotal

			elapsed := t.Sub(startTime).Seconds()
			fmt.Fprintf(f, "%.0f,%d,%.0f\n", elapsed, currentTotal, float64(delta))
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
