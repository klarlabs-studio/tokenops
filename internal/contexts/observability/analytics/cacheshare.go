package analytics

// CacheReadShare is the share of input that was read from the prompt
// cache, between 0 and 1: cacheRead of input, where input already
// includes the cache reads and writes (as Row and SessionTurn count it,
// and so input + cache read + cache write as a provider bills it). It is
// nil when there was no input, so "no input" never reads as "nothing
// cached". A cache read above the input it is part of is bad data and is
// held at 1.
func CacheReadShare(input, cacheRead int64) *float64 {
	if input <= 0 {
		return nil
	}
	s := float64(max(cacheRead, 0)) / float64(input)
	s = min(s, 1)
	return &s
}
