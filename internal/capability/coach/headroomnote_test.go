package coach

import (
	"testing"

	"go.klarlabs.de/tokenops/internal/config"
)

func TestHeadroomNoteFollowsItsSettingAndTheInformPower(t *testing.T) {
	off := false
	for name, tc := range map[string]struct {
		cfg  config.Config
		want bool
	}{
		"default on": {config.Config{}, true},
		"turned off": {config.Config{Coach: config.CoachConfig{HeadroomNote: &off}}, false},
		"inform off": {config.Config{Coach: config.CoachConfig{Powers: map[string]string{"inform": "off"}}}, false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := Build(tc.cfg).HeadroomNote; got != tc.want {
				t.Errorf("HeadroomNote = %v, want %v", got, tc.want)
			}
		})
	}
	for in, want := range map[string]bool{"on": true, "off": false, "true": true, "no": false} {
		got, err := ParseHeadroomNote(in)
		if err != nil || got != want {
			t.Errorf("ParseHeadroomNote(%q) = %v, %v; want %v", in, got, err, want)
		}
	}
	if _, err := ParseHeadroomNote("sometimes"); err == nil {
		t.Error("accepted a value that is neither on nor off")
	}
}
