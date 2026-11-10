package daemon

import (
	"encoding/xml"
	"os"
	"strings"

	"go.klarlabs.de/tokenops/internal/config"
)

// UnitReads is the config file the daemon started from the unit file at
// path reads, "" when the file cannot be read or does not say.
func UnitReads(kind UnitKind, path string) string {
	body, err := os.ReadFile(path) //nolint:gosec // the operator's own unit file
	if err != nil {
		return ""
	}
	return UnitConfigPath(kind, string(body))
}

// UnitConfigPath is the config file a daemon started from the unit body
// reads: the one its command line names, or the default one of the
// environment the unit gives it. It is "" when the unit does not say.
//
// The daemon reads its config once, at start, from the place its unit
// gives it and not from the shell a command runs in. A command that wrote
// another file and restarted the daemon reported a change that the daemon
// never read.
func UnitConfigPath(kind UnitKind, body string) string {
	var args []string
	var env map[string]string
	switch kind {
	case UnitLaunchd:
		args, env = launchdCommand(body)
	case UnitSystemd:
		args, env = systemdCommand(body)
	}
	// args[0] is the binary.
	for i := 1; i < len(args); i++ {
		if path, ok := strings.CutPrefix(args[i], "--config="); ok {
			return path
		}
		if (args[i] == "--config" || args[i] == "-c") && i+1 < len(args) {
			return args[i+1]
		}
	}
	if env["XDG_CONFIG_HOME"] == "" && env["HOME"] == "" {
		return ""
	}
	return config.DefaultPathIn(env["XDG_CONFIG_HOME"], env["HOME"])
}

// launchdCommand reads the program arguments and the environment out of a
// LaunchAgent plist.
func launchdCommand(body string) (args []string, env map[string]string) {
	env = map[string]string{}
	dec := xml.NewDecoder(strings.NewReader(body))
	// key is the last key of the job's own dictionary, section the value
	// of it being read, envKey the last key inside the environment.
	var key, section, envKey string
	var text strings.Builder
	for {
		tok, err := dec.Token()
		if err != nil {
			return args, env
		}
		switch t := tok.(type) {
		case xml.StartElement:
			text.Reset()
			switch {
			case t.Name.Local == "array" && key == "ProgramArguments":
				section = key
			case t.Name.Local == "dict" && key == "EnvironmentVariables":
				section = key
			}
		case xml.CharData:
			text.Write(t)
		case xml.EndElement:
			switch t.Name.Local {
			case "key":
				switch section {
				case "EnvironmentVariables":
					envKey = text.String()
				case "":
					key = text.String()
				}
			case "string":
				switch section {
				case "ProgramArguments":
					args = append(args, text.String())
				case "EnvironmentVariables":
					env[envKey] = text.String()
				}
			case "array", "dict":
				if section != "" {
					section, key = "", ""
				}
			}
		}
	}
}

// systemdCommand reads the command line and the environment out of a
// systemd unit.
func systemdCommand(body string) (args []string, env map[string]string) {
	env = map[string]string{}
	for line := range strings.SplitSeq(body, "\n") {
		line = strings.TrimSpace(line)
		if rest, ok := strings.CutPrefix(line, "ExecStart="); ok {
			args = systemdWords(rest, true)
		}
		if rest, ok := strings.CutPrefix(line, "Environment="); ok {
			for _, word := range systemdWords(rest, false) {
				if name, value, ok := strings.Cut(word, "="); ok {
					env[name] = value
				}
			}
		}
	}
	return args, env
}

// systemdWords splits a unit file value into its words, undoing what
// systemdQuote does: quotes, backslashes, and the doubled % (and, in a
// command line, $).
func systemdWords(s string, command bool) []string {
	var words []string
	var word strings.Builder
	var quote rune
	inWord := false
	rs := []rune(s)
	for i := 0; i < len(rs); i++ {
		c := rs[i]
		next := rune(0)
		if i+1 < len(rs) {
			next = rs[i+1]
		}
		switch {
		case c == '\\' && quote != '\'' && next != 0:
			word.WriteRune(next)
			inWord = true
			i++
		case (c == '%' || command && c == '$') && next == c:
			word.WriteRune(c)
			inWord = true
			i++
		case quote != 0:
			if c == quote {
				quote = 0
			} else {
				word.WriteRune(c)
			}
		case c == '"' || c == '\'':
			quote, inWord = c, true
		case c == ' ' || c == '\t':
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		default:
			word.WriteRune(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}
