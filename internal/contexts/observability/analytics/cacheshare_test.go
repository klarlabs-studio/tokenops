package analytics

import "testing"

func TestCacheReadShare(t *testing.T) {
	tests := []struct {
		name             string
		input, cacheRead int64
		want             float64
		absent           bool
	}{
		{name: "no input", input: 0, cacheRead: 0, absent: true},
		{name: "no input with a stray cache read", input: 0, cacheRead: 10, absent: true},
		{name: "nothing cached", input: 1000, cacheRead: 0, want: 0},
		{name: "all cached", input: 1000, cacheRead: 1000, want: 1},
		{name: "mixed", input: 1000, cacheRead: 750, want: 0.75},
		{name: "cache read above input held at one", input: 100, cacheRead: 150, want: 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := CacheReadShare(tt.input, tt.cacheRead)
			if tt.absent {
				if got != nil {
					t.Fatalf("share = %v, want absent", *got)
				}
				return
			}
			if got == nil || *got != tt.want {
				t.Fatalf("share = %v, want %v", got, tt.want)
			}
		})
	}
}
