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
			for _, workerInfo := range rdi.Workers.Slice {
				fmt.Fprintf(&workersSpeed, "%.0f,", workerInfo.Speed)
			}
			fmt.Fprintf(f, "%.0f,%d,%.0f,%s\n", elapsed, currentTotal, float64(delta), workersSpeed.String())
		}
	}
}

type Logger struct {
	HttpTrace *log.Logger
	Restarts  *log.Logger
	Writes    *log.Logger
}

func (rdi *RangeDownloadInfo) getLoggers() (*Logger, []*os.File, error) {
	openLog := func(name string) (*log.Logger, *os.File, error) {
		f, err := os.OpenFile(filepath.Join(rdi.DirName, name), os.O_TRUNC|os.O_CREATE|os.O_WRONLY, 0644)
		if err != nil {
			return nil, nil, err
		}
		return log.New(f, "", log.LstdFlags|log.Lmicroseconds), f, nil
	}

	var files []*os.File
	logger := &Logger{}

	l, f, err := openLog("httptrace.log")
	if err != nil {
		return nil, nil, err
	}
	logger.HttpTrace, files = l, append(files, f)

	l, f, err = openLog("restarts.log")
	if err != nil {
		return nil, nil, err
	}
	logger.Restarts, files = l, append(files, f)

	l, f, err = openLog("chunks.log")
	if err != nil {
		return nil, nil, err
	}
	logger.Writes, files = l, append(files, f)

	return logger, files, nil
}
