package downloader

import (
	"context"
	"sync/atomic"
)

type ChunkTask struct {
	Index int64
	Start int64
	End   int64 // End Exclusive

	Hedged atomic.Bool
	Done   atomic.Bool

	Ctx    context.Context
	Cancel context.CancelFunc

	ReservationHead atomic.Int64 // Next reservable range's start byte
	CommittedBytes  atomic.Int64 // total bytes committed (perfectly written) to the file
}

func (ct *ChunkTask) reserveRange(maxBlock int64) (start int64, end int64, ok bool) {
	for {
		cur := ct.ReservationHead.Load()
		if cur >= ct.End {
			return 0, 0, false
		}

		next := cur + maxBlock
		if next >= ct.End {
			next = ct.End
		}

		if ct.ReservationHead.CompareAndSwap(cur, next) {
			return cur, next, true
		}
	}
}
