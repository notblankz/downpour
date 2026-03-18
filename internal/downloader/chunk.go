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

	ChunkVersion      atomic.Int32
	UnresolvedTickets atomic.Int32
}

func (ct *ChunkTask) reserveRange(maxBlock int64) (start int64, end int64, ok bool) {
	for {
		cur := ct.ReservationHead.Load()
		if cur >= ct.End {
			return 0, 0, false
		}

		// choose whichever is smaller -> in case cur + maxBlock goes ahead of ct.End
		next := min(cur+maxBlock, ct.End)

		if ct.ReservationHead.CompareAndSwap(cur, next) {
			return cur, next, true
		}
	}
}
