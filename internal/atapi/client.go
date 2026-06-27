// Package atapi is a thin client for the Autotask PSA REST API: zone detection,
// header-based auth, and generic query/create/update/get helpers. It deals in
// map[string]any payloads so org-specific fields can be added without code
// changes, and handles query pagination transparently.
package atapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// zoneDetectURL is the fixed entry point used to discover an account's API zone.
const zoneDetectURL = "https://webservices.autotask.net/ATServicesRest/v1.0/zoneInformation"

// maxPageSize is Autotask's per-page query cap.
const maxPageSize = 500

var zoneDetectHTTPClient = &http.Client{Timeout: 60 * time.Second}

// Client talks to a single Autotask zone with a fixed set of credentials.
type Client struct {
	httpc           *http.Client
	base            string // zone base, e.g. https://webservices2.autotask.net/ATServicesRest/
	username        string
	secret          string
	integrationCode string
}

// New builds a client. baseURL is the zone base from DetectZone (or a cached one).
func New(baseURL, username, secret, integrationCode string) *Client {
	return &Client{
		httpc:           &http.Client{Timeout: 60 * time.Second},
		base:            baseURL,
		username:        username,
		secret:          secret,
		integrationCode: integrationCode,
	}
}

// DetectZone resolves the API base URL for the account that owns username.
// It needs no authentication.
func DetectZone(ctx context.Context, username string) (string, error) {
	return detectZone(ctx, zoneDetectURL, username)
}

// detectZone is the testable core of DetectZone with an injectable endpoint.
func detectZone(ctx context.Context, endpoint, username string) (string, error) {
	u := endpoint + "?user=" + url.QueryEscape(username)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, http.NoBody)
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := zoneDetectHTTPClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("zone detection: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("zone detection: read response: %w", err)
	}
	if resp.StatusCode >= 400 {
		return "", fmt.Errorf("zone detection failed (%d): %s", resp.StatusCode, apiError(data))
	}
	var z struct {
		ZoneName string `json:"zoneName"`
		URL      string `json:"url"`
	}
	if err := json.Unmarshal(data, &z); err != nil {
		return "", fmt.Errorf("zone detection: bad response: %w", err)
	}
	if z.URL == "" {
		return "", errors.New("zone detection: empty url in response")
	}
	return z.URL, nil
}

// Filter is one node of an Autotask query filter (leaf or grouped via Items).
type Filter struct {
	Op    string   `json:"op"`
	Field string   `json:"field,omitempty"`
	Value any      `json:"value,omitempty"`
	Items []Filter `json:"items,omitempty"`
}

type queryRequest struct {
	Filter     []Filter `json:"filter"`
	MaxRecords int      `json:"MaxRecords,omitempty"`
}

type queryResponse struct {
	Items       []map[string]any `json:"items"`
	PageDetails struct {
		Count       int    `json:"count"`
		NextPageURL string `json:"nextPageUrl"`
	} `json:"pageDetails"`
}

// Query runs a filtered query against an entity, following pagination until the
// result set is exhausted or limit rows are collected (limit <= 0 means all).
func (c *Client) Query(ctx context.Context, entity string, filter []Filter, limit int) ([]map[string]any, error) {
	if len(filter) == 0 {
		// Autotask requires a filter; "exist id" returns everything.
		filter = []Filter{{Op: "exist", Field: "id"}}
	}
	var all []map[string]any
	var resp queryResponse
	if err := c.do(ctx, http.MethodPost, c.entityURL(entity, "query"), queryRequest{Filter: filter, MaxRecords: maxPageSize}, &resp); err != nil {
		return nil, err
	}
	all = append(all, resp.Items...)
	next := resp.PageDetails.NextPageURL
	for next != "" && (limit <= 0 || len(all) < limit) {
		nextURL, err := c.validatedPaginationURL(next)
		if err != nil {
			return nil, err
		}
		var page queryResponse
		if err := c.do(ctx, http.MethodGet, nextURL, nil, &page); err != nil {
			return nil, err
		}
		all = append(all, page.Items...)
		next = page.PageDetails.NextPageURL
	}
	if limit > 0 && len(all) > limit {
		all = all[:limit]
	}
	return all, nil
}

type writeResponse struct {
	ItemID int64 `json:"itemId"`
}

// Create POSTs a new entity and returns its new id.
func (c *Client) Create(ctx context.Context, entity string, fields map[string]any) (int64, error) {
	var r writeResponse
	if err := c.do(ctx, http.MethodPost, c.entityURL(entity), fields, &r); err != nil {
		return 0, err
	}
	return r.ItemID, nil
}

// Update PATCHes an existing entity. fields must include the "id".
func (c *Client) Update(ctx context.Context, entity string, fields map[string]any) (int64, error) {
	var r writeResponse
	if err := c.do(ctx, http.MethodPatch, c.entityURL(entity), fields, &r); err != nil {
		return 0, err
	}
	return r.ItemID, nil
}

// GetByID fetches a single entity by id.
func (c *Client) GetByID(ctx context.Context, entity string, id int64) (map[string]any, error) {
	var r struct {
		Item map[string]any `json:"item"`
	}
	if err := c.do(ctx, http.MethodGet, c.entityURL(entity, strconv.FormatInt(id, 10)), nil, &r); err != nil {
		return nil, err
	}
	return r.Item, nil
}

func (c *Client) entityURL(entity string, suffix ...string) string {
	parts := append([]string{strings.TrimRight(c.base, "/"), "v1.0", entity}, suffix...)
	return strings.Join(parts, "/")
}

