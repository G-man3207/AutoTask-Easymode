package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const defaultEntraAuthority = "https://login.microsoftonline.com"

// TechnicianProfile is the server-side identity binding between a Microsoft
// Entra user and the Autotask resource/role that atem is allowed to use.
type TechnicianProfile struct {
	TenantID   string   `json:"tenantId"`
	ObjectID   string   `json:"objectId"`
	UPN        string   `json:"upn,omitempty"`
	Email      string   `json:"email,omitempty"`
	ResourceID int      `json:"resourceId"`
	RoleID     int      `json:"roleId"`
	Scopes     []string `json:"scopes,omitempty"`
}

func (p *TechnicianProfile) hasScope(scope string) bool {
	return slices.Contains(p.Scopes, "*") || slices.Contains(p.Scopes, scope)
}

type authIdentity struct {
	TenantID          string
	ObjectID          string
	PreferredUsername string
	Email             string
}

type profileStore struct {
	bySubject map[string]TechnicianProfile
}

func loadProfileStore(raw, path string) (*profileStore, error) {
	if strings.TrimSpace(raw) == "" && strings.TrimSpace(path) != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, fmt.Errorf("read profile file: %w", err)
		}
		raw = string(data)
	}
	if strings.TrimSpace(raw) == "" {
		return nil, hinted("set ATEM_AUTH_PROFILES or ATEM_AUTH_PROFILE_FILE", "no technician profiles configured")
	}
	var profiles []TechnicianProfile
	if err := json.Unmarshal([]byte(raw), &profiles); err != nil {
		return nil, hinted("profiles must be a JSON array", "invalid technician profile JSON: %v", err)
	}
	store := &profileStore{bySubject: map[string]TechnicianProfile{}}
	for _, p := range profiles {
		if strings.TrimSpace(p.TenantID) == "" || strings.TrimSpace(p.ObjectID) == "" {
			return nil, hinted("each profile needs tenantId and objectId", "technician profile is missing Entra subject")
		}
		if p.ResourceID == 0 || p.RoleID == 0 {
			return nil, hinted("each profile needs resourceId and roleId", "technician profile %s is missing Autotask ids", profileKey(p.TenantID, p.ObjectID))
		}
		if len(p.Scopes) == 0 {
			return nil, hinted(`add scopes such as ["ticket:create","time:add","report:read"] or ["*"]`, "technician profile %s has no scopes", profileKey(p.TenantID, p.ObjectID))
		}
		store.bySubject[profileKey(p.TenantID, p.ObjectID)] = p
	}
	return store, nil
}

func (s *profileStore) lookup(id authIdentity) (*TechnicianProfile, bool) {
	p, ok := s.bySubject[profileKey(id.TenantID, id.ObjectID)]
	if !ok {
		return nil, false
	}
	return &p, true
}

func profileKey(tenantID, objectID string) string {
	return strings.ToLower(strings.TrimSpace(tenantID)) + ":" + strings.ToLower(strings.TrimSpace(objectID))
}

func mcpScopeForCommand(name string) string {
	switch name {
	case "company search":
		return "company:read"
	case "contact search":
		return "company:read"
	case "contact create":
		return "contact:create"
	case "ticket search", "ticket issue-types", "ticket show":
		return "ticket:read"
	case "ticket create":
		return "ticket:create"
	case "time add":
		return "time:add"
	case "report":
		return "report:read"
	default:
		return ""
	}
}

func (s mcpSurface) filteredForProfile(profile *TechnicianProfile) mcpSurface {
	if profile == nil {
		return s
	}
	filtered := s
	filtered.commands = make([]command, 0, len(s.commands))
	for i := range s.commands {
		c := s.commands[i]
		scope := mcpScopeForCommand(c.Name)
		if scope == "" || profile.hasScope(scope) {
			filtered.commands = append(filtered.commands, c)
		}
	}
	return filtered
}

func (a *App) authorizeCommand(c command) error {
	if a.profile == nil {
		return nil
	}
	scope := mcpScopeForCommand(c.Name)
	if scope == "" || a.profile.hasScope(scope) {
		return nil
	}
	return hinted("ask an atem administrator to add the required scope to your profile", "profile is not allowed to run %q (needs %s)", c.Name, scope)
}

