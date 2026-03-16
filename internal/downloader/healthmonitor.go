package downloader

import (
	"context"
	"sort"
	"time"
)

func (rdi *RangeDownloadInfo) StartHealthMonitor(ctx context.Context) error {
	ticker := time.NewTicker(1 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:

			// copy current worker speeds
			var workerSpeeds []float64
			var activeWorkers []*WorkerInfo
			var idleWorkers []*WorkerInfo
			for _, wi := range rdi.Workers.Slice {
				wi.UpdateSpeed()
				if wi.Status == WorkerStatusDone {
					idleWorkers = append(idleWorkers, wi)
				} else {
					activeWorkers = append(activeWorkers, wi)
					workerSpeeds = append(workerSpeeds, wi.Speed)
				}
			}

			// HEDGE WORKING COMMENTED OUT TILL Normal Queue works with ChunkTask
			// hedge work if below condition is met
			// if len(idleWorkers) > 0 {
			// 	// find the worst ACTIVE workers
			// 	sort.Slice(activeWorkers, func(i, j int) bool {
			// 		return activeWorkers[i].Speed < activeWorkers[j].Speed
			// 	})

			// 	// find the best IDLE workers based on their Speed throughout the download
			// 	sort.Slice(idleWorkers, func(i, j int) bool {
			// 		return idleWorkers[i].Speed > idleWorkers[j].Speed
			// 	})

			// 	progress := float64(rdi.BytesWritten.Load()) / float64(rdi.TotalSize)

			// 	var hedgeThreshold float64
			// 	switch {
			// 	case progress >= 0.90:
			// 		hedgeThreshold = 0.8 * rdi.WorkerBaselineSpeed
			// 	case progress >= 0.75:
			// 		hedgeThreshold = 0.5 * rdi.WorkerBaselineSpeed
			// 	default:
			// 		hedgeThreshold = 0.3 * rdi.WorkerBaselineSpeed
			// 	}

			// 	idleIndex := 0
			// 	for _, aw := range activeWorkers {
			// 		if aw.Chunk.Hedged.Load() {
			// 			continue
			// 		}
			// 		if aw.Speed >= hedgeThreshold {
			// 			continue
			// 		}
			// 		if idleIndex >= len(idleWorkers) {
			// 			break
			// 		}
			// 		rdi.HedgeWg.Add(1)
			// 		idleWorkers[idleIndex].HedgeChan <- HedgeChunk{
			// 			ChunkIndex:        aw.Chunk.Index,
			// 			SharedWriteOffset: aw.Chunk.SharedWriteOffset,
			// 		}
			// 		idleIndex++
			// 	}
			// }

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
					if wi.Status != WorkerStatusDone && wi.Speed < (0.3*rdi.WorkerBaselineSpeed) && (time.Since(wi.RestartedAt) > 5*time.Second) {
						signalRestart(wi.RestartWorkerChan)
						wi.RestartedAt = time.Now()
					}
				}
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// function to perform non-blocking send
func signalRestart(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}
