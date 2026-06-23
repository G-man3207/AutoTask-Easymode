// Package config loads and persists the local atem configuration: Autotask
// credentials, the cached API zone URL, sensible ticket/time-entry defaults,
// and customer name -> companyID aliases.
//
// Secrets resolve from environment variables first, then the config file, so a
// technician can keep the secret out of the plaintext file via:
//
//	ATEM_USERNAME, ATEM_SECRET, ATEM_INTEGRATION_CODE, ATEM_RESOURCE_ID
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Defaults holds org-specific IDs used when creating tickets and time entries.
// They vary per Autotask instance, so they live in config rather than the code.
type Defaults struct {
	QueueID              int `json:"queueId,omitempty"`              // required by Autotask to create a ticket
	Priority             int `json:"priority,omitempty"`             // ticket priority id
	TicketStatusNew      int `json:"ticketStatusNew,omitempty"`      // status id for a freshly opened ticket
	TicketStatusComplete int `json:"ticketStatusComplete,omitempty"` // status id used when closing
	BillingCodeID        int `json:"billingCodeId,omitempty"`        // work type for time entries
	RoleID               int `json:"roleId,omitempty"`               // role for time entries
	FlagHoursOver        int `json:"flagHoursOver,omitempty"`        // review flag: time entries longer than this many hours
	FlagNotesUnder       int `json:"flagNotesUnder,omitempty"`       // ...whose note is shorter than this many characters
	FlagHoursAlways      int `json:"flagHoursAlways,omitempty"`      // review flag: entries at least this many hours, regardless of note
}

// Config is the on-disk configuration. Credential fields may be left empty in
// the file and supplied via environment variables instead.
type Config struct {
	Username        string         `json:"username,omitempty"`
	IntegrationCode string         `json:"integrationCode,omitempty"`
	Secret          string         `json:"secret,omitempty"` // discouraged in the file; prefer ATEM_SECRET
	APIBaseURL      string         `json:"apiBaseUrl,omitempty"`
	ResourceID      int            `json:"resourceId,omitempty"` // your technician resource (who the time is logged as)
	Defaults        Defaults       `json:"defaults"`
	Aliases         map[string]int `json:"aliases,omitempty"` // customer alias -> companyID

	path string // where this config was loaded from (not serialized)
}

// Dir returns the atem config directory, creating it if necessary.
func Dir() (string, error) {
	base, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	d := filepath.Join(base, "atem")
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", err
	}
	return d, nil
}

// StatePath returns the path to the local timer state file.
func StatePath() (string, error) { return joinDir("state.json") }

// configPath returns the path to the config file.
func configPath() (string, error) { return joinDir("config.json") }

func joinDir(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}

// Load reads the standard config file, returning an empty (but usable) config
// if none exists yet. Environment variables are NOT merged in here; use the
// accessor methods (Credentials, Resource) so Save never persists env values.
func Load() (*Config, error) {
	p, err := configPath()
	if err != nil {
		return nil, err
	}
	return LoadFrom(p)
}

// LoadFrom reads a config from an explicit path. Tests use this to point at a
// temporary file instead of the user config directory.
func LoadFrom(path string) (*Config, error) {
	c := &Config{path: path, Aliases: map[string]int{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return c, nil
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(data, c); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	c.path = path
	if c.Aliases == nil {
		c.Aliases = map[string]int{}
	}
	return c, nil
}

// Save writes the config file with restrictive permissions.
func (c *Config) Save() error {
	if c.path == "" {
		p, err := configPath()
		if err != nil {
			return err
		}
		c.path = p
	}
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(c.path, data, 0o600)
}

// Path returns the file this config is bound to.
func (c *Config) Path() string { return c.path }

// Credentials resolves the API credentials, preferring environment variables.
func (c *Config) Credentials() (username, secret, integrationCode string) {
	return firstNonEmpty(os.Getenv("ATEM_USERNAME"), c.Username),
		firstNonEmpty(os.Getenv("ATEM_SECRET"), c.Secret),
		firstNonEmpty(os.Getenv("ATEM_INTEGRATION_CODE"), c.IntegrationCode)
}

// Resource resolves the technician resource id, preferring ATEM_RESOURCE_ID.
func (c *Config) Resource() int {
	if v := os.Getenv("ATEM_RESOURCE_ID"); v != "" {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil {
			return n
		}
	}
	return c.ResourceID
}

// MissingCredentials reports which credential pieces are absent, for friendly errors.
func (c *Config) MissingCredentials() []string {
	u, s, code := c.Credentials()
	var missing []string
	if u == "" {
		missing = append(missing, "username")
	}
	if s == "" {
		missing = append(missing, "secret")
	}
	if code == "" {
		missing = append(missing, "integrationCode")
	}
	return missing
}

// ResolveCompany turns a CLI argument into a companyID. It accepts a numeric id
// directly, or a case-insensitive alias previously saved with `atem company alias`.
func (c *Config) ResolveCompany(arg string) (int, error) {
	arg = strings.TrimSpace(arg)
	if arg == "" {
		return 0, errors.New("no company given")
	}
	if n, err := strconv.Atoi(arg); err == nil {
		return n, nil
	}
	if id, ok := c.Aliases[strings.ToLower(arg)]; ok {
		return id, nil
	}
	return 0, fmt.Errorf("unknown company %q: pass a numeric companyID or add an alias with `atem company alias %q <id>`", arg, arg)
}

// SetAlias stores a lowercased alias -> companyID mapping.
func (c *Config) SetAlias(alias string, companyID int) {
	if c.Aliases == nil {
		c.Aliases = map[string]int{}
	}
	c.Aliases[strings.ToLower(strings.TrimSpace(alias))] = companyID
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if strings.TrimSpace(v) != "" {
			return v
		}
	}
	return ""
}
