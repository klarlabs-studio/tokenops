package providers

import (
	"sort"
	"strings"
)

// maxSuggestions bounds the ids Resolve offers for an unknown one.
const maxSuggestions = 5

// Resolve finds the provider an operator named: its id or its name,
// ignoring case, spaces, dashes and underscores. When nothing matches,
// suggestions are the closest ids, nearest first, at most five.
func Resolve(name string) (id string, ok bool, suggestions []string) {
	key := normName(name)
	if key == "" {
		return "", false, nil
	}
	// An id wins over another provider's name.
	for _, d := range all {
		if normName(string(d.ID)) == key {
			return string(d.ID), true, nil
		}
	}
	for _, d := range all {
		if normName(d.DisplayName) == key || normName(d.Label()) == key {
			return string(d.ID), true, nil
		}
	}
	type near struct {
		id   string
		dist int
	}
	var close []near
	for _, d := range all {
		id := string(d.ID)
		dist := min(editDistance(key, normName(id)), editDistance(key, normName(d.Label())))
		if dist <= 2 || (len(key) >= 4 && strings.Contains(normName(id), key)) {
			close = append(close, near{id, dist})
		}
	}
	sort.SliceStable(close, func(i, j int) bool {
		if close[i].dist != close[j].dist {
			return close[i].dist < close[j].dist
		}
		return close[i].id < close[j].id
	})
	for i, n := range close {
		if i == maxSuggestions {
			break
		}
		suggestions = append(suggestions, n.id)
	}
	return "", false, suggestions
}

func normName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	return strings.NewReplacer("-", "", "_", "", " ", "", ".", "").Replace(s)
}

// editDistance is the Levenshtein distance between a and b.
func editDistance(a, b string) int {
	prev := make([]int, len(b)+1)
	cur := make([]int, len(b)+1)
	for j := range prev {
		prev[j] = j
	}
	for i := 1; i <= len(a); i++ {
		cur[0] = i
		for j := 1; j <= len(b); j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			cur[j] = min(prev[j]+1, cur[j-1]+1, prev[j-1]+cost)
		}
		prev, cur = cur, prev
	}
	return prev[len(b)]
}
