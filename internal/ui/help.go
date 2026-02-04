package ui

import "fmt"

func PrintHelp() {
	fmt.Print(`downpour - simple download manager

usage: downpour <url> [options]

options:
	-h, --help     show this help message
`)
}
