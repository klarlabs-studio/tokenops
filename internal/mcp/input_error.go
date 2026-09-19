package mcp

import (
	"errors"

	"go.klarlabs.de/mcp/server"
)

// inputError marks err as a problem with what the caller asked for — a bad
// argument, or a config change the rules refuse — so the agent sees the
// message and can correct its call.
//
// The MCP library replaces any other handler error with a bare "internal
// error", deliberately: an unexpected failure may carry paths or state. That
// is right for a store that failed to open and wrong for "a budget needs a
// ceiling": every refusal the config tools were written to explain reached
// the agent as "internal error", which it cannot act on. Only errors about
// the caller's input are marked; failures inside TokenOps stay masked.
func inputError(err error) error {
	if err == nil {
		return nil
	}
	var ie *server.ToolInputError
	if errors.As(err, &ie) {
		return err
	}
	return &server.ToolInputError{Message: err.Error()}
}
