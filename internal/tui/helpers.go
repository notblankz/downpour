package tui

import (
	"fmt"
	"net/url"
	"time"
)

// humanBytes formats a byte count as a human readable size (KiB/MiB/GiB)
func humanBytes(b int64) string {
	const kib, mib, gib = 1 << 10, 1 << 20, 1 << 30
	switch {
	case b >= gib:
		return fmt.Sprintf("%.2f GiB", float64(b)/gib)
	case b >= mib:
		return fmt.Sprintf("%.0f MiB", float64(b)/mib)
	case b >= kib:
		return fmt.Sprintf("%.0f KiB", float64(b)/kib)
	default:
		return fmt.Sprintf("%d B", b)
	}
}

// formatDuration rounds d to the nearest seconds and formats it as "26s" or "1m06s"
func formatDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	d = d.Round(time.Second)
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
}

// hostOf returns the host porition of a URL, or the raw string if it has none
func hostOf(raw string) string {
	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}
	return raw
}
