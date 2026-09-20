package config

import (
	"reflect"
	"strings"
	"testing"
)

// secretWords are the field-name fragments that mark a value as a
// credential. Kept deliberately broad: a false positive costs one line in
// Redacted, a false negative puts a key on the dashboard.
var secretWords = []string{
	"token", "key", "secret", "cookie", "password", "passwd",
	"credential", "auth", "session",
}

// notSecret is the escape hatch for a field whose name trips secretWords
// but which carries no credential. It is empty today: every
// credential-shaped field in Config really is a credential, and all five
// are masked.
//
// It exists so that the first false positive is written down with its
// reason rather than fixed by weakening secretWords, which would silently
// stop guarding the real ones. TestNotSecretExemptionsStillExist fails on
// an entry that outlives its field, so the list cannot rot into a way of
// ignoring a secret that was renamed.
var notSecret = map[string]string{}

// Redacted is a hand-maintained allowlist, and the thing it protects
// against — a secret reaching `config show`, the MCP control tool or
// /api/config — is exactly what a forgotten field causes. Nothing failed
// when a new credential field was added; this is what fails.
//
// The test fills every string in the Config with a sentinel, redacts,
// then walks the result: any field whose *name* says credential must no
// longer hold the sentinel.
func TestRedactedMasksEverySecretShapedField(t *testing.T) {
	cfg := Default()
	const sentinel = "SENTINEL-SECRET-VALUE"
	fillStrings(reflect.ValueOf(&cfg).Elem(), sentinel)

	red := cfg.Redacted()

	var leaked []string
	walkStrings(reflect.ValueOf(red), "", func(path, val string) {
		if !looksSecret(path) || !strings.Contains(val, sentinel) {
			return
		}
		if why, ok := exempt(path); ok {
			t.Logf("%s exempt: %s", path, why)
			return
		}
		leaked = append(leaked, path)
	})

	if len(leaked) > 0 {
		t.Errorf("Redacted() left credential-shaped fields intact:\n  %s\n"+
			"add them to Redacted, or add a justified entry to notSecret",
			strings.Join(leaked, "\n  "))
	}
}

// The allowlist is only trustworthy if it is still describing real
// fields. An entry left behind after a rename silently stops protecting
// anything.
func TestNotSecretExemptionsStillExist(t *testing.T) {
	cfg := Default()
	seen := map[string]bool{}
	walkStrings(reflect.ValueOf(cfg), "", func(path, _ string) {
		for prefix := range notSecret {
			if path == prefix || strings.HasPrefix(path, prefix+".") {
				seen[prefix] = true
			}
		}
	})
	for prefix := range notSecret {
		if !seen[prefix] {
			t.Errorf("notSecret[%q] names a field that no longer exists", prefix)
		}
	}
}

// Every OTel header value is redacted regardless of its name, because
// the header carrying the credential is the operator's choice.
func TestRedactedMasksEveryHeader(t *testing.T) {
	cfg := Default()
	cfg.OTel.Headers = map[string]string{
		"authorization": "Bearer abc",
		"x-tenant":      "acme",
	}
	for k, v := range cfg.Redacted().OTel.Headers {
		if v != SensitiveHeaderPlaceholder {
			t.Errorf("header %q = %q, want the placeholder", k, v)
		}
	}
}

// Redacted must not mutate the receiver. It returns a copy that gets
// serialised while the original keeps serving requests.
func TestRedactedLeavesTheOriginalIntact(t *testing.T) {
	cfg := Default()
	cfg.Dashboard.AdminToken = "real-token"
	_ = cfg.Redacted()
	if cfg.Dashboard.AdminToken != "real-token" {
		t.Errorf("Redacted mutated the receiver: %q", cfg.Dashboard.AdminToken)
	}
}

func exempt(path string) (string, bool) {
	for prefix, why := range notSecret {
		if path == prefix || strings.HasPrefix(path, prefix+".") {
			return why, true
		}
	}
	return "", false
}

func looksSecret(path string) bool {
	lower := strings.ToLower(path)
	for _, w := range secretWords {
		if strings.Contains(lower, w) {
			return true
		}
	}
	return false
}

// fillStrings writes v into every settable string in the tree, including
// map values, so a field that is never populated by Default() is still
// covered.
func fillStrings(v reflect.Value, s string) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(s)
		}
	case reflect.Pointer:
		if !v.IsNil() {
			fillStrings(v.Elem(), s)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			if v.Type().Field(i).IsExported() {
				fillStrings(v.Field(i), s)
			}
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			fillStrings(v.Index(i), s)
		}
	case reflect.Map:
		if v.IsNil() && v.CanSet() {
			v.Set(reflect.MakeMap(v.Type()))
		}
		if v.Type().Key().Kind() == reflect.String && v.Type().Elem().Kind() == reflect.String {
			v.SetMapIndex(reflect.ValueOf("probe"), reflect.ValueOf(s))
			return
		}
		for _, k := range v.MapKeys() {
			elem := reflect.New(v.Type().Elem()).Elem()
			elem.Set(v.MapIndex(k))
			fillStrings(elem, s)
			v.SetMapIndex(k, elem)
		}
	}
}

// walkStrings visits every string in the tree with its dotted field path.
func walkStrings(v reflect.Value, path string, fn func(path, val string)) {
	switch v.Kind() {
	case reflect.String:
		fn(path, v.String())
	case reflect.Pointer:
		if !v.IsNil() {
			walkStrings(v.Elem(), path, fn)
		}
	case reflect.Struct:
		for i := range v.NumField() {
			f := v.Type().Field(i)
			if !f.IsExported() {
				continue
			}
			walkStrings(v.Field(i), join(path, f.Name), fn)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			walkStrings(v.Index(i), path, fn)
		}
	case reflect.Map:
		for _, k := range v.MapKeys() {
			walkStrings(v.MapIndex(k), path, fn)
		}
	}
}

func join(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}
