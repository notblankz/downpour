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

	Ctx    context.Context
	Cancel context.CancelFunc

	CommittedBytes atomic.Int64 // total bytes committed (perfectly written) to the file
}
