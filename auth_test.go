package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEntraValidatorValidatesSignedToken(t *testing.T) {
	key := testRSAKey(t)
	const (
		kid      = "test-key"
		tenantID = "tenant-1"
		audience = "api://atem-test"
		issuer   = "https://login.microsoftonline.com/tenant-1/v2.0"
	)
	metadataURL := testOIDCServer(t, key, kid, issuer)
	validator := newEntraValidator(tenantID, []string{audience}, metadataURL)
	validator.now = func() time.Time { return testNow }

	token := signedTestJWT(t, key, kid, map[string]any{
		"iss":                issuer,
		"tid":                tenantID,
		"oid":                "object-1",
		"aud":                audience,
		"exp":                testNow.Add(time.Hour).Unix(),
		"nbf":                testNow.Add(-time.Minute).Unix(),
		"preferred_username": "tech@example.com",
	})

	id, err := validator.Validate(context.Background(), token)
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if id.TenantID != tenantID || id.ObjectID != "object-1" || id.PreferredUsername != "tech@example.com" {
		t.Fatalf("identity = %+v", id)
	}
}

func TestEntraValidatorRejectsInvalidClaims(t *testing.T) {
	key := testRSAKey(t)
	const (
		kid      = "test-key"
		tenantID = "tenant-1"
		audience = "api://atem-test"
		issuer   = "https://login.microsoftonline.com/tenant-1/v2.0"
	)
	metadataURL := testOIDCServer(t, key, kid, issuer)
	validator := newEntraValidator(tenantID, []string{audience}, metadataURL)
	validator.now = func() time.Time { return testNow }

	tests := []struct {
		name   string
		claims map[string]any
	}{
		{
			name: "wrong audience",
			claims: map[string]any{
				"iss": issuer, "tid": tenantID, "oid": "object-1", "aud": "api://elsewhere", "exp": testNow.Add(time.Hour).Unix(),
			},
		},
		{
			name: "expired",
			claims: map[string]any{
				"iss": issuer, "tid": tenantID, "oid": "object-1", "aud": audience, "exp": testNow.Add(-time.Hour).Unix(),
			},
		},
		{
			name: "wrong tenant",
			claims: map[string]any{
				"iss": issuer, "tid": "tenant-2", "oid": "object-1", "aud": audience, "exp": testNow.Add(time.Hour).Unix(),
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := signedTestJWT(t, key, kid, tt.claims)
			if _, err := validator.Validate(context.Background(), token); err == nil {
				t.Fatal("expected token validation to fail")
			}
		})
	}
}

func TestHTTPMCPEntraProfileInjectsAutotaskIdentity(t *testing.T) {
	key := testRSAKey(t)
	const (
		kid      = "test-key"
		tenantID = "tenant-1"
		objectID = "object-1"
		audience = "api://atem-test"
		issuer   = "https://login.microsoftonline.com/tenant-1/v2.0"
	)
	metadataURL := testOIDCServer(t, key, kid, issuer)
	profiles := `[{"tenantId":"tenant-1","objectId":"object-1","resourceId":77,"roleId":88,"scopes":["ticket:create"]}]`
	authn, err := newMCPAuthenticator(authOptions{
		Mode:        "entra",
		TenantID:    tenantID,
		Audiences:   []string{audience},
		MetadataURL: metadataURL,
		Profiles:    profiles,
	})
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	if b, ok := authn.(*bearerAuthenticator); ok {
		b.validator.now = func() time.Time { return testNow }
	}
	app := newTestApp(t, &fakeClient{})
	app.cfg.Defaults.QueueID = 55

	token := signedTestJWT(t, key, kid, map[string]any{
		"iss": issuer,
		"tid": tenantID,
		"oid": objectID,
		"aud": audience,
		"exp": testNow.Add(time.Hour).Unix(),
	})
	req := httptest.NewRequestWithContext(
		context.Background(),
		http.MethodPost,
		"/mcp",
		strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"ticket_create","arguments":{"company":"123","title":"x","desc":"what it's about","dry-run":true}}}`),
	)
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+token)
	rr := httptest.NewRecorder()

	app.mcpHTTPHandler(m365MCPSurface(), authn).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
	var rpc struct {
		Result struct {
			StructuredContent struct {
				Fields map[string]any `json:"fields"`
			} `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &rpc); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	if asInt64(rpc.Result.StructuredContent.Fields["assignedResourceID"]) != 77 {
		t.Fatalf("assignedResourceID = %v", rpc.Result.StructuredContent.Fields["assignedResourceID"])
	}
	if asInt64(rpc.Result.StructuredContent.Fields["assignedResourceRoleID"]) != 88 {
		t.Fatalf("assignedResourceRoleID = %v", rpc.Result.StructuredContent.Fields["assignedResourceRoleID"])
	}
}

