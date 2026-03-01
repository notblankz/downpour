package downloader

import (
	"os"
	"sync"
	"sync/atomic"
)

type writeJob struct {
	buf    *[]byte
	offset int64
	n      int64
}

func writerWorker(jobQueue chan writeJob, f *os.File, pool *sync.Pool, bytesWrittern *atomic.Int64, onError ErrorFunc) {
	
}
