package ui

import (
	"downpour/internal/downloader"
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

// snapshot of the current state of the app
type Model struct {
	filename    string
	totalSize   int64
	acceptRange bool
	rdi         *downloader.RangeDownloadInfo
	downloaded  int64
	progress    progress.Model
	status      string
	err         error
	startTime   time.Time
	elapsed     time.Duration
}

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
	return tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg {
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
		nextTick := tea.Tick(100*time.Millisecond, func(t time.Time) tea.Msg { return TickMsg{} })
		m.downloaded = m.rdi.BytesWritten.Load()
		if m.totalSize > 0 {
			percent := float64(m.downloaded) / float64(m.totalSize)
			return m, tea.Batch(m.progress.SetPercent(percent), nextTick)
		}
		return m, nextTick
	case DoneMsg:
		m.status = "done"
		m.elapsed = time.Since(m.startTime)
		return m, tea.Quit
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
		return fmt.Sprintf("Error: %v\n", m.err)
	}

	if m.status == "done" {
		return fmt.Sprintf("Downloaded %s (%d bytes in %vs)\n", m.filename, m.downloaded, (m.elapsed.Seconds()))
	}

	if m.status == "downloading" {
		if m.acceptRange {
			return fmt.Sprintf(
				"Downloading (Accepts Ranges) %s\n\n%s\n\n%d / %d bytes\nPress q to quit\n",
				m.filename,
				m.progress.View(),
				m.downloaded,
				m.totalSize,
			)
		}
	}

	return fmt.Sprintf(
		"Downloading (Only Streaming) %s\n\n%s\n\n%d / %d bytes\nPress q to quit\n",
		m.filename,
		m.progress.View(),
		m.downloaded,
		m.totalSize,
	)
}
