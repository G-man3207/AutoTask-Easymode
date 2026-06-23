package main

import (
	"io"
	"os"
	"strings"
	"testing"
)

// captureStdout redirects os.Stdout for the duration of fn and returns what was
// written. It is not safe for use with t.Parallel.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w
	defer func() { os.Stdout = old }()
	fn()
	_ = w.Close()
	data, _ := io.ReadAll(r)
	return string(data)
}

func TestRunVersion(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = run([]string{"version"}) })
	if code != 0 {
		t.Errorf("exit code = %d", code)
	}
	if !strings.Contains(out, "atem "+version) {
		t.Errorf("out = %q", out)
	}
	// Build metadata is stamped via -ldflags; with a plain `go test`/`go build`
	// it defaults to "unknown", but the labels must always be present so a stale
	// binary can be told apart from a freshly installed one.
	if !strings.Contains(out, "commit ") || !strings.Contains(out, "built ") {
		t.Errorf("version output missing build metadata: %q", out)
	}
}

func TestRunNoArgsShowsUsage(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = run(nil) })
	if code != 2 {
		t.Errorf("exit code = %d, want 2", code)
	}
	if !strings.Contains(out, "USAGE") {
		t.Errorf("usage missing: %q", out)
	}
}

func TestRunHelp(t *testing.T) {
	var code int
	out := captureStdout(t, func() { code = run([]string{"help"}) })
	if code != 0 || !strings.Contains(out, "USAGE") {
		t.Errorf("help failed: code=%d out=%q", code, out)
	}
}

func TestRunEndToEnd(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // keep newApp away from the real config dir

	var code int
	out := captureStdout(t, func() { code = run([]string{"config", "show"}) })
	if code != 0 || !strings.Contains(out, `"action": "config.show"`) {
		t.Errorf("config show via run: code=%d out=%q", code, out)
	}

	out = captureStdout(t, func() { code = run([]string{"nope"}) })
	if code != 1 || !strings.Contains(out, `"ok": false`) {
		t.Errorf("unknown via run: code=%d out=%q", code, out)
	}
}
