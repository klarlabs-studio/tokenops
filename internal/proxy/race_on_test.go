//go:build race

package proxy

// raceEnabled reports that the tests run under the race detector, which
// slows every instrumented memory access and so distorts latency.
const raceEnabled = true
