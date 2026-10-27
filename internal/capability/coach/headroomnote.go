package coach

import (
	"fmt"
	"strings"
)

// ParseHeadroomNote reads the headroom note's setting: on or off.
func ParseHeadroomNote(s string) (bool, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "on", "true", "yes":
		return true, nil
	case "off", "false", "no":
		return false, nil
	}
	return false, fmt.Errorf("headroom note must be on or off, got %q", s)
}
