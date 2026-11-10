package claudecodejsonl

// Messages tells the first transcript line of a message from its repeats.
//
// Claude Code writes an assistant message as one line per content block
// (a thought, some text, each tool call), and every one of those lines
// repeats the message's id and its whole usage. Whatever sums usage or
// counts turns from a transcript counts a message at its first line:
// counting every line counts a message as often as it has blocks.
//
// Make one with Messages{} for each transcript, or for each run over
// several when a message copied into a second file (a resumed or forked
// session) should count once too.
type Messages map[string]struct{}

// First reports whether id has not been seen before, and remembers it. A
// line with no id is its own message: there is nothing to tell it from
// another by.
func (m Messages) First(id string) bool {
	if id == "" {
		return true
	}
	if _, seen := m[id]; seen {
		return false
	}
	m[id] = struct{}{}
	return true
}
