//go:build darwin

package applogin

import (
	"encoding/binary"
	"slices"
	"testing"
)

func TestProcArgs(t *testing.T) {
	raw := binary.LittleEndian.AppendUint32(nil, 3)
	raw = append(raw, "/bin/ls\x00\x00\x00\x00ls\x00--csrf_token\x00abc\x00HOME=/x\x00"...)
	if got := procArgs(raw); !slices.Equal(got, []string{"ls", "--csrf_token", "abc"}) {
		t.Errorf("%q", got)
	}
	for _, bad := range [][]byte{nil, {1, 0}, binary.LittleEndian.AppendUint32(nil, 2)} {
		if got := procArgs(bad); len(got) != 0 {
			t.Errorf("%v: %q", bad, got)
		}
	}
}
