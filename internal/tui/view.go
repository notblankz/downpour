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
	stWarn  = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
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
		"", m.header(), info, "", status, line, "", m.workers(width), stMuted.Render("q quit"), "",
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

func (m model) workers(width int) string {
	ws := m.snap.Workers
	if len(ws) == 0 {
		return ""
	}

	var maxSpeed float64
	for _, w := range ws {
		if w.Speed > maxSpeed {
			maxSpeed = w.Speed
		}
	}

	const barW = 8
	cells := make([]string, len(ws))
	for i, w := range ws {
		fill := 0
		st := stSpeed
		if maxSpeed > 0 {
			fill = int(math.Round(w.Speed / maxSpeed * barW))
			if w.Speed < 0.5*maxSpeed {
				st = stWarn
			}
		}
		bar := st.Render(strings.Repeat("█", fill)) + stMuted.Render(strings.Repeat("░", barW-fill))
		cells[i] = stMuted.Render(fmt.Sprintf("w%02d", w.ID)) + " " + bar + " " +
			st.Render(fmt.Sprintf("%4.1f MB/s", w.Speed/(1<<20)))
	}

	const cellW, gap = 22, 3
	cols := max(1, (width+gap)/(cellW+gap))
	if cols > len(cells) {
		cols = len(cells)
	}

	var b strings.Builder
	b.WriteString(stMuted.Render("workers") + "\n")
	for i, c := range cells {
		if i > 0 {
			if i%cols == 0 {
				b.WriteByte('\n')
			} else {
				b.WriteString(strings.Repeat(" ", gap))
			}
		}
		// pad every cell except the last one in its row (no trailing padding)
		if (i%cols != cols-1) && i != len(cells)-1 {
			if g := cellW - lipgloss.Width(c); g > 0 {
				c += strings.Repeat(" ", g)
			}
		}
		b.WriteString(c)
	}
	return b.String()
}

func (m model) errorView() string {
	return strings.Join([]string{
		stErr.Render("✗ download failed"), "",
		stMuted.Render(m.err.Error()), "",
	}, "\n")
}
