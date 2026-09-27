package cli

import (
	"fmt"
	"io"
	"os"
	"sync"
	"time"

	"golang.org/x/term"
)

// activity gives long CLI operations one consistent presentation. Interactive
// terminals get a single animated line; redirected output remains stable and
// line-oriented for logs, tests, and shell pipelines.
type activity struct {
	out         io.Writer
	label       string
	interactive bool
	done        chan struct{}
	wg          sync.WaitGroup
	once        sync.Once
}

func startActivity(out io.Writer, label string) *activity {
	interactive := false
	if f, ok := out.(*os.File); ok {
		interactive = term.IsTerminal(int(f.Fd()))
	}
	return startActivityMode(out, label, interactive)
}

func startActivityMode(out io.Writer, label string, interactive bool) *activity {
	a := &activity{out: out, label: label, interactive: interactive, done: make(chan struct{})}
	if !interactive {
		fmt.Fprintf(out, "%s...\n", label)
		return a
	}

	frames := []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}
	fmt.Fprintf(out, "\r\x1b[2K%s %s", frames[0], label)
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ticker := time.NewTicker(80 * time.Millisecond)
		defer ticker.Stop()
		i := 1
		for {
			select {
			case <-a.done:
				return
			case <-ticker.C:
				fmt.Fprintf(out, "\r\x1b[2K%s %s", frames[i%len(frames)], label)
				i++
			}
		}
	}()
	return a
}

func (a *activity) finish(symbol, message string) {
	if a == nil {
		return
	}
	a.once.Do(func() {
		close(a.done)
		a.wg.Wait()
		if a.interactive {
			fmt.Fprintf(a.out, "\r\x1b[2K%s %s\n", symbol, message)
			return
		}
		fmt.Fprintf(a.out, "%s %s\n", symbol, message)
	})
}

func (a *activity) success(message string) { a.finish("✓", message) }
func (a *activity) failure(message string) { a.finish("✗", message) }
