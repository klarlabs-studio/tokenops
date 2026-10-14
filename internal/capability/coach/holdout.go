package coach

import (
	"fmt"
	"strconv"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
)

// ParseHoldout reads a holdout setting as an operator writes it: off, on
// (the default share), or a percent from 0 to 10.
func ParseHoldout(s string) (int, error) {
	v := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(s)), "%")
	switch v {
	case "off":
		return 0, nil
	case "on":
		return config.DefaultHoldoutPercent, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 || n > config.MaxHoldoutPercent {
		return 0, fmt.Errorf("holdout must be off, on, or a percent from 0 to %d, got %q", config.MaxHoldoutPercent, s)
	}
	return n, nil
}
