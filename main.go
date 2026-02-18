package main

import (
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
	helpFlag := flag.Bool("help", false, "show help")
	flag.BoolVar(helpFlag, "h", false, "show help (shorthand)")

	httpLog := flag.Bool("httplog", false, "generate http trace logfile")
	flag.BoolVar(httpLog, "hl", false, "generate http trace logfile (shorthand)")
	flag.Parse()

	if *helpFlag {
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

	rdi := downloader.InitRangeDownloadInfo(filename, totalSize, urlString, *httpLog)
	m := ui.InitialModel(filename, totalSize, acceptRangeBool, rdi)
	p := tea.NewProgram(m)

	if acceptRangeBool {
		go rdi.RangeDownload(
			func() {
				p.Send(ui.DoneMsg{})
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
func startErrorUI(err error) {
	m := ui.InitialModel("Unknown", 0, false, nil)
	p := tea.NewProgram(m)
	if _, err := p.Run(); err != nil {
		os.Exit(1)
	}
	p.Send(ui.ErrorMsg{Err: err})
}
