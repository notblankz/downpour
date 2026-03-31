package downloader

import (
	"context"
	"sync/atomic"
)

type ChunkTask struct {
	Index int64
	Start int64
	End   int64 // End Exclusive

	Done atomic.Bool
	// ActiveHedgers tracks workers currently racing this chunk via hedge assignment.
	ActiveHedgers atomic.Int32

	Ctx    context.Context
	Cancel context.CancelFunc

	CommittedBytes atomic.Int64 // total bytes committed (perfectly written) to the file
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
