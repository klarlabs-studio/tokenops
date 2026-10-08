//go:build !unix

package accounts

import "os/exec"

// ownProcessGroup leaves cmd as it is: only its own process is killed on
// cancellation.
func ownProcessGroup(*exec.Cmd) {}
