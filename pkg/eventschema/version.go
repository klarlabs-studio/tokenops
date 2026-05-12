package eventschema

// SchemaVersion is the current TokenOps event schema version.
//
// Bumping rules:
//   - Patch (1.0.X): additive, backward-compatible field additions or doc fixes.
//   - Minor (1.X.0): additive enum members, new optional fields.
//   - Major (X.0.0): breaking changes — renamed/removed fields, type changes.
//
// Consumers should treat unknown fields as forward-compatible and unknown
// enum values as the package-defined Unknown sentinel.
const SchemaVersion = "1.2.0"
