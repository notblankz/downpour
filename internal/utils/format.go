package utils

import (
	"fmt"
	"math"
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