type authOptions struct {
	Mode        string
	TenantID    string
	Audiences   []string
	MetadataURL string
	Profiles    string
	ProfileFile string
}

type mcpAuthenticator interface {
	Authenticate(context.Context, *http.Request) (*TechnicianProfile, error)
}

type noAuthenticator struct{}

func (noAuthenticator) Authenticate(context.Context, *http.Request) (*TechnicianProfile, error) {
	return nil, nil
}

type bearerAuthenticator struct {
	validator *entraValidator
	profiles  *profileStore
}

func (a *bearerAuthenticator) Authenticate(ctx context.Context, r *http.Request) (*TechnicianProfile, error) {
	token, err := bearerToken(r.Header.Get("Authorization"))
	if err != nil {
		return nil, authHTTPError{status: http.StatusUnauthorized, message: err.Error()}
	}
	id, err := a.validator.Validate(ctx, token)
	if err != nil {
		return nil, authHTTPError{status: http.StatusUnauthorized, message: err.Error()}
	}
	profile, ok := a.profiles.lookup(*id)
	if !ok {
		return nil, authHTTPError{status: http.StatusForbidden, message: "no atem profile for authenticated user"}
	}
	return profile, nil
}

type authHTTPError struct {
	status  int
	message string
}

func (e authHTTPError) Error() string { return e.message }

func bearerToken(header string) (string, error) {
	scheme, token, ok := strings.Cut(strings.TrimSpace(header), " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || strings.TrimSpace(token) == "" {
		return "", errors.New("missing bearer token")
	}
	return strings.TrimSpace(token), nil
}

func newMCPAuthenticator(opts authOptions) (mcpAuthenticator, error) {
	switch strings.ToLower(strings.TrimSpace(opts.Mode)) {
	case "", "none":
		return noAuthenticator{}, nil
	case "entra":
		if strings.TrimSpace(opts.TenantID) == "" {
			return nil, hinted("set ATEM_ENTRA_TENANT_ID", "missing Entra tenant id")
		}
		if len(opts.Audiences) == 0 {
			return nil, hinted("set ATEM_ENTRA_AUDIENCE", "missing Entra token audience")
		}
		profiles, err := loadProfileStore(opts.Profiles, opts.ProfileFile)
		if err != nil {
			return nil, err
		}
		return &bearerAuthenticator{
			validator: newEntraValidator(opts.TenantID, opts.Audiences, opts.MetadataURL),
			profiles:  profiles,
		}, nil
	default:
		return nil, hinted("use --auth none or --auth entra", "unknown auth mode %q", opts.Mode)
	}
}

type entraValidator struct {
	tenantID    string
	audiences   []string
	metadataURL string
	now         func() time.Time
	httpClient  *http.Client

	mu       sync.Mutex
	verifier *oidc.IDTokenVerifier
}

