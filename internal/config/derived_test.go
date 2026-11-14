package config

import (
	"strings"
	"testing"
	"time"
)

// A derived figure's max age has a default, honours a set value, and
// refuses a negative one or one longer than the figures stay truthful.
func TestDerivedMaxAge(t *testing.T) {
	cases := []struct {
		name    string
		set     time.Duration
		want    time.Duration
		invalid bool
	}{
		{name: "unset is the default", set: 0, want: DefaultDerivedMaxAge},
		{name: "a set value stands", set: 2 * time.Minute, want: 2 * time.Minute},
		{name: "the longest allowed stands", set: MaxDerivedMaxAge, want: MaxDerivedMaxAge},
		{name: "negative is refused", set: -time.Second, invalid: true},
		{name: "past the longest is refused", set: MaxDerivedMaxAge + time.Second, invalid: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cfg := Default()
			cfg.Storage.DerivedMaxAge = c.set
			err := cfg.Validate()
			if c.invalid {
				if err == nil || !strings.Contains(err.Error(), "storage.derived_max_age") {
					t.Fatalf("Validate() = %v, want a storage.derived_max_age error", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Validate() = %v", err)
			}
			if got := cfg.Storage.EffectiveDerivedMaxAge(); got != c.want {
				t.Errorf("EffectiveDerivedMaxAge() = %s, want %s", got, c.want)
			}
		})
	}
}

// TOKENOPS_STORAGE_DERIVED_MAX_AGE overrides the file; a malformed value
// leaves the file's setting, as the other overrides do.
func TestDerivedMaxAgeEnv(t *testing.T) {
	for _, c := range []struct {
		env  string
		want time.Duration
	}{
		{env: "90s", want: 90 * time.Second},
		{env: "soon", want: time.Minute},
	} {
		t.Setenv("TOKENOPS_STORAGE_DERIVED_MAX_AGE", c.env)
		cfg := Default()
		cfg.Storage.DerivedMaxAge = time.Minute
		applyEnvOverrides(&cfg)
		if cfg.Storage.DerivedMaxAge != c.want {
			t.Errorf("env %q: %s, want %s", c.env, cfg.Storage.DerivedMaxAge, c.want)
		}
	}
}
