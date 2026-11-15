package agentdx

// LineMarginPoints is how far a band's rejection rate must rise above the
// best trusted band below it before the context line moves there. Rejection
// rates are a few points in every band on a healthy corpus, so a smaller
// rise is noise, not a size at which quality got worse.
const LineMarginPoints = 2.0

// ContextLine is the context size past which this operator's own bands
// show instructions going worse: the lower edge of the first trusted band
// whose rejection rate rises LineMarginPoints above the lowest trusted
// band below it. A band is trusted with minPromptsPerBand instructions.
// ok is false when no band rises that far, and the fallback stands.
func ContextLine(bands []ContextBand, fallback int64) (line int64, ok bool) {
	lowest := -1.0
	for _, b := range bands {
		if b.Prompts < minPromptsPerBand {
			continue
		}
		if lowest >= 0 && b.Lo > 0 && b.RejectRatePct >= lowest+LineMarginPoints {
			return b.Lo, true
		}
		if lowest < 0 || b.RejectRatePct < lowest {
			lowest = b.RejectRatePct
		}
	}
	return fallback, false
}

// JudgedRejectRate is the reject rate over the trusted bands only (those
// with minPromptsPerBand instructions), each weighted by its instructions,
// and how many instructions it pools. With no trusted band it is 0 over 0.
func JudgedRejectRate(bands []ContextBand) (pct float64, prompts int) {
	var rejects float64
	for _, b := range bands {
		if b.Prompts < minPromptsPerBand {
			continue
		}
		prompts += b.Prompts
		rejects += b.RejectRatePct * float64(b.Prompts)
	}
	if prompts == 0 {
		return 0, 0
	}
	return rejects / float64(prompts), prompts
}