func (c *Client) validatedPaginationURL(next string) (string, error) {
	u, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("parse pagination URL: %w", err)
	}
	if !u.IsAbs() || u.User != nil {
		return "", errors.New("pagination URL must be absolute and must not include user info")
	}
	base, err := url.Parse(c.entityURL(""))
	if err != nil {
		return "", fmt.Errorf("parse Autotask base URL: %w", err)
	}
	if !strings.EqualFold(u.Scheme, base.Scheme) || !strings.EqualFold(u.Host, base.Host) {
		return "", fmt.Errorf("pagination URL host %q does not match Autotask host %q", u.Host, base.Host)
	}
	basePath := strings.TrimRight(base.EscapedPath(), "/") + "/"
	nextPath := strings.TrimRight(u.EscapedPath(), "/") + "/"
	if !strings.HasPrefix(nextPath, basePath) {
		return "", fmt.Errorf("pagination URL path %q is outside Autotask API path %q", u.EscapedPath(), base.EscapedPath())
	}
	return u.String(), nil
}

// do executes a request with auth headers and decodes a JSON response into out.
// fullURL may be an entity URL or an absolute pagination URL.
func (c *Client) do(ctx context.Context, method, fullURL string, body, out any) error {
	var payload []byte
	if body != nil {
		var err error
		if payload, err = json.Marshal(body); err != nil {
			return err
		}
	}
	tries := 1
	if retryableRead(method, fullURL) {
		tries += len(readRetryBackoffs)
	}
	var lastErr error
	for attempt := range tries {
		retry, delay, err := c.doOnce(ctx, method, fullURL, payload, body != nil, out, attempt)
		if !retry {
			return err
		}
		lastErr = err
		if attempt == tries-1 {
			break
		}
		if err := sleepContext(ctx, delay); err != nil {
			return err
		}
	}
	return lastErr
}

func (c *Client) doOnce(ctx context.Context, method, fullURL string, payload []byte, hasBody bool, out any, attempt int) (bool, time.Duration, error) {
	var rdr io.Reader
	if hasBody {
		rdr = bytes.NewReader(payload)
	}
	req, err := http.NewRequestWithContext(ctx, method, fullURL, rdr)
	if err != nil {
		return false, 0, err
	}
	req.Header.Set("ApiIntegrationCode", c.integrationCode)
	req.Header.Set("UserName", c.username)
	req.Header.Set("Secret", c.secret)
	req.Header.Set("Accept", "application/json")
	if hasBody {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpc.Do(req)
	if err != nil {
		if ctx.Err() == nil {
			return true, readRetryBackoff(attempt), err
		}
		return false, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, 0, fmt.Errorf("read response: %w", err)
	}
	if transientStatus(resp.StatusCode) {
		return true, retryDelay(resp, attempt), fmt.Errorf("autotask %s %s -> %d: %s", method, trimURL(fullURL), resp.StatusCode, apiError(data))
	}
	if resp.StatusCode >= 400 {
		return false, 0, fmt.Errorf("autotask %s %s -> %d: %s", method, trimURL(fullURL), resp.StatusCode, apiError(data))
	}
	if out != nil && len(data) > 0 {
		if err := json.Unmarshal(data, out); err != nil {
			return false, 0, fmt.Errorf("decode response: %w", err)
		}
	}
	return false, 0, nil
}

var readRetryBackoffs = []time.Duration{250 * time.Millisecond, 500 * time.Millisecond, time.Second}

func retryableRead(method, fullURL string) bool {
	if method == http.MethodGet {
		return true
	}
	path := strings.TrimRight(trimURL(fullURL), "/")
	return method == http.MethodPost && strings.HasSuffix(path, "/query")
}

func transientStatus(status int) bool {
	return status == http.StatusTooManyRequests ||
		status == http.StatusBadGateway ||
		status == http.StatusServiceUnavailable ||
		status == http.StatusGatewayTimeout
}

func retryDelay(resp *http.Response, attempt int) time.Duration {
	if v := strings.TrimSpace(resp.Header.Get("Retry-After")); v != "" {
		if seconds, err := strconv.Atoi(v); err == nil && seconds >= 0 {
			return time.Duration(seconds) * time.Second
		}
		if when, err := http.ParseTime(v); err == nil {
			if delay := time.Until(when); delay > 0 {
				return delay
			}
		}
	}
	return readRetryBackoff(attempt)
}

func readRetryBackoff(attempt int) time.Duration {
	if attempt < len(readRetryBackoffs) {
		return readRetryBackoffs[attempt]
	}
	return readRetryBackoffs[len(readRetryBackoffs)-1]
}

func sleepContext(ctx context.Context, delay time.Duration) error {
	if delay <= 0 {
		return nil
	}
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// apiError pulls human-readable messages out of an Autotask error body.
func apiError(data []byte) string {
	var e struct {
		Errors []string `json:"errors"`
	}
	if err := json.Unmarshal(data, &e); err == nil && len(e.Errors) > 0 {
		return strings.Join(e.Errors, "; ")
	}
	s := strings.TrimSpace(string(data))
	if s == "" {
		return "(empty response body)"
	}
	if len(s) > 500 {
		s = s[:500] + "…"
	}
	return s
}

func trimURL(u string) string {
	if i := strings.Index(u, "?"); i >= 0 {
		return u[:i]
	}
	return u
}
