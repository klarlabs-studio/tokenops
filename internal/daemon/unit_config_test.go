package daemon

import (
	"slices"
	"testing"
)

// A unit says which config its daemon reads, and what is rendered into it
// reads back as it went in, whatever the path holds.
func TestUnitConfigPathOfARenderedUnit(t *testing.T) {
	const awkward = `/home/ada/My Configs/50% "odd" $HOME\x & <y>'z'.yaml`
	tests := []struct {
		name string
		spec UnitSpec
		want string
	}{
		{"nothing pinned: the default of the unit's home",
			UnitSpec{}, "/home/ada/.config/tokenops/config.yaml"},
		{"a config directory",
			UnitSpec{ConfigHome: "/data/cfg"}, "/data/cfg/tokenops/config.yaml"},
		{"a config file",
			UnitSpec{Config: "/etc/tokenops/work.yaml"}, "/etc/tokenops/work.yaml"},
		{"a config file wins over a config directory",
			UnitSpec{Config: "/etc/tokenops/work.yaml", ConfigHome: "/data/cfg"}, "/etc/tokenops/work.yaml"},
		{"a config file with every awkward character",
			UnitSpec{Config: awkward}, awkward},
		{"a config directory with every awkward character",
			UnitSpec{ConfigHome: `/data/50% "odd" $HOME\x & <y>'z'`}, `/data/50% "odd" $HOME\x & <y>'z'/tokenops/config.yaml`},
	}
	for _, kind := range []UnitKind{UnitLaunchd, UnitSystemd} {
		for _, tt := range tests {
			t.Run(string(kind)+": "+tt.name, func(t *testing.T) {
				spec := tt.spec
				spec.Bin, spec.Home = "/usr/bin/tokenops", "/home/ada"
				body, err := RenderUnit(kind, spec)
				if err != nil {
					t.Fatalf("render: %v", err)
				}
				if got := UnitConfigPath(kind, body); got != tt.want {
					t.Errorf("UnitConfigPath = %q, want %q\n%s", got, tt.want, body)
				}
			})
		}
	}
}

// Units nobody rendered here: written by hand, by an older version, or not
// units at all.
func TestUnitConfigPathOfOtherUnits(t *testing.T) {
	tests := []struct {
		name string
		kind UnitKind
		body string
		want string
	}{
		{"systemd, --config=", UnitSystemd,
			"[Service]\nExecStart=/usr/bin/tokenops start --config=/etc/t.yaml\nEnvironment=HOME=/home/ada\n", "/etc/t.yaml"},
		{"systemd, -c", UnitSystemd,
			"[Service]\nExecStart=/usr/bin/tokenops -c '/etc/my t.yaml' start\n", "/etc/my t.yaml"},
		{"systemd, several variables on a line", UnitSystemd,
			"[Service]\nExecStart=/usr/bin/tokenops start\nEnvironment=HOME=/home/ada \"XDG_CONFIG_HOME=/data/my cfg\"\n",
			"/data/my cfg/tokenops/config.yaml"},
		{"systemd, no environment", UnitSystemd, "[Service]\nExecStart=/usr/bin/tokenops start\n", ""},
		{"launchd, no environment", UnitLaunchd,
			"<plist><dict><key>ProgramArguments</key><array><string>/usr/bin/tokenops</string><string>start</string></array></dict></plist>", ""},
		{"launchd, a config named like a key elsewhere", UnitLaunchd,
			"<plist><dict><key>Other</key><dict><key>HOME</key><string>/not/this</string></dict>" +
				"<key>EnvironmentVariables</key><dict><key>HOME</key><string>/Users/ada</string></dict></dict></plist>",
			"/Users/ada/.config/tokenops/config.yaml"},
		{"not a unit", UnitLaunchd, "not xml <<<", ""},
		{"empty", UnitSystemd, "", ""},
		{"no supervisor", UnitKind("initd"), "ExecStart=/usr/bin/tokenops start --config /x", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := UnitConfigPath(tt.kind, tt.body); got != tt.want {
				t.Errorf("UnitConfigPath = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSystemdWords(t *testing.T) {
	tests := []struct {
		in      string
		command bool
		want    []string
	}{
		{`/usr/bin/tokenops start`, true, []string{"/usr/bin/tokenops", "start"}},
		{`  a   b	c `, true, []string{"a", "b", "c"}},
		{`"a b" 'c d' e\ f`, true, []string{"a b", "c d", "e f"}},
		{`"say \"hi\" \\ there"`, true, []string{`say "hi" \ there`}},
		{`100%% $$HOME`, true, []string{"100%", "$HOME"}},
		{`100%% $$HOME`, false, []string{"100%", "$$HOME"}},
		{`""`, true, []string{""}},
		{``, true, nil},
	}
	for _, tt := range tests {
		if got := systemdWords(tt.in, tt.command); !slices.Equal(got, tt.want) {
			t.Errorf("systemdWords(%q, %v) = %q, want %q", tt.in, tt.command, got, tt.want)
		}
	}
}

func TestUnitReadsAFileThatIsNotThere(t *testing.T) {
	if got := UnitReads(UnitLaunchd, "/nonexistent/unit.plist"); got != "" {
		t.Errorf("UnitReads = %q, want nothing", got)
	}
}
