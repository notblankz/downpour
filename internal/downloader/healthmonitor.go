package downloader

import (
	"context"
	"sort"
	"time"
)

type RestartReason struct {
	Speed    float64
	Baseline float64
}

const MonitorInterval = 1 * time.Second

func (rdi *RangeDownloadInfo) StartHealthMonitor(ctx context.Context) error {
	ticker := time.NewTicker(MonitorInterval)
	defer ticker.Stop()
	MirrorWorkerMap := make(map[*MirrorInfo][]*WorkerInfo)

	for {
		select {
		case <-ticker.C:

			clear(MirrorWorkerMap)
			// reset mirror speeds
			for _, mi := range rdi.Mirrors {
				mi.Speed = 0
			}
			// copy current worker speeds
			var workerSpeeds []float64
			for _, wi := range rdi.Workers.Slice {
				wi.UpdateSpeed()
				MirrorWorkerMap[wi.Mirror] = append(MirrorWorkerMap[wi.Mirror], wi)
				wi.Mirror.Speed += wi.Speed
				if wi.Status == WorkerStatusDownloading || wi.Status == WorkerStatusRequesting || wi.Status == WorkerStatusRetrying {
					workerSpeeds = append(workerSpeeds, wi.Speed)
				}
			}

			if len(workerSpeeds) == 0 {
				continue
			}

			sort.Slice(workerSpeeds, func(i, j int) bool {
				return workerSpeeds[i] > workerSpeeds[j]
			})

			// find number of entries to trim for mean
			trimCount := int(float64(len(workerSpeeds)) * 0.15)
			if trimCount == 0 && rdi.Workers.Limit >= 4 {
				// a small safeguard to make sure we drop atleast 1 value from both ends
				trimCount = 1
			}

			// find trimmed mean
			workerSpeedSummation := 0.0
			validWorkers := 0
			for i := trimCount; i < (len(workerSpeeds) - trimCount); i++ {
				workerSpeedSummation += workerSpeeds[i]
				validWorkers++
			}
			if validWorkers > 0 {
				rdi.WorkerBaselineSpeed = workerSpeedSummation / float64(validWorkers)
			} else {
				// fallback if something weird happens
				rdi.WorkerBaselineSpeed = workerSpeeds[len(workerSpeeds)/2]
			}

			// find and restart workers that are slower and have not been recently restarted
			if rdi.BytesWritten.Load() <= int64(0.98*float64(rdi.TotalSize)) {
				for _, wi := range rdi.Workers.Slice {
					if wi.Status != WorkerStatusDone &&
						wi.Status != WorkerStatusIdle &&
						wi.Speed < (0.3*rdi.WorkerBaselineSpeed) &&
						time.Since(wi.RestartedAt) > 5*time.Second &&
						time.Since(wi.LastChunkDoneAt) > 3*time.Second {

						reason := RestartReason{
							Speed:    wi.Speed,
							Baseline: rdi.WorkerBaselineSpeed,
						}
						signalRestart(wi.RestartWorkerChan, reason)
						wi.RestartedAt = time.Now()
					}
				}
			}

			if len(rdi.Mirrors) > 1 {
				rdi.rebalanceMirrors(MirrorWorkerMap)
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// function to perform non-blocking send
func signalRestart(ch chan RestartReason, reason RestartReason) {
	select {
	case ch <- reason:
	default:
	}
}
