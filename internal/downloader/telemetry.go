package downloader

import (
	"context"
	"fmt"
	"os"
	"time"
)

func StartTelemetry(ctx context.Context, rdi *RangeDownloadInfo, filename string) error {
	f, err := os.Create(filename)
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
