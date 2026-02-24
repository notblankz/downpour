package ui

import (
	"downpour/internal/downloader"
	"downpour/internal/utils"
	"fmt"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
)

type ProgressMsg struct {
	Bytes int
}

type DoneMsg struct{}

type ErrorMsg struct {
	Err error
}

type TickMsg struct{}

type VerifyingMsg struct{}

// snapshot of the current state of the app
type Model struct {
	filename       string
	totalSize      int64
	acceptRange    bool
	rdi            *downloader.RangeDownloadInfo
	downloaded     int64
	progress       progress.Model
	status         string
	err            error
	startTime      time.Time
	elapsed        time.Duration
	lastDownloaded int64
	currentSpeed   float64
}

const asciiLogo = `
    ____
   / __ \____ _      ______  ____  ____  __  _______
  / / / / __ \ | /| / / __ \/ __ \/ __ \/ / / / ___/
 / /_/ / /_/ / |/ |/ / / / / /_/ / /_/ / /_/ / /
/_____/\____/|__/|__/_/ /_/ .___/\____/\__,_/_/
                         /_/
`

func InitialModel(filename string, total int64, acceptRange bool, rdi *downloader.RangeDownloadInfo) Model {
	p := progress.New(progress.WithDefaultGradient())
	return Model{
		filename:    filename,
		totalSize:   total,
		acceptRange: acceptRange,
		rdi:         rdi,
		downloaded:  0,
		progress:    p,
		status:      "downloading",
		err:         nil,
		startTime:   time.Now(),
	}
}

func (m Model) Init() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return TickMsg{}
	})
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "q":
			return m, tea.Quit
		}
	case ProgressMsg:
		m.downloaded += int64(msg.Bytes)
		if m.totalSize > 0 {
			percent := float64(m.downloaded) / float64(m.totalSize)
			cmd := m.progress.SetPercent(percent)
			return m, cmd
		}
		return m, nil
	case TickMsg:
		if m.rdi == nil || m.status == "error" {
			return m, nil
		}
		currentTotal := m.rdi.BytesWritten.Load()
		delta := currentTotal - m.lastDownloaded
		instantSpeed := float64(delta) * 2
		m.currentSpeed = (0.7 * m.currentSpeed) + (0.3 * instantSpeed)
		m.downloaded = currentTotal
		m.lastDownloaded = currentTotal
		nextTick := tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return TickMsg{} })
		if m.totalSize > 0 {
			percent := float64(m.downloaded) / float64(m.totalSize)
			return m, tea.Batch(m.progress.SetPercent(percent), nextTick)
		}
		return m, nextTick
	case DoneMsg:
		m.status = "done"
		m.elapsed = time.Since(m.startTime)
		return m, tea.Quit
	case VerifyingMsg:
		m.status = "verifying"
		return m, nil
	case ErrorMsg:
		m.err = msg.Err
		m.status = "error"
		return m, tea.Quit
	}

	var cmd tea.Cmd
	var prog tea.Model

	prog, cmd = m.progress.Update(msg)
	m.progress = prog.(progress.Model)
	return m, cmd
}

func (m Model) View() string {
	if m.status == "error" {
		return fmt.Sprintf("\nFatal Error: %v\n\n  Press 'q' to quit", m.err)
	}

	format := func(val float64, suffix string) string {
		v, p := utils.ScaleValue(val)
		return fmt.Sprintf("%.2f %s%s", v, p, suffix)
	}

	if m.status == "done" {
		avgSpeed := float64(m.downloaded) / m.elapsed.Seconds()

		filenameDisplay := m.filename
		if m.rdi != nil && m.rdi.Checksum != nil {
			filenameDisplay = fmt.Sprintf("%s (%s checksum verified)", m.filename, m.rdi.Checksum.AlgoName)
		}

		return fmt.Sprintf(
			"%s\nDownload Complete!\n\n    File: %s\n    Size: %s\n    Time: %.2fs\n    Average Speed: %s\n\n  Press 'q' to exit",
			asciiLogo,
			filenameDisplay,
			format(float64(m.downloaded), "B"),
			m.elapsed.Seconds(),
			format(avgSpeed, "B/s"),
		)
	}

	if m.status == "verifying" {
		return fmt.Sprintf(
			"%s\nDownload Complete\n\nChecking Checksum of %s...\nPlease wait",
			asciiLogo,
			m.filename,
		)
	}

	header := "Streaming"
	if m.acceptRange {
		header = "Parallel Multi-Worker"
	}

	speedStr := format(m.currentSpeed, "B/s")
	var etdStr string
	if m.currentSpeed > 0 && m.totalSize > 0 {
		remaining := float64(m.totalSize - m.downloaded)
		seconds := remaining / m.currentSpeed
		etdStr = fmt.Sprintf("ETD: %s", (time.Duration(seconds) * time.Second).Round(time.Second))
	} else {
		etdStr = "ETD: Calculating..."
	}

	return fmt.Sprintf(
		"%s\nDownloading (%s)\nFile: %s\n\n%s\n\nSpeed: %s | %s\nDownloaded: %s / %s\n\nPress 'q' to quit",
		asciiLogo,
		header,
		m.filename,
		m.progress.View(),
		speedStr,
		etdStr,
		format(float64(m.downloaded), "B"),
		format(float64(m.totalSize), "B"),
	)
}
