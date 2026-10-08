//go:build !darwin && !linux

package applogin

import (
	"context"
	"errors"
)

// listProcesses is not supported here: no process sign-in is read.
func listProcesses(context.Context) ([]Process, error) {
	return nil, errors.New("reading another process's command line is not supported on this system")
}
