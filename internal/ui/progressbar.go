package ui

import (
	"downpour/internal/downloader"
	"downpour/internal/utils"
	"fmt"
	"strings"
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
		// Global Updates
		currentTotal := m.rdi.BytesWritten.Load()
		delta := currentTotal - m.lastDownloaded
		instantSpeed := float64(delta) * 2
		m.currentSpeed = (0.6 * m.currentSpeed) + (0.4 * instantSpeed)
		m.downloaded = currentTotal
		m.lastDownloaded = currentTotal

		// Per worker updates
		for _, worker := range m.rdi.Workers.Slice {
			worker.UpdateSpeed()
		}
		nextTick := tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg { return TickMsg{} })

		// Update Global progress bar percentage
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

	if m.status == "done" {
		avgSpeed := float64(m.downloaded) / m.elapsed.Seconds()

		filenameDisplay := m.filename
		if m.rdi != nil && m.rdi.Checksum != nil {
			filenameDisplay = fmt.Sprintf("%s (%s checksum verified)", m.filename, m.rdi.Checksum.AlgoName)
		}

		return fmt.Sprintf(
			"%s\nDownload Complete!\n\n    Filename: %s\n    Downloaded: %s (Filesize: %s)\n    Time: %.2fs\n    Average Speed: %s\n\n  Press 'q' to exit",
			asciiLogo,
			filenameDisplay,
			formatBytes(float64(m.rdi.BytesWritten.Load()), "B"),
			formatBytes(float64(m.rdi.TotalSize), "B"),
			m.elapsed.Seconds(),
			formatBytes(avgSpeed, "B/s"),
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

	speedStr := formatBytes(m.currentSpeed, "B/s")
	var etdStr string
	if m.currentSpeed > 0 && m.totalSize > 0 {
		remaining := float64(m.totalSize - m.downloaded)
		seconds := remaining / m.currentSpeed
		etdStr = fmt.Sprintf("ETA: %s", (time.Duration(seconds) * time.Second).Round(time.Second))
	} else {
		etdStr = "ETA: Calculating..."
	}

	return fmt.Sprintf(
		"%s\nFile: %s\nMode: %s\n\nProgress: %s\n\nSize: %-30s\nSpeed: %-29s%s\n\nIndividual Worker Speeds:%s\n\nPress 'q' to quit",
		asciiLogo,
		m.filename,
		header,
		m.progress.View(),
		fmt.Sprintf("%s / %s", formatBytes(float64(m.downloaded), "B"), formatBytes(float64(m.totalSize), "B")),
		speedStr,
		etdStr,
		func() string {
			if !m.acceptRange || m.rdi == nil {
				return " N/A (Streaming)"
			}
			return formatWorkerGrid(m.rdi)
		}(),
	)
}

func formatWorker(workerInfo *downloader.WorkerInfo) string {
	return fmt.Sprintf("W%d - %8s [chunk %5s]", workerInfo.ID, formatBytes(workerInfo.Speed, "B/s"), fmt.Sprintf("#%d", workerInfo.Chunk.Index))
}

func formatWorkerGrid(rdi *downloader.RangeDownloadInfo) string {
	rows := len(rdi.Workers.Slice) / 2
	var sb strings.Builder
	for i := 0; i < (rows * 2); i += 2 {
		fmt.Fprintf(&sb, "\n")
		firstWorkerStr := formatWorker(rdi.Workers.Slice[i])
		secondWorkerStr := formatWorker(rdi.Workers.Slice[i+1])
		fmt.Fprintf(&sb, "%-36s%s", firstWorkerStr, secondWorkerStr)
	}

	return sb.String()
}

func formatBytes(val float64, suffix string) string {
	v, p := utils.ScaleValue(val)
	return fmt.Sprintf("%.2f %s%s", v, p, suffix)
}
