package sqliteuri

import "testing"

func TestFile(t *testing.T) {
	tests := []struct{ path, query, want string }{
		{"/a/events.db", "mode=ro", "file:/a/events.db?mode=ro"},
		{"/a/x#1.db", "mode=ro", "file:/a/x%231.db?mode=ro"},
		{"/a/x?y.db", "", "file:/a/x%3fy.db"},
		{"/a/100%.db", "mode=ro", "file:/a/100%25.db?mode=ro"},
		{"/a/d e.db", "mode=ro", "file:/a/d e.db?mode=ro"},
	}
	for _, tt := range tests {
		if got := File(tt.path, tt.query); got != tt.want {
			t.Errorf("File(%q, %q) = %q, want %q", tt.path, tt.query, got, tt.want)
		}
	}
}
