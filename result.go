package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Result is the single JSON object every command prints. The stable shape makes
// the CLI easy to drive from an AI agent: success carries data; failure carries
// a message and an optional remediation hint.
type Result struct {
	OK     bool   `json:"ok"`
	Action string `json:"action,omitempty"`
	DryRun bool   `json:"dryRun,omitempty"`
	Data   any    `json:"data,omitempty"`
	Error  string `json:"error,omitempty"`
	Hint   string `json:"hint,omitempty"`
}

// cmdResult is what a command handler returns on success.
type cmdResult struct {
	action string
	dryRun bool
	data   any
}

// appError carries a remediation hint alongside an error.
type appError struct {
	err  error
	hint string
}

func (e *appError) Error() string { return e.err.Error() }
func (e *appError) Unwrap() error { return e.err }

// hinted wraps an error message with a hint shown to the user.
func hinted(hint, format string, args ...any) error {
	return &appError{err: fmt.Errorf(format, args...), hint: hint}
}

// resultFromError converts any error into a failure Result.
func resultFromError(err error) Result {
	res := Result{OK: false, Error: err.Error()}
	var ae *appError
	if errors.As(err, &ae) {
		res.Hint = ae.hint
	}
	return res
}

// writeJSON encodes r as indented JSON without HTML escaping.
func writeJSON(w io.Writer, r Result) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	enc.SetEscapeHTML(false)
	return enc.Encode(r)
}

func emitJSON(w, errw io.Writer, r Result) bool {
	if err := writeJSON(w, r); err != nil {
		_, _ = fmt.Fprintf(errw, "failed to write result JSON: %v\n", err)
		return false
	}
	return true
}
