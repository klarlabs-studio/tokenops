package headroom

import "time"

// SetGlanceAdviceBudget sets the glance's budget for a test and returns a
// function that restores it.
func SetGlanceAdviceBudget(d time.Duration) func() {
	old := glanceAdviceBudget
	glanceAdviceBudget = d
	return func() { glanceAdviceBudget = old }
}

// WaitAdviceRefresh waits for c's background refresh, if any.
func WaitAdviceRefresh(c *AdviceCache) {
	c.mu.Lock()
	done := c.refreshing
	c.mu.Unlock()
	if done != nil {
		<-done
	}
}
