package main

import (
	"flag"
	"net/http"
	"path/filepath"

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

	url := flag.Arg(0)

	resp, err := http.Head(url)
	if err != nil {
		panic(err)
	}
	totalSize := resp.ContentLength
	resp.Body.Close()

	filename := filepath.Base(url)

	m := ui.InitialModel(filename, totalSize)
	p := tea.NewProgram(m)

	go downloader.Download(
		url,

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

	if _, err := p.Run(); err != nil {
		panic(err)
	}
}
