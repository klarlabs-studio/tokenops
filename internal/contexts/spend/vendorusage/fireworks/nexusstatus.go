package fireworks

import (
	"context"
	"encoding/json"
	"regexp"
	"sort"
	"strings"
)

// SignIn is who Nexus CLI says is signed in. Any field may be empty.
type SignIn struct {
	Account string
	User    string
	Email   string
}

// SignInSource reports who is signed in, or the zero SignIn when it
// cannot tell. The daemon passes one that runs `nexus status --json`.
type SignInSource func(ctx context.Context) SignIn

var (
	userResource    = regexp.MustCompile(`^accounts/([a-z0-9][a-z0-9-]{0,62})/users/([A-Za-z0-9][A-Za-z0-9._-]{0,127})$`)
	accountResource = regexp.MustCompile(`^accounts/([a-z0-9][a-z0-9-]{0,62})$`)
	// resourceID is a Fireworks account or user id. It has no underscore,
	// so a key (fw_…) never passes for one.
	resourceID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9.-]{0,127}$`)
	// statusSecret is a field whose value is never looked at.
	statusSecret = regexp.MustCompile(`(?i)secret|password|token|api_?key|credential|cookie`)
	userField    = regexp.MustCompile(`(?i)user`)
	accountField = regexp.MustCompile(`(?i)account`)
)

// ParseNexusStatus reads the output of `nexus status --json`. Its fields
// are not documented one by one, so the parse looks through the whole
// document for values of the right shape under fitting names: a user
// resource name (accounts/<a>/users/<u>) anywhere, an account id under a
// name with "account", a user id under one with "user", and an email.
// Secret-named fields are skipped, and a value shaped like a key never
// matches any of the shapes.
func ParseNexusStatus(out []byte) SignIn {
	var doc any
	if json.Unmarshal(out, &doc) != nil {
		return SignIn{}
	}
	var s SignIn
	walkStatus(doc, "", 0, &s)
	return s
}

func walkStatus(v any, path string, depth int, s *SignIn) {
	if depth > maxJSONDepth {
		return
	}
	switch t := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(t))
		for k := range t {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			if statusSecret.MatchString(k) {
				continue
			}
			walkStatus(t[k], path+"."+k, depth+1, s)
		}
	case []any:
		for _, e := range t {
			walkStatus(e, path, depth+1, s)
		}
	case string:
		statusValue(path, strings.TrimSpace(t), s)
	}
}

// statusValue keeps one string value when its shape fits its path.
func statusValue(path, v string, s *SignIn) {
	if m := userResource.FindStringSubmatch(v); m != nil {
		if s.User == "" {
			s.Account, s.User = m[1], m[2]
		}
		return
	}
	if isEmail(v) {
		if s.Email == "" {
			s.Email = v
		}
		return
	}
	last := path[strings.LastIndex(path, ".")+1:]
	switch {
	case accountField.MatchString(path):
		if m := accountResource.FindStringSubmatch(v); m != nil {
			v = m[1]
		} else if !resourceID.MatchString(v) || !idField(last, "account") {
			return
		}
		if s.Account == "" {
			s.Account = v
		}
	case userField.MatchString(path) && resourceID.MatchString(v) && idField(last, "user"):
		if s.User == "" {
			s.User = v
		}
	}
}

// idField reports whether a field holds an id: "id", "<kind>", "<kind>Id"
// or "<kind>_id", in any case.
func idField(name, kind string) bool {
	n := strings.ToLower(strings.ReplaceAll(name, "_", ""))
	return n == "id" || n == kind || n == kind+"id"
}
