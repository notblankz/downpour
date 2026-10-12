package tui

import (
	"fmt"
	"math"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

var (
	stTitle = lipgloss.NewStyle().Foreground(lipgloss.Color("5")).Bold(true)
	stMuted = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	stPct   = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	stSpeed = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	stOK    = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	stErr   = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

func (m model) View() string {
	if m.done {
		return m.doneView()
	}
	return m.progressView()
}

// header renders the "downpour-ing <file> from <host>" headline
func (m model) header() string {
	return stTitle.Render("downpour-ing") + " " + filepath.Base(m.cfg.Output) + " " + stMuted.Render("from") + " " + hostOf(m.cfg.URL)
}

func (m model) progressView() string {
	width := m.width
	if width <= 0 {
		width = 80
	}
	s := m.snap
	ratio := 0.0
	if s.Total > 0 {
		ratio = float64(s.Downloaded) / float64(s.Total)
	}

	info := stMuted.Render(fmt.Sprintf("%s · %d workers · %s chunks", humanBytes(s.Total), m.cfg.Workers, humanBytes(m.cfg.ChunkSize)))

	status := stMuted.Render("connecting…")
	if s.Total > 0 {
		status = stMuted.Render(fmt.Sprintf("%s / %s · %s",
			humanBytes(s.Downloaded), humanBytes(s.Total), formatDuration(time.Since(m.start))))
	}

	pct := stPct.Render(fmt.Sprintf("%3d%%", int(math.Round(ratio*100))))
	meta := "ETA " + formatDuration(s.ETA) + " · " + stSpeed.Render(fmt.Sprintf("%.1f MB/s", s.Speed/(1<<20)))
	barW := max(width-lipgloss.Width(pct)-lipgloss.Width(meta)-2, 1)
	m.bar.Width = barW
	line := pct + " " + m.bar.ViewAs(ratio) + " " + meta

	return strings.Join([]string{
		"", m.header(), info, "", status, line, "", stMuted.Render("q quit"), "",
	}, "\n")
}

func (m model) doneView() string {
	if m.err != nil {
		return m.errorView()
	}
	total := m.snap.Total
	elapsed := time.Since(m.start)
	avg := 0.0
	if s := elapsed.Seconds(); s > 0 {
		avg = float64(total) / s
	}

	lines := []string{
		stOK.Render("download complete"), "",
		stMuted.Render("file   ") + filepath.Base(m.cfg.Output),
		stMuted.Render("size   ") + humanBytes(total),
		stMuted.Render("time   ") + formatDuration(elapsed),
		stMuted.Render("speed  ") + fmt.Sprintf("%.1f MB/s", avg/(1<<20)),
	}
	if m.logFile != "" {
		lines = append(lines, stMuted.Render("log    ")+m.logFile)
	}
	return strings.Join(append(lines, ""), "\n")
}

func (m model) errorView() string {
	return strings.Join([]string{
		stErr.Render("✗ download failed"), "",
		stMuted.Render(m.err.Error()), "",
	}, "\n")
}
