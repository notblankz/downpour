package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/notblankz/downpour/internal/engine"
	"github.com/spf13/cobra"
)

func main() {
	var (
		output    string
		workers   int
		chunkSize string
	)
	root := &cobra.Command{
		Use:   "downpour <url>",
		Short: "A fast concurrent file downloader",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cs, err := parseSize(chunkSize)
			if err != nil {
				return fmt.Errorf("invalid chunk size: %w", err)
			}
			cfg := engine.NewConfig(args[0], output, workers, cs)
			return engine.NewDownloader(cfg).Run(cmd.Context())
		},
	}

	f := root.Flags()
	f.StringVarP(&output, "output", "o", "download.bin", "output file path")
	f.IntVarP(&workers, "workers", "w", 16, "number of concurrent workers (1-32)")
	f.StringVarP(&chunkSize, "chunk-size", "c", "4MB", "chunk size each worker downloads")

	if err := root.Execute(); err != nil {
		os.Exit(1)
	}
}

// parseSize turns "4MB" / "512KB" / raw bytes into a byte count
func parseSize(s string) (int64, error) {
	s = strings.TrimSpace(strings.ToUpper(s))
	mult := int64(1)
	switch {
	case strings.HasSuffix(s, "KB"):
		mult, s = 1<<10, strings.TrimSuffix(s, "KB")
	case strings.HasSuffix(s, "MB"):
		mult, s = 1<<20, strings.TrimSuffix(s, "MB")
	case strings.HasSuffix(s, "GB"):
		mult, s = 1<<30, strings.TrimSuffix(s, "GB")
	case strings.HasSuffix(s, "B"):
		s = strings.TrimSuffix(s, "B")
	}

	n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
	if err != nil {
		return 0, fmt.Errorf("bad size %q", s)
	}

	return n * mult, nil
}
