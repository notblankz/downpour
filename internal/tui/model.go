package tui

import (
	"context"
	"time"

	"github.com/charmbracelet/bubbles/progress"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/notblankz/downpour/internal/engine"
)

type model struct {
	ctx     context.Context
	cancel  context.CancelFunc
	sup     *engine.Supervisor
	cfg     engine.Config
	bar     progress.Model
	width   int
	snap    *engine.Snapshot
	start   time.Time
	done    bool
	err     error
	logFile string
}

type tickMsg time.Time
type doneMsg struct{ err error }

// Run drives the download under a Bubbletea program and returns the download's
// own error (not the TUI's)
func Run(parent context.Context, sup *engine.Supervisor, cfg engine.Config, logFile string) error {
	ctx, cancel := context.WithCancel(parent)
	defer cancel()

	m := model{
		ctx:     ctx,
		cancel:  cancel,
		sup:     sup,
		cfg:     cfg,
		bar:     progress.New(progress.WithSolidFill("2"), progress.WithoutPercentage()),
		snap:    sup.Snapshot(),
		start:   time.Now(),
		logFile: logFile,
	}

	res, err := tea.NewProgram(m).Run()
	if err != nil {
		return err
	}

	if fm, ok := res.(model); ok {
		return fm.err
	}

	return nil
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.runDownload(), tickCmd())
}

func (m model) runDownload() tea.Cmd {
	return func() tea.Msg {
		return doneMsg{
			err: m.sup.Run(m.ctx),
		}
	}
}

func tickCmd() tea.Cmd {
	return tea.Tick(250*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c", "esc":
			return m, tea.Quit
		}
	case tickMsg:
		if m.done {
			return m, nil
		}
		m.snap = m.sup.Snapshot()
		return m, tickCmd()
	case doneMsg:
		m.done, m.err = true, msg.err
		m.snap = m.sup.Snapshot()
		return m, tea.Quit
	}
	return m, nil
}
