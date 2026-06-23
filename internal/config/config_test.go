package config

import (
	"path/filepath"
	"testing"
)

func tempConfig(t *testing.T) *Config {
	t.Helper()
	c, err := LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	return c
}

func TestLoadFromMissingReturnsEmpty(t *testing.T) {
	c := tempConfig(t)
	if len(c.Aliases) != 0 {
		t.Errorf("expected no aliases, got %v", c.Aliases)
	}
	if len(c.MissingCredentials()) != 3 {
		t.Errorf("expected all credentials missing, got %v", c.MissingCredentials())
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	c, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	c.Username = "api@example.com"
	c.Defaults.QueueID = 8
	c.SetAlias("Acme Care", 42)
	if err := c.Save(); err != nil {
		t.Fatal(err)
	}

	got, err := LoadFrom(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.Username != "api@example.com" {
		t.Errorf("username = %q", got.Username)
	}
	if got.Defaults.QueueID != 8 {
		t.Errorf("queueID = %d", got.Defaults.QueueID)
	}
	if got.Aliases["acme care"] != 42 {
		t.Errorf("alias = %d", got.Aliases["acme care"])
	}
}

func TestCredentialsEnvOverride(t *testing.T) {
	c := tempConfig(t)
	c.Username = "file-user"
	c.Secret = "file-secret"
	c.IntegrationCode = "file-code"
	t.Setenv("ATEM_USERNAME", "env-user")
	t.Setenv("ATEM_SECRET", "env-secret")

	user, secret, code := c.Credentials()
	if user != "env-user" || secret != "env-secret" {
		t.Errorf("env override failed: %q %q", user, secret)
	}
	if code != "file-code" {
		t.Errorf("integrationCode should fall back to file: %q", code)
	}
}

func TestResourceEnvOverride(t *testing.T) {
	c := tempConfig(t)
	c.ResourceID = 7
	if c.Resource() != 7 {
		t.Fatalf("want 7, got %d", c.Resource())
	}
	t.Setenv("ATEM_RESOURCE_ID", "99")
	if c.Resource() != 99 {
		t.Fatalf("env should override, got %d", c.Resource())
	}
}

func TestStandardPaths(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir()) // keep Dir() out of the real config location
	cp, err := configPath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(cp) != "config.json" {
		t.Errorf("config path = %q", cp)
	}
	sp, err := StatePath()
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(sp) != "state.json" {
		t.Errorf("state path = %q", sp)
	}
}

func TestLoadUsesStandardPath(t *testing.T) {
	t.Setenv("APPDATA", t.TempDir())
	c, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if c.Path() == "" {
		t.Error("Path() should be set after Load")
	}
}

func TestResolveCompany(t *testing.T) {
	c := tempConfig(t)
	c.SetAlias("Acme", 42)
	tests := []struct {
		name    string
		arg     string
		want    int
		wantErr bool
	}{
		{"numeric", "123", 123, false},
		{"alias lower", "acme", 42, false},
		{"alias mixed case", "ACME", 42, false},
		{"unknown", "nope", 0, true},
		{"empty", "", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := c.ResolveCompany(tt.arg)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tt.arg)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("got %d want %d", got, tt.want)
			}
		})
	}
}
