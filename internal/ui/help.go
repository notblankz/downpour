package ui

import "fmt"

func PrintHelp() {
	fmt.Print(`downpour - high-performance concurrent download manager

Usage:
  downpour <url> [options]

Options:
  -h,  --help         Show this help message
  -t,  --telemetry    Generate a CSV file with download telemetry data
  -l,  --httplog      Generate an HTTP trace logfile
  -m,  --mirrors      Mirror URLs (comma-separated)
  -c,  --checksum     Verify the downloaded file against this expected hash
  -a,  --algorithm    Specify the cryptographic algorithm for validation (e.g., sha256, md5)
  -v,  --version      Print version
`)
}
