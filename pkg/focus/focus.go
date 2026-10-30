// Package focus is the FOCUS writer the TokenOps client exports spend
// with (internal/contexts/spend/focus), for a program outside this module
// that writes the same format: the team plane writes its organisation
// dataset with it (ADR 0012 §11), so the server's file and a member's own
// export follow one set of rules and one conformance test.
package focus

import f "go.klarlabs.de/tokenops/internal/contexts/spend/focus"

type (
	// Spec is a FOCUS version a dataset can be written in.
	Spec = f.Spec
	// Row is one FOCUS charge.
	Row = f.Row
	// Blend is one day's usage of one provider's models taken together.
	Blend = f.Blend
)

// V1_4 is FOCUS 1.4, the version the organisation dataset is written in.
var V1_4 = f.V1_4

// ColumnEstimatedCost is the custom column holding TokenOps' estimate.
const ColumnEstimatedCost = f.ColumnEstimatedCost

// Blended writes b as one FOCUS 1.4 usage row (see the internal package).
func Blended(b Blend) Row { return f.Blended(b) }
