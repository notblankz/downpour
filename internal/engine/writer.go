package engine

import (
	"os"
	"sync"
	"sync/atomic"
)

// PieceWriter adapts file.WriteAt into an io.Writer, writing a piece at an
// advancing offset and adding each write to the global progress counter
type PieceWriter struct {
	file     *os.File
	offset   int64
	progress *atomic.Int64
}

// NewPieceWriter returns a PieceWriter positioned at start
func NewPieceWriter(f *os.File, start int64, progress *atomic.Int64) *PieceWriter {
	return &PieceWriter{
		file:     f,
		offset:   start,
		progress: progress,
	}
}

// Write writes b at the current offset via WriteAt, advances the offset and records progress
func (w *PieceWriter) Write(b []byte) (int, error) {
	n, err := w.file.WriteAt(b, w.offset)
	w.offset += int64(n)
	w.progress.Add(int64(n))
	return n, err
}

const bufSize = 512 << 10

var bufPool = sync.Pool{
	New: func() any {
		b := make([]byte, bufSize)
		return &b
	},
}

// GetBuffer borrows a reusable copy buffer from the pool
func GetBuffer() *[]byte {
	return bufPool.Get().(*[]byte)
}

// PutBuffer returns a buffer to the pool for reuse
func PutBuffer(b *[]byte) {
	bufPool.Put(b)
}
