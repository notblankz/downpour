package downloader

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"
)

type ChunkTask struct {
	Index int64
	Start int64
	End   int64 // End Exclusive

	// time stored as Int64 (unix nano seconds) since time.Time{} is not thread safe
	StartTimeNsec atomic.Int64

	Done atomic.Bool
	// ActiveHedgers tracks workers currently racing this chunk via hedge assignment.
	ActiveHedgers atomic.Int32

	Ctx    context.Context
	Cancel context.CancelFunc

	CommittedBytes atomic.Int64 // total bytes committed (perfectly written) to the file

	WorkerContributions sync.Map    // map of WorkerID[WorkerContributionEntry]
	LoggedOnce          atomic.Bool // this is to make sure only one log line is printed globally
}

type WorkerContributionEntry struct {
	WorkerID       int
	CommittedBytes int64
	IsHedging      bool
}

const maxHedgersPerChunk = 3

func (ct *ChunkTask) isUsable() bool {
	if ct == nil {
		return false
	}

	if ct.Done.Load() {
		return false
	}

	if ct.Ctx != nil {
		select {
		case <-ct.Ctx.Done():
			return false
		default:
		}
	}
	return true
}

func (ct *ChunkTask) isHedgeable() bool {
	if !ct.isUsable() {
		return false
	}

	// if the chunk is in it's last stages no point in hedging it
	if ct.CommittedBytes.Load() >= int64(0.90*(float64(ct.End)-float64(ct.Start))) {
		return false
	}

	return true
}

func (ct *ChunkTask) tryAcquireHedgeSlot() bool {
	if ct == nil {
		return false
	}
	for {
		active := ct.ActiveHedgers.Load()
		if active >= maxHedgersPerChunk {
			return false
		}
		if ct.ActiveHedgers.CompareAndSwap(active, active+1) {
			return true
		}
	}
}

func (ct *ChunkTask) releaseHedgeSlot() {
	if ct == nil {
		return
	}
	for {
		active := ct.ActiveHedgers.Load()
		if active <= 0 {
			return
		}
		if ct.ActiveHedgers.CompareAndSwap(active, active-1) {
			return
		}
	}
}

// advances the chunks committed bytes and returns the amount of unique progress added.
func (ct *ChunkTask) advanceCommitedBytes(newFrontier int64) (delta int64, done bool) {
	if ct == nil {
		return 0, false
	}

	chunkLen := ct.End - ct.Start
	if chunkLen <= 0 {
		return 0, true
	}

	if newFrontier < 0 {
		newFrontier = 0
	}
	if newFrontier > chunkLen {
		newFrontier = chunkLen
	}

	for {
		old := ct.CommittedBytes.Load()
		if old >= chunkLen {
			if ct.Done.CompareAndSwap(false, true) {
				ct.Cancel()
			}
			return 0, true
		}
		if newFrontier <= old {
			return 0, false
		}
		if ct.CommittedBytes.CompareAndSwap(old, newFrontier) {
			delta = newFrontier - old
			if newFrontier >= chunkLen {
				if ct.Done.CompareAndSwap(false, true) {
					ct.Cancel()
				}
				return delta, true
			}
			return delta, false
		}
	}
}

func (ct *ChunkTask) addWorkerContribution(worker *WorkerInfo, delta int64) {
	if ct == nil || delta <= 0 {
		return
	}

	// update the map if worker entry available
	if v, ok := ct.WorkerContributions.Load(worker.ID); ok {
		wc := v.(*WorkerContributionEntry)
		wc.CommittedBytes += delta
		if worker.IsHedging {
			wc.IsHedging = true
		}
		return
	}

	// create a new key value entry if lookup fails
	ct.WorkerContributions.Store(worker.ID, &WorkerContributionEntry{
		WorkerID:       worker.ID,
		CommittedBytes: delta,
		IsHedging:      worker.IsHedging,
	})
}

func (ct *ChunkTask) getWorkerContributions() []WorkerContributionEntry {
	if ct == nil {
		return nil
	}

	output := make([]WorkerContributionEntry, 0, 4)

	ct.WorkerContributions.Range(func(key, value any) bool {
		wc, ok := value.(*WorkerContributionEntry)
		if !ok || wc == nil {
			return true
		}

		if wc.CommittedBytes <= 0 {
			return true
		}

		output = append(output, *wc)
		return true
	})

	// Sorts all normal workers in the start and according to worker IDs (ascending)
	sort.Slice(output, func(i, j int) bool {
		if output[i].IsHedging != output[j].IsHedging {
			return !output[i].IsHedging
		}
		return output[i].WorkerID < output[j].WorkerID
	})

	return output
}

func (ct *ChunkTask) trySetStartTime(now time.Time) bool {
	if ct == nil {
		return false
	}

	return ct.StartTimeNsec.CompareAndSwap(0, now.UnixNano())
}

func (ct *ChunkTask) getStartTime() (time.Time, bool) {
	if ct == nil {
		return time.Time{}, false
	}

	nsec := ct.StartTimeNsec.Load()
	if nsec == 0 {
		return time.Time{}, false
	}

	return time.Unix(0, nsec), true
}
