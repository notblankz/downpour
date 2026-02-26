package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"downpour/internal/downloader"
	"downpour/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	var helpFlag, httpLog, telemetryFlag bool
	var expectedHash, algorithm string

	flag.BoolVar(&helpFlag, "help", false, "Show help message")
	flag.BoolVar(&helpFlag, "h", false, "Show help message (shorthand)")

	flag.BoolVar(&httpLog, "httplog", false, "Generate HTTP trace logfile")
	flag.BoolVar(&httpLog, "hl", false, "Generate HTTP trace logfile (shorthand)")

	flag.BoolVar(&telemetryFlag, "telemetry", false, "Generate download telemetry CSV")
	flag.BoolVar(&telemetryFlag, "tel", false, "Generate download telemetry CSV (shorthand)")

	flag.StringVar(&expectedHash, "checksum", "", "Expected checksum hash")
	flag.StringVar(&expectedHash, "c", "", "Expected checksum hash (shorthand)")

	flag.StringVar(&algorithm, "algorithm", "", "Cryptographic algorithm")
	flag.StringVar(&algorithm, "a", "", "Cryptographic algorithm (shorthand)")

	flag.Parse()

	if helpFlag {
		ui.PrintHelp()
		return
	}

	if len(flag.Args()) != 1 {
		ui.PrintHelp()
		return
	}

	urlString := flag.Arg(0)

	req, _ := http.NewRequest("GET", urlString, nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 Downpour/1.0")
	req.Header.Set("Range", "bytes=0-0")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		startErrorUI(fmt.Errorf("network error: %w", err))
		return
	}
	defer resp.Body.Close()

	// extract stuff from resp
	var totalSize int64
	var acceptRangeBool bool

	if resp.StatusCode == http.StatusPartialContent {
		acceptRangeBool = true
		contentRange := resp.Header.Get("Content-Range")
		if contentRange != "" {
			parts := strings.Split(contentRange, "/")
			if len(parts) == 2 {
				fmt.Sscanf(parts[1], "%d", &totalSize)
			}
		}
	} else {
		// fallback
		totalSize = resp.ContentLength
		acceptRangeBool = false
	}

	parsedUrl, err := url.Parse(urlString)
	if err != nil {
		panic(err)
	}
	filename := downloader.GetFileName(parsedUrl, resp)

	rdi := downloader.InitRangeDownloadInfo(filename, totalSize, urlString, httpLog)
	m := ui.InitialModel(filename, totalSize, acceptRangeBool, rdi)
	p := tea.NewProgram(m)

	if telemetryFlag {
		ctx, cancelTelemetry := context.WithCancel(context.Background())
		defer cancelTelemetry()
		go downloader.StartTelemetry(ctx, rdi, fmt.Sprintf("%s.csv", filename))
	}

	if algorithm != "" && expectedHash != "" {
		algo := strings.ToLower(algorithm)
		hashStr := expectedHash

		algoInfo, exists := downloader.SupportedChecksum[algo]
		if !exists {
			p.Send(ui.ErrorMsg{Err: fmt.Errorf("Error: Algorithm '%s' is not supported\n", algo)})
		}

		if len(hashStr) != algoInfo.ChecksumLen {
			p.Send(ui.ErrorMsg{Err: fmt.Errorf("Error: Invalid %s checksum length. Expected %d characters, got %d\n", algo, algoInfo.ChecksumLen, len(hashStr))})
		}

		rdi.Checksum = &downloader.ChecksumInfo{
			ExpectedHash: strings.ToLower(hashStr),
			AlgoName:     algo,
			Algo:         algoInfo,
		}
	}

	if acceptRangeBool {
		go rdi.RangeDownload(
			func() {
				p.Send(ui.DoneMsg{})
			},

			func() {
				p.Send(ui.VerifyingMsg{})
			},

			func(err error) {
				p.Send(ui.ErrorMsg{Err: err})
			},
		)
	} else {
		go downloader.StreamDownload(
			*parsedUrl,

			func(n int64) {
				p.Send(ui.ProgressMsg{Bytes: int(n)})
			},

			func() {
				p.Send(ui.DoneMsg{})
			},

			func(err error) {
				p.Send(ui.ErrorMsg{Err: err})
			},
		)
	}

	if _, err := p.Run(); err != nil {
		panic(err)
	}
}

// <== Helper Functions ==>
// FIX
func startErrorUI(err error) {
	m := ui.InitialModel("Unknown", 0, false, nil)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		os.Exit(1)
	}
	p.Send(ui.ErrorMsg{Err: err})
}
