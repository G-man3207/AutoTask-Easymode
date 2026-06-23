package main

import (
	"context"
	_ "embed" // for go:embed of ui.html
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"time"
)

//go:embed ui.html
var uiHTML []byte

const (
	defaultUIPort       = 7378
	uiReadHeaderTimeout = 5 * time.Second
)

// uiConfig mirrors the editable configuration fields for the local web panel.
type uiConfig struct {
	Username             string `json:"username"`
	IntegrationCode      string `json:"integrationCode"`
	Secret               string `json:"secret"`
	APIBaseURL           string `json:"apiBaseUrl"`
	ResourceID           int    `json:"resourceId"`
	QueueID              int    `json:"queueId"`
	Priority             int    `json:"priority"`
	TicketStatusNew      int    `json:"ticketStatusNew"`
	TicketStatusComplete int    `json:"ticketStatusComplete"`
	BillingCodeID        int    `json:"billingCodeId"`
	RoleID               int    `json:"roleId"`
	FlagHoursOver        int    `json:"flagHoursOver"`
	FlagNotesUnder       int    `json:"flagNotesUnder"`
	FlagHoursAlways      int    `json:"flagHoursAlways"`
}

// cmdUI serves the local configuration panel on 127.0.0.1 and blocks until the
// process is interrupted. It is an interactive command, so (unlike the others)
// it does not emit a JSON result while running.
func (a *App) cmdUI(args []string) (*cmdResult, error) {
	fs := newFlagSet("ui")
	port := fs.Int("port", defaultUIPort, "localhost port to serve on")
	noOpen := fs.Bool("no-open", false, "do not open the browser automatically")
	if err := fs.Parse(args); err != nil {
		return nil, usageErr("ui", err)
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(context.Background(), "tcp", fmt.Sprintf("127.0.0.1:%d", *port))
	if err != nil {
		return nil, hinted("try another --port", "cannot listen on 127.0.0.1:%d: %v", *port, err)
	}
	url := "http://" + ln.Addr().String()
	srv := &http.Server{Handler: a.uiHandler(), ReadHeaderTimeout: uiReadHeaderTimeout}

	_, _ = fmt.Fprintf(os.Stdout, "atem config panel: %s  (press Ctrl+C to stop)\n", url)
	if !*noOpen {
		openBrowser(url)
	}
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return nil, err
	}
	return &cmdResult{action: "ui", data: UIResult{Served: url}}, nil
}

// uiHandler wires the panel's routes. Split out so handlers are testable without
// binding a socket.
func (a *App) uiHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(uiHTML)
	})
	mux.HandleFunc("/api/config", a.handleConfig)
	mux.HandleFunc("/api/doctor", a.handleDoctor)
	return mux
}

func (a *App) handleConfig(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		writeJSONObject(w, a.uiConfigFromCfg())
	case http.MethodPost:
		var uc uiConfig
		if err := json.NewDecoder(r.Body).Decode(&uc); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		a.applyUIConfig(uc)
		if err := a.cfg.Save(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSONObject(w, map[string]any{"ok": true})
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (a *App) handleDoctor(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSONObject(w, a.doctorReport(r.Context()))
}

// uiConfigFromCfg builds the editable view from the on-disk config (raw file
// values, so the form reflects exactly what is saved).
func (a *App) uiConfigFromCfg() uiConfig {
	d := a.cfg.Defaults
	return uiConfig{
		Username:             a.cfg.Username,
		IntegrationCode:      a.cfg.IntegrationCode,
		Secret:               a.cfg.Secret,
		APIBaseURL:           a.cfg.APIBaseURL,
		ResourceID:           a.cfg.ResourceID,
		QueueID:              d.QueueID,
		Priority:             d.Priority,
		TicketStatusNew:      d.TicketStatusNew,
		TicketStatusComplete: d.TicketStatusComplete,
		BillingCodeID:        d.BillingCodeID,
		RoleID:               d.RoleID,
		FlagHoursOver:        d.FlagHoursOver,
		FlagNotesUnder:       d.FlagNotesUnder,
		FlagHoursAlways:      d.FlagHoursAlways,
	}
}

func (a *App) applyUIConfig(uc uiConfig) {
	a.cfg.Username = uc.Username
	a.cfg.IntegrationCode = uc.IntegrationCode
	a.cfg.Secret = uc.Secret
	a.cfg.APIBaseURL = uc.APIBaseURL
	a.cfg.ResourceID = uc.ResourceID
	a.cfg.Defaults.QueueID = uc.QueueID
	a.cfg.Defaults.Priority = uc.Priority
	a.cfg.Defaults.TicketStatusNew = uc.TicketStatusNew
	a.cfg.Defaults.TicketStatusComplete = uc.TicketStatusComplete
	a.cfg.Defaults.BillingCodeID = uc.BillingCodeID
	a.cfg.Defaults.RoleID = uc.RoleID
	a.cfg.Defaults.FlagHoursOver = uc.FlagHoursOver
	a.cfg.Defaults.FlagNotesUnder = uc.FlagNotesUnder
	a.cfg.Defaults.FlagHoursAlways = uc.FlagHoursAlways
}

func writeJSONObject(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(v)
}

// openBrowser best-effort opens url in the default browser.
func openBrowser(url string) {
	var name string
	var args []string
	switch runtime.GOOS {
	case "windows":
		name, args = "rundll32", []string{"url.dll,FileProtocolHandler", url}
	case "darwin":
		name, args = "open", []string{url}
	default:
		name, args = "xdg-open", []string{url}
	}
	// Fixed OS opener launched with our own 127.0.0.1 URL — not user input.
	// (gosec G204 is excluded for this file in .golangci.yml.)
	_ = exec.CommandContext(context.Background(), name, args...).Start()
}
