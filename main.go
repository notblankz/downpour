package main

import (
	"flag"
	"net/http"
	"net/url"

	"downpour/internal/downloader"
	"downpour/internal/ui"

	tea "github.com/charmbracelet/bubbletea"
)

func main() {
	helpFlag := flag.Bool("help", false, "show help")
	flag.BoolVar(helpFlag, "h", false, "show help (shorthand)")
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

	// find InitialModel values -> TotalSize & AcceptRanges
	resp, err := http.Head(urlString)
	if err != nil {
		panic(err)
	}
	totalSize := resp.ContentLength

	var acceptRangeBool bool
	if acceptRange := resp.Header.Get("Accept-Ranges"); acceptRange == "bytes" {
		acceptRangeBool = true
	}
	resp.Body.Close()

	parsedUrl, err := url.Parse(urlString)
	if err != nil {
		panic(err)
	}
	filename := downloader.GetFileName(parsedUrl, resp)

	rdi := downloader.InitRangeDownloadInfo(filename, totalSize, urlString)
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
