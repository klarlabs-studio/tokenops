package accounts

import (
	"regexp"
	"sync"
)

// lazyRegexp compiles pattern on first use. This package holds a parser
// for every provider TokenOps reads, and every tokenops process links it,
// including the hooks that run once per tool call; compiling all of them
// at start cost about 2.5 ms each time.
func lazyRegexp(pattern string) func() *regexp.Regexp {
	return sync.OnceValue(func() *regexp.Regexp { return regexp.MustCompile(pattern) })
}
