package atomicfile

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteFileReplacesExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("old"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}

	if err := WriteFile(path, []byte("new"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "new" {
		t.Fatalf("content = %q, want new", data)
	}
}

func TestWriteJSONUsesIndentedEncoding(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	value := struct {
		Name string `json:"name"`
	}{Name: "atem"}

	if err := WriteJSON(path, value, 0o600); err != nil {
		t.Fatalf("write json: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read json: %v", err)
	}
	if got := string(data); !strings.Contains(got, "\n  \"name\": \"atem\"\n") {
		t.Fatalf("json was not indented as expected: %q", got)
	}
}

func TestWriteFileRejectsEmptyPath(t *testing.T) {
	if err := WriteFile("", []byte("nope"), 0o600); err == nil {
		t.Fatal("WriteFile() error = nil, want error")
	}
}
