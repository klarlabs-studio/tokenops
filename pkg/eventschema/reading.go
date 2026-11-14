package eventschema

// AttrRepeat marks a vendor reading that a scheduled reader stored again
// unchanged: its figures are those of the reading the same reader stored
// before it, and only its time (and how long it stands) is new. Readers
// of a reading treat it as any other; it keeps the newest reading's age
// honest. What it does not do is change a figure computed from the
// readings' contents, so a job that keeps such a figure (ADR 0019) does
// not count it as a change. Its value is AttrRepeatValue.
const AttrRepeat = "repeat"

// AttrRepeatValue is the value AttrRepeat carries.
const AttrRepeatValue = "true"
