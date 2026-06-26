package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

const defaultHTTPAddr = ":8080"

func (a *App) serveHTTPCommand(args []string) int {
	fs := newFlagSet("serve")
	addr := fs.String("addr", envDefault("ATEM_HTTP_ADDR", defaultHTTPAddr), "HTTP listen address")
	toolset := fs.String("toolset", envDefault("ATEM_MCP_TOOLSET", "m365"), "MCP toolset: m365 or all")
	authMode := fs.String("auth", envDefault("ATEM_AUTH_MODE", "none"), "auth mode: none or entra")
	allowUnauthenticated := fs.Bool("allow-unauthenticated", envBool("ATEM_ALLOW_UNAUTHENTICATED"), "allow auth none on a non-loopback listener")
	tenantID := fs.String("tenant-id", envDefault("ATEM_ENTRA_TENANT_ID", ""), "expected Entra tenant id")
	audience := fs.String("audience", envDefault("ATEM_ENTRA_AUDIENCE", ""), "expected Entra token audience")
	metadataURL := fs.String("metadata-url", envDefault("ATEM_ENTRA_METADATA_URL", ""), "optional Entra OIDC metadata URL")
	profileFile := fs.String("profile-file", envDefault("ATEM_AUTH_PROFILE_FILE", ""), "technician profile JSON file")
	if err := fs.Parse(args); err != nil {
		if !emitJSON(os.Stdout, os.Stderr, resultFromError(usageErr("serve", err))) {
			return 1
		}
		return 1
	}
	if err := validateHTTPAuthExposure(*addr, *authMode, *allowUnauthenticated); err != nil {
		if !emitJSON(os.Stdout, os.Stderr, resultFromError(err)) {
			return 1
		}
		return 1
	}
	surface, err := mcpSurfaceByName(*toolset)
	if err != nil {
		if !emitJSON(os.Stdout, os.Stderr, resultFromError(err)) {
			return 1
		}
		return 1
	}
	authn, err := newMCPAuthenticator(authOptions{
		Mode:        *authMode,
		TenantID:    *tenantID,
		Audiences:   splitCSV(firstNonEmptyString(envDefault("ATEM_ENTRA_AUDIENCES", ""), *audience)),
		MetadataURL: *metadataURL,
		Profiles:    os.Getenv("ATEM_AUTH_PROFILES"),
		ProfileFile: *profileFile,
	})
	if err != nil {
		if !emitJSON(os.Stdout, os.Stderr, resultFromError(err)) {
			return 1
		}
		return 1
	}
	return a.serveHTTPMCP(*addr, surface, authn)
}

func mcpSurfaceByName(name string) (mcpSurface, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "m365", "copilot":
		return m365MCPSurface(), nil
	case "all", "local":
		return localMCPSurface(), nil
	default:
		return mcpSurface{}, hinted("use --toolset m365 or --toolset all", "unknown MCP toolset %q", name)
	}
}

func envDefault(name, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(name)); v != "" {
		return v
	}
	return fallback
}

func envBool(name string) bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(name))) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func validateHTTPAuthExposure(addr, authMode string, allowUnauthenticated bool) error {
	switch strings.ToLower(strings.TrimSpace(authMode)) {
	case "", "none":
		if allowUnauthenticated || isLoopbackListenAddr(addr) {
			return nil
		}
		return hinted(
			"set ATEM_AUTH_MODE=entra for hosted MCP, or bind to 127.0.0.1 for local unauthenticated testing",
			"refusing to serve unauthenticated MCP on non-loopback address %q", addr,
		)
	default:
		return nil
	}
}

func isLoopbackListenAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	host = strings.Trim(strings.TrimSpace(host), "[]")
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func (a *App) serveHTTPMCP(addr string, surface mcpSurface, authn mcpAuthenticator) int {
	srv := &http.Server{
		Addr:              addr,
		Handler:           a.mcpHTTPHandler(surface, authn),
		ReadHeaderTimeout: 5 * time.Second,
	}
	log.Printf("atem remote MCP listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Printf("atem remote MCP stopped: %v", err)
		return 1
	}
	return 0
}

func (a *App) mcpHTTPHandler(surface mcpSurface, authn mcpAuthenticator) http.Handler {
	if authn == nil {
		authn = noAuthenticator{}
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	})
	mux.HandleFunc("/mcp", func(w http.ResponseWriter, r *http.Request) {
		a.handleMCPHTTP(w, r, surface, authn)
	})
	return mux
}

func (a *App) handleMCPHTTP(w http.ResponseWriter, r *http.Request, surface mcpSurface, authn mcpAuthenticator) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if !acceptsStreamableHTTP(r.Header.Get("Accept")) {
		http.Error(w, "MCP clients must accept application/json and text/event-stream", http.StatusNotAcceptable)
		return
	}
	body, err := readMCPBody(w, r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if bytes.HasPrefix(bytes.TrimSpace(body), []byte("[")) {
		http.Error(w, "batched JSON-RPC messages are not supported", http.StatusBadRequest)
		return
	}

	profile, err := authn.Authenticate(r.Context(), r)
	if err != nil {
		writeAuthError(w, err)
		return
	}
	requestApp := a
	if profile != nil {
		requestApp = a.withProfile(profile)
		surface = surface.filteredForProfile(profile)
	}

	resp, reply := requestApp.handleRPCWithSurface(body, surface)
	if !reply {
		w.WriteHeader(http.StatusAccepted)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(resp)
}

func readMCPBody(w http.ResponseWriter, r *http.Request) ([]byte, error) {
	defer func() { _ = r.Body.Close() }()
	return io.ReadAll(http.MaxBytesReader(w, r.Body, 4<<20))
}

func acceptsStreamableHTTP(accept string) bool {
	accept = strings.ToLower(accept)
	return strings.Contains(accept, "application/json") && strings.Contains(accept, "text/event-stream")
}

func writeAuthError(w http.ResponseWriter, err error) {
	var authErr authHTTPError
	if errors.As(err, &authErr) {
		if authErr.status == http.StatusUnauthorized {
			w.Header().Set("WWW-Authenticate", "Bearer")
		}
		http.Error(w, authErr.message, authErr.status)
		return
	}
	http.Error(w, "authentication failed", http.StatusUnauthorized)
}