func newEntraValidator(tenantID string, audiences []string, metadataURL string) *entraValidator {
	return &entraValidator{
		tenantID:    strings.TrimSpace(tenantID),
		audiences:   audiences,
		metadataURL: strings.TrimSpace(metadataURL),
		now:         time.Now,
		httpClient:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (v *entraValidator) Validate(ctx context.Context, token string) (*authIdentity, error) {
	verifier, err := v.tokenVerifier(ctx)
	if err != nil {
		return nil, err
	}
	idToken, err := verifier.Verify(oidc.ClientContext(ctx, v.httpClient), token)
	if err != nil {
		return nil, fmt.Errorf("invalid token: %w", err)
	}
	if !hasAllowedAudience(idToken.Audience, v.audiences) {
		return nil, errors.New("token audience does not match atem")
	}
	var claims entraClaims
	if err := idToken.Claims(&claims); err != nil {
		return nil, fmt.Errorf("decode token claims: %w", err)
	}
	if err := v.validateClaims(claims); err != nil {
		return nil, err
	}
	id := &authIdentity{
		TenantID:          claims.TenantID,
		ObjectID:          claims.ObjectID,
		PreferredUsername: claims.PreferredUsername,
		Email:             claims.Email,
	}
	return id, nil
}

func (v *entraValidator) tokenVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	v.mu.Lock()
	cached := v.verifier
	v.mu.Unlock()
	if cached != nil {
		return cached, nil
	}
	verifier, err := v.buildVerifier(ctx)
	if err != nil {
		return nil, err
	}
	v.mu.Lock()
	if v.verifier == nil {
		v.verifier = verifier
	}
	cached = v.verifier
	v.mu.Unlock()
	return cached, nil
}

func (v *entraValidator) buildVerifier(ctx context.Context) (*oidc.IDTokenVerifier, error) {
	metadataURL := v.metadataURL
	if metadataURL == "" {
		metadataURL = defaultEntraAuthority + "/" + v.tenantID + "/v2.0/.well-known/openid-configuration"
	}
	var meta oidcMetadata
	if err := getJSON(ctx, v.httpClient, metadataURL, &meta); err != nil {
		return nil, fmt.Errorf("fetch Entra metadata: %w", err)
	}
	if meta.Issuer == "" || meta.JWKSURI == "" {
		return nil, errors.New("entra metadata missing issuer or jwks_uri")
	}
	if err := validateFetchURL(meta.JWKSURI); err != nil {
		return nil, fmt.Errorf("validate Entra signing keys URL: %w", err)
	}
	keySet := oidc.NewRemoteKeySet(oidc.ClientContext(ctx, v.httpClient), meta.JWKSURI)
	return oidc.NewVerifier(meta.Issuer, keySet, &oidc.Config{
		SkipClientIDCheck:    true,
		SupportedSigningAlgs: []string{"RS256"},
		Now:                  v.now,
	}), nil
}

func (v *entraValidator) validateClaims(claims entraClaims) error {
	if !strings.EqualFold(claims.TenantID, v.tenantID) {
		return errors.New("token was issued by an unexpected tenant")
	}
	if claims.ObjectID == "" {
		return errors.New("token has no object id")
	}
	return nil
}

func getJSON(ctx context.Context, client *http.Client, rawURL string, out any) error {
	if err := validateFetchURL(rawURL); err != nil {
		return err
	}
	//nolint:gosec // OIDC metadata/JWKS URLs are validated before fetch; tests use localhost HTTP.
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, http.NoBody)
	if err != nil {
		return err
	}
	//nolint:gosec // Required for Entra OIDC/JWKS discovery after validateFetchURL restricts scheme/host.
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func validateFetchURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil {
		return fmt.Errorf("parse oidc URL: %w", err)
	}
	if u.User != nil || u.Hostname() == "" {
		return errors.New("oidc URL must not include user info and must have a host")
	}
	if u.Scheme == "https" {
		return nil
	}
	if u.Scheme == "http" {
		host := strings.ToLower(u.Hostname())
		ip := net.ParseIP(host)
		if host == "localhost" || (ip != nil && ip.IsLoopback()) {
			return nil
		}
	}
	return errors.New("oidc URL must use https")
}

type oidcMetadata struct {
	Issuer  string `json:"issuer"`
	JWKSURI string `json:"jwks_uri"`
}

type entraClaims struct {
	TenantID          string `json:"tid"`
	ObjectID          string `json:"oid"`
	PreferredUsername string `json:"preferred_username"`
	UPN               string `json:"upn"`
	Email             string `json:"email"`
}

func (c *entraClaims) UnmarshalJSON(data []byte) error {
	type alias entraClaims
	var out alias
	if err := json.Unmarshal(data, &out); err != nil {
		return err
	}
	if out.Email == "" {
		out.Email = out.UPN
	}
	*c = entraClaims(out)
	return nil
}

func hasAllowedAudience(tokenAudiences, allowed []string) bool {
	for _, aud := range tokenAudiences {
		if slices.Contains(allowed, aud) {
			return true
		}
	}
	return false
}

func splitCSV(s string) []string {
	var out []string
	for _, part := range strings.Split(s, ",") {
		if trimmed := strings.TrimSpace(part); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
