package downloader

import "time"

type ChunkInfo struct {
	Index           int64
	Size            int64
	BytesDownloaded int64
}

type WorkerStatus string

const (
	WorkerStatusIdle        WorkerStatus = "idle"
	WorkerStatusRequesting  WorkerStatus = "requesting"
	WorkerStatusDownloading WorkerStatus = "downloading"
	WorkerStatusRetrying    WorkerStatus = "retrying"
)

type WorkerInfo struct {
	ID                int
	Chunk             ChunkInfo
	Speed             float64
	TotalBytesWritten int64
	LastBytes         int64
	LastSample        time.Time
	Status            WorkerStatus
	StartedAt         time.Time
}

func (info *WorkerInfo) UpdateSpeed() float64 {
	if info.LastSample.IsZero() {
		info.LastBytes = info.TotalBytesWritten
		info.LastSample = time.Now()
		return 0
	}
	deltaBytes := info.TotalBytesWritten - info.LastBytes
	info.LastBytes = info.TotalBytesWritten
	deltaTime := time.Since(info.LastSample).Seconds()
	info.LastSample = time.Now()
	curSpeed := float64(deltaBytes) / deltaTime
	info.Speed = curSpeed
	return curSpeed
}
