package ui

import (
	"fmt"

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

// snapshot of the current state of the app
type Model struct {
	filename   string
	totalSize  int64
	downloaded int64
	progress   progress.Model
	status     string
	err        error
}

func InitialModel(filename string, total int64) Model {
	p := progress.New(progress.WithDefaultGradient())
	return Model{
		filename:   filename,
		totalSize:  total,
		downloaded: 0,
		progress:   p,
		status:     "downloading",
		err:        nil,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
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
	case DoneMsg:
		m.status = "done"
		return m, tea.Quit
	case ErrorMsg:
		m.err = msg.Err
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
		return fmt.Sprintf("Downloaded %s (%d bytes)\n", m.filename, m.downloaded)
	}

	return fmt.Sprintf(
		"Downloading %s\n\n%s\n\n%d / %d bytes\nPress q to quit\n",
		m.filename,
		m.progress.View(),
		m.downloaded,
		m.totalSize,
	)
}
