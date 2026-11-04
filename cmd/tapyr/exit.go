package main

import (
	"errors"
	"fmt"
	"io"
)

// Exit codes, see docs/spec/09-cli-api-ui.md.
const (
	exitOK            = 0
	exitFailure       = 1 // usage error or internal error
	exitInvalidConfig = 2
	exitInfeasible    = 3
)

// exitError carries the exit code of a failed command. When silent is true,
// the command has already reported the problem and the message is not
// printed again.
type exitError struct {
	code   int
	silent bool
	err    error
}

func (e *exitError) Error() string { return e.err.Error() }
func (e *exitError) Unwrap() error { return e.err }

// report prints err (if any) and maps it to an exit code.
func report(err error, stderr io.Writer) int {
	if err == nil {
		return exitOK
	}
	if ee, ok := errors.AsType[*exitError](err); ok {
		if !ee.silent {
			fmt.Fprintf(stderr, "Error: %v\n", ee.err)
		}
		return ee.code
	}
	// Errors not produced by our commands come from cobra: usage errors such
	// as unknown commands or flags.
	fmt.Fprintf(stderr, "Error: %v\nRun 'tapyr --help' for usage.\n", err)
	return exitFailure
}
