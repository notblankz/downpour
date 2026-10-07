package engine

const (
	minChunkSize = 256 << 10
	maxWorkers   = 32
)

type Config struct {
	URL       string
	Output    string
	Workers   int
	ChunkSize int64
}

// NewConfig builds a Config, clamping workers and chunk size into safe ranges
func NewConfig(url, output string, workers int, chunkSize int64) Config {
	switch {
	case workers < 1:
		workers = 1
	case workers > maxWorkers:
		workers = maxWorkers
	}

	if chunkSize < minChunkSize {
		chunkSize = minChunkSize
	}

	return Config{
		URL:       url,
		Output:    output,
		Workers:   workers,
		ChunkSize: chunkSize,
	}
}
