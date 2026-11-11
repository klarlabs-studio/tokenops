// Package heartbeat keeps the reading of a scheduled reader current.
//
// A reader that polls a vendor on a schedule stores a reading only when it
// differs from the last one, so an account nobody is using stores nothing
// for hours. Whoever reads the store then cannot tell a reader that works
// from one that has stopped: both leave an old reading behind.
//
// Two things settle it. A reader stores an unchanged reading again once
// Every has passed (Keeper), and writes on each reading it stores for how
// long that reading stands (Stamp), which it knows from its own schedule.
// A reading older than it says it stands for is one its reader has stopped
// replacing.
package heartbeat

import (
	"math"
	"strconv"
	"sync"
	"time"
)

// Every is how often an unchanged reading is stored again.
const Every = 10 * time.Minute

// Attr is the attribute a stored reading carries to say for how many
// minutes it stands. A reading without it makes no such promise: it comes
// from a reader that reads as work happens, or was stored before readings
// said.
const Attr = "fresh_for_min"

// missedPolls is how many polls in a row may fail before the last reading
// is read as stale.
const missedPolls = 2

// GoodFor is how long a reading stands when its reader polls every
// interval. Two stored readings are at most Every and one poll apart: an
// unchanged reading is not due just before Every has passed, and is stored
// one poll later. missedPolls more polls may fail before the reading is
// stale.
func GoodFor(interval time.Duration) time.Duration {
	interval = max(interval, 0)
	return Every + (1+missedPolls)*interval
}

// Stamp writes on a reading for how long it stands, for a reader that
// polls every interval. Part of a minute counts as a whole one.
func Stamp(attrs map[string]string, interval time.Duration) {
	attrs[Attr] = strconv.Itoa(int(math.Ceil(GoodFor(interval).Minutes())))
}

// FreshFor reads for how long a stored reading stands. ok is false when
// the reading does not say.
func FreshFor(attrs map[string]string) (time.Duration, bool) {
	minutes, err := strconv.Atoi(attrs[Attr])
	if err != nil || minutes <= 0 {
		return 0, false
	}
	return time.Duration(minutes) * time.Minute, true
}

// Keeper decides whether a reading is to be stored: when it differs from
// the last one stored, and when that one was stored Every ago. Its zero
// value is ready to use.
type Keeper struct {
	mu  sync.Mutex
	key string
	at  time.Time
	any bool
}

// Due reports whether a reading whose content is key is to be stored at
// now. A clock set back since the last store is no reason to hold a
// reading back.
func (k *Keeper) Due(key string, now time.Time) bool {
	k.mu.Lock()
	defer k.mu.Unlock()
	if !k.any || key != k.key {
		return true
	}
	since := now.Sub(k.at)
	return since >= Every || since < 0
}

// Stored records that the reading whose content is key was stored at now.
// A reader calls it once the store has taken the reading, and not before:
// a reading marked stored that was not is never stored until it changes.
func (k *Keeper) Stored(key string, now time.Time) {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.key, k.at, k.any = key, now, true
}
