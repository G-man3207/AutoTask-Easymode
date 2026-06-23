package main

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestResultFromErrorPlain(t *testing.T) {
	r := resultFromError(errors.New("boom"))
	if r.OK || r.Error != "boom" || r.Hint != "" {
		t.Errorf("r = %+v", r)
	}
}

func TestResultFromErrorWithHint(t *testing.T) {
	r := resultFromError(hinted("try X", "bad %s", "thing"))
	if r.Error != "bad thing" || r.Hint != "try X" {
		t.Errorf("r = %+v", r)
	}
}

func TestAppErrorUnwrap(t *testing.T) {
	base := errors.New("inner")
	ae := &appError{err: base, hint: "h"}
	if !errors.Is(ae, base) {
		t.Error("Unwrap should expose inner error")
	}
	if ae.Error() != "inner" {
		t.Errorf("Error() = %q", ae.Error())
	}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	err := writeJSON(&buf, Result{OK: true, Action: "x", Data: map[string]any{"html": "<b>"}})
	if err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, `"ok": true`) {
		t.Errorf("missing ok field: %s", out)
	}
	if !strings.Contains(out, "<b>") {
		t.Errorf("should not HTML-escape output: %s", out)
	}
}
