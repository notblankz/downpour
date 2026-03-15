package utils

import (
	"fmt"
	"math"
	"time"
)

func ScaleValue(b float64) (float64, string) {
	const unit = 1024
	if b < unit {
		return b, ""
	}

	exp := int(math.Log(b) / math.Log(unit))

	prefixes := "KMGTPE"

	if exp > len(prefixes) {
		exp = len(prefixes)
	}

	scaledValue := b / math.Pow(unit, float64(exp))

	return scaledValue, string(prefixes[exp-1])
}

func FormatSpeedString(toScaleValue float64, prefixString string) string {
	scaledValue, scaledPrefix := ScaleValue(toScaleValue)
	return fmt.Sprintf("%.2f%s%s", scaledValue, scaledPrefix, prefixString)
}

func FormatDuration(d time.Duration) string {
	d = d.Round(time.Second)
	h := d / time.Hour
	d -= h * time.Hour
	m := d / time.Minute
	d -= m * time.Minute
	s := d / time.Second
	if h > 0 {
		return fmt.Sprintf("%dh %dm %ds", h, m, s)
	}
	if m > 0 {
		return fmt.Sprintf("%dm %ds", m, s)
	}
	return fmt.Sprintf("%ds", s)
}
