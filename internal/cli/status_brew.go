package cli

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// brewExtractMarker names the Homebrew child process that hangs on macOS
// 27.0 (Homebrew/brew#24235): the sandboxed extract step of a download.
const brewExtractMarker = "sandbox_operation.rb extract"

// brewStuckAfter is how long an extract must have run before status says it
// is stuck. A healthy extract of a bottle finishes in seconds.
const brewStuckAfter = 2 * time.Minute

// brewStuckMessage is what status shows for a stuck extract. Status only
// reports it; nothing is killed.
const brewStuckMessage = "Homebrew is stuck unpacking a download (a macOS 27.0 bug, Homebrew/brew#24235); " +
	"updating macOS or restarting the Mac clears it."

// brewProcess is one process from the process table: its command line and
// how long it has been running.
type brewProcess struct {
	Command string
	Elapsed time.Duration
}

// processLister returns the processes status inspects. Production uses
// listProcesses; tests inject a fixed table.
type processLister func(ctx context.Context) ([]brewProcess, error)

// brewStuckWarnings returns the status warning for a Homebrew extract that
// has run past brewStuckAfter, or nothing. A failed listing degrades to no
// warning: status must not fail because the process table could not be read.
func brewStuckWarnings(ctx context.Context, list processLister) []string {
	procs, err := list(ctx)
	if err != nil {
		return nil
	}
	for _, p := range procs {
		if strings.Contains(p.Command, brewExtractMarker) && p.Elapsed > brewStuckAfter {
			return []string{brewStuckMessage}
		}
	}
	return nil
}

// listProcesses reads the process table with ps. It is read-only: it lists
// processes and never signals one.
func listProcesses(ctx context.Context) ([]brewProcess, error) {
	out, err := exec.CommandContext(ctx, "ps", "-axww", "-o", "etime=", "-o", "command=").Output()
	if err != nil {
		return nil, fmt.Errorf("ps: %w", err)
	}
	var procs []brewProcess
	for _, line := range strings.Split(string(out), "\n") {
		if p, ok := parsePsLine(line); ok {
			procs = append(procs, p)
		}
	}
	return procs, nil
}

// parsePsLine reads one line of `ps -o etime= -o command=`: the elapsed time,
// then the command line.
func parsePsLine(line string) (brewProcess, bool) {
	line = strings.TrimSpace(line)
	etime, command, found := strings.Cut(line, " ")
	if !found {
		return brewProcess{}, false
	}
	elapsed, err := parseEtime(etime)
	if err != nil {
		return brewProcess{}, false
	}
	return brewProcess{Command: strings.TrimSpace(command), Elapsed: elapsed}, true
}

// parseEtime reads ps's elapsed time, [[dd-]hh:]mm:ss.
func parseEtime(s string) (time.Duration, error) {
	var days int
	if d, rest, ok := strings.Cut(s, "-"); ok {
		n, err := strconv.Atoi(d)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("etime %q: bad days", s)
		}
		days, s = n, rest
	}
	parts := strings.Split(s, ":")
	if len(parts) == 2 {
		parts = append([]string{"0"}, parts...)
	}
	if len(parts) != 3 {
		return 0, fmt.Errorf("etime %q: want [[dd-]hh:]mm:ss", s)
	}
	var fields [3]int
	for i, p := range parts {
		n, err := strconv.Atoi(p)
		if err != nil || n < 0 {
			return 0, fmt.Errorf("etime %q: bad field %q", s, p)
		}
		fields[i] = n
	}
	secs := days*86400 + fields[0]*3600 + fields[1]*60 + fields[2]
	return time.Duration(secs) * time.Second, nil
}