func TestHTTPMCPEntraRequiresBearerToken(t *testing.T) {
	authn, err := newMCPAuthenticator(authOptions{
		Mode:      "entra",
		TenantID:  "tenant-1",
		Audiences: []string{"api://atem-test"},
		Profiles:  `[{"tenantId":"tenant-1","objectId":"object-1","resourceId":77,"roleId":88,"scopes":["*"]}]`,
	})
	if err != nil {
		t.Fatalf("authenticator: %v", err)
	}
	app := newTestApp(t, &fakeClient{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()

	app.mcpHTTPHandler(m365MCPSurface(), authn).ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
}

func TestProfileStoreLoadsFromFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "profiles.json")
	if err := os.WriteFile(path, []byte(`[{"tenantId":"tenant-1","objectId":"object-1","resourceId":77,"roleId":88,"scopes":["*"]}]`), 0o600); err != nil {
		t.Fatal(err)
	}
	store, err := loadProfileStore("", path)
	if err != nil {
		t.Fatalf("loadProfileStore: %v", err)
	}
	profile, ok := store.lookup(authIdentity{TenantID: "tenant-1", ObjectID: "object-1"})
	if !ok || profile.ResourceID != 77 || !profile.hasScope("time:add") {
		t.Fatalf("profile lookup = %+v ok=%v", profile, ok)
	}
}

func TestProfileStoreRejectsMissingScopes(t *testing.T) {
	if _, err := loadProfileStore(`[{"tenantId":"tenant-1","objectId":"object-1","resourceId":77,"roleId":88}]`, ""); err == nil {
		t.Fatal("expected missing scopes to fail")
	}
}

func TestNewMCPAuthenticatorConfigValidation(t *testing.T) {
	if _, err := newMCPAuthenticator(authOptions{Mode: "none"}); err != nil {
		t.Fatalf("none auth should be valid: %v", err)
	}
	if _, err := newMCPAuthenticator(authOptions{Mode: "entra", Audiences: []string{"api://atem-test"}}); err == nil {
		t.Fatal("expected missing tenant to fail")
	}
	if _, err := newMCPAuthenticator(authOptions{Mode: "bogus"}); err == nil {
		t.Fatal("expected bad auth mode to fail")
	}
}

func TestAuthorizeCommandRequiresProfileScope(t *testing.T) {
	app := newTestApp(t, &fakeClient{}).withProfile(&TechnicianProfile{
		TenantID: "tenant-1", ObjectID: "object-1", ResourceID: 77, RoleID: 88, Scopes: []string{"ticket:read"},
	})
	params, _ := json.Marshal(map[string]any{
		"name":      "ticket_create",
		"arguments": map[string]any{"company": "123", "title": "x", "desc": "what it's about", "dry-run": true},
	})
	res, rerr := app.mcpToolsCallWithSurface(params, m365MCPSurface())
	if rerr != nil {
		t.Fatalf("rpc error: %v", rerr)
	}
	if res["isError"] != true {
		t.Fatalf("expected scope failure, got %v", res)
	}
}

func TestValidateFetchURL(t *testing.T) {
	if err := validateFetchURL("https://login.microsoftonline.com/tenant/v2.0/.well-known/openid-configuration"); err != nil {
		t.Fatalf("https URL rejected: %v", err)
	}
	if err := validateFetchURL("http://localhost/metadata"); err != nil {
		t.Fatalf("localhost URL rejected: %v", err)
	}
	if err := validateFetchURL("http://169.254.169.254/metadata"); err == nil {
		t.Fatal("expected non-local http URL to fail")
	}
}

func TestM365ToolsListIsFilteredByProfileScopes(t *testing.T) {
	surface := m365MCPSurface().filteredForProfile(&TechnicianProfile{
		TenantID: "tenant-1", ObjectID: "object-1", ResourceID: 77, RoleID: 88, Scopes: []string{"ticket:read"},
	})
	tools := mcpToolsFor(surface.commands)
	names := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	if !names["ticket_search"] || !names["ticket_show"] {
		t.Fatalf("ticket read tools missing: %v", names)
	}
	if names["ticket_create"] || names["time_add"] || names["report"] {
		t.Fatalf("profile scopes leaked write/report tools: %v", names)
	}
}

func testOIDCServer(t *testing.T, key *rsa.PrivateKey, kid, issuer string) string {
	t.Helper()
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/.well-known/openid-configuration":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"issuer":   issuer,
				"jwks_uri": server.URL + "/keys",
			})
		case "/keys":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"keys": []map[string]any{{
					"kty": "RSA",
					"kid": kid,
					"n":   base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
					"e":   base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.E)).Bytes()),
				}},
			})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server.URL + "/.well-known/openid-configuration"
}

func testRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("generate RSA key: %v", err)
	}
	return key
}

func signedTestJWT(t *testing.T, key *rsa.PrivateKey, kid string, claims map[string]any) string {
	t.Helper()
	header := map[string]any{"alg": "RS256", "typ": "JWT", "kid": kid}
	signingInput := base64.RawURLEncoding.EncodeToString(mustJSON(t, header)) + "." + base64.RawURLEncoding.EncodeToString(mustJSON(t, claims))
	hash := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, hash[:])
	if err != nil {
		t.Fatalf("sign JWT: %v", err)
	}
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func mustJSON(t *testing.T, v any) []byte {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal JSON: %v", err)
	}
	return b
}
