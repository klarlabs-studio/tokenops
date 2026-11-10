package claudecodejsonl

import "testing"

func TestMessagesCountsAMessageAtItsFirstLine(t *testing.T) {
	seen := Messages{}
	steps := []struct {
		id   string
		want bool
	}{
		{"msg_a", true},
		{"msg_a", false},
		{"msg_b", true},
		{"msg_a", false},
		{"", true},
		{"", true},
	}
	for i, s := range steps {
		if got := seen.First(s.id); got != s.want {
			t.Errorf("step %d: First(%q) = %v, want %v", i, s.id, got, s.want)
		}
	}
}
