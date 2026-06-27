package main

import (
	"strings"
	"testing"
)

// TestOperationKey locks in the write-journal key contract: the key identifies
// one operation deterministically (so a crashed write resumes on retry), differs
// across action/payload, and — since it is local, single-user, and carries no
// security property — is the payload verbatim rather than an opaque hash. That
// makes a stranded journal record human-readable when it needs inspecting.
func TestOperationKey(t *testing.T) {
	a, err := operationKey("time.add", map[string]any{"ticket": 1})
	if err != nil {
		t.Fatalf("operationKey: %v", err)
	}

	// Deterministic: identical input must reproduce the key (resume relies on it).
	b, err := operationKey("time.add", map[string]any{"ticket": 1})
	if err != nil {
		t.Fatalf("operationKey: %v", err)
	}
	if a != b {
		t.Errorf("operationKey not deterministic: %q vs %q", a, b)
	}

	// The key embeds the action and payload so a stranded record is debuggable.
	if !strings.HasPrefix(a, "time.add:") {
		t.Errorf("key %q should be prefixed with the action", a)
	}
	if !strings.Contains(a, `"ticket":1`) {
		t.Errorf("key %q should embed the payload verbatim", a)
	}

	// Different action or payload must yield a different key.
	if c, _ := operationKey("timer.stop", map[string]any{"ticket": 1}); c == a {
		t.Errorf("keys should differ across actions: %q", c)
	}
	if d, _ := operationKey("time.add", map[string]any{"ticket": 2}); d == a {
		t.Errorf("keys should differ across payloads: %q", d)
	}
}
