package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDispatchRoutesViaRegistry(t *testing.T) {
	fc := &fakeClient{items: map[int64]map[string]any{5: {"id": float64(5), "title": "T"}}}
	app := newTestApp(t, fc)

	// Two-word command with a positional.
	res, err := app.dispatch([]string{"ticket", "show", "5"})
	if err != nil {
		t.Fatalf("ticket show: %v", err)
	}
	if res.action != "ticket.show" {
		t.Errorf("action = %q", res.action)
	}

	// Unknown subcommand of a known group.
	if _, err := app.dispatch([]string{"timer", "bogus"}); err == nil {
		t.Error("expected error for unknown timer subcommand")
	}
	// Unknown top-level command.
	if _, err := app.dispatch([]string{"nope"}); err == nil {
		t.Error("expected error for unknown command")
	}
}

func TestDescribeDataCoversRegistry(t *testing.T) {
	data := describeData()
	cmds, ok := data["commands"].([]command)
	if !ok || len(cmds) != len(commands) {
		t.Fatalf("describe commands = %T len %d", data["commands"], len(cmds))
	}
	// Spot-check a known command/flag.
	c := lookupCommand("time add")
	if c == nil {
		t.Fatal("time add missing from registry")
	}
	var windows *cmdFlag
	for i := range c.Flags {
		if c.Flags[i].Name == "windows" {
			windows = &c.Flags[i]
		}
	}
	if windows == nil || !windows.Required {
		t.Errorf("time add --windows should be required: %+v", windows)
	}
}

// TestRegistryFlagsAreDefined guards against drift: every flag declared in the
// registry must actually be defined by its handler (else describe/MCP would
// advertise a flag the CLI rejects).
func TestRegistryFlagsAreDefined(t *testing.T) {
	for _, c := range commands {
		if c.MCPHidden {
			continue // ui binds a socket; don't run it
		}
		app := newTestApp(t, &fakeClient{})
		argv := append(strings.Fields(c.Name), dummyArgv(c)...)
		_, err := app.dispatch(argv)
		if err != nil {
			msg := err.Error()
			if strings.Contains(msg, "not defined") || strings.Contains(msg, "unknown flag") {
				t.Errorf("%s: registry flag mismatch with handler: %v", c.Name, err)
			}
		}
	}
}

func dummyArgv(c command) []string {
	var pos, flags []string
	for _, f := range c.Flags {
		switch {
		case f.Positional:
			pos = append(pos, "1")
		case f.Type == "bool":
			flags = append(flags, "--"+f.Name)
		default:
			flags = append(flags, "--"+f.Name, "1")
		}
	}
	return append(pos, flags...)
}

func TestMCPInitialize(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	resp, reply := app.handleRPC([]byte(`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18"}}`))
	if !reply {
		t.Fatal("initialize should get a reply")
	}
	result, _ := resp.Result.(map[string]any)
	if result["protocolVersion"] != "2025-06-18" {
		t.Errorf("protocolVersion = %v (should echo client)", result["protocolVersion"])
	}
	si, _ := result["serverInfo"].(map[string]any)
	if si["name"] != "atem" {
		t.Errorf("serverInfo.name = %v", si["name"])
	}
}

func TestServeMCPStripsBOMAndResponds(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	in := append([]byte{0xEF, 0xBB, 0xBF}, []byte(
		`{"jsonrpc":"2.0","id":1,"method":"initialize"}`+"\n"+
			`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`+"\n")...)
	var out bytes.Buffer
	if code := app.serveMCP(bytes.NewReader(in), &out); code != 0 {
		t.Fatalf("exit code = %d", code)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 responses, got %d: %q", len(lines), out.String())
	}
	var first rpcResponse
	if err := json.Unmarshal([]byte(lines[0]), &first); err != nil {
		t.Fatalf("first response not JSON (BOM not stripped?): %v", err)
	}
	if first.Error != nil {
		t.Errorf("initialize errored: %+v", first.Error)
	}
}

func TestMCPNotificationGetsNoReply(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	if _, reply := app.handleRPC([]byte(`{"jsonrpc":"2.0","method":"notifications/initialized"}`)); reply {
		t.Error("notifications must not get a reply")
	}
}

func TestMCPToolsList(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	resp, _ := app.handleRPC([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`))
	result, _ := resp.Result.(map[string]any)
	tools, _ := result["tools"].([]map[string]any)
	if len(tools) == 0 {
		t.Fatal("no tools listed")
	}
	names := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	if !names["time_add"] || !names["ticket_create"] {
		t.Errorf("expected time_add and ticket_create tools; got %v", names)
	}
	if names["ui"] {
		t.Error("interactive ui must not be exposed as a tool")
	}
}

func TestM365MCPSurfaceFiltersTools(t *testing.T) {
	tools := mcpToolsFor(m365MCPSurface().commands)
	names := map[string]bool{}
	for _, tool := range tools {
		name, _ := tool["name"].(string)
		names[name] = true
	}
	want := []string{"company_search", "ticket_search", "ticket_show", "ticket_create", "time_add", "report"}
	for _, name := range want {
		if !names[name] {
			t.Errorf("M365 surface missing %s; got %v", name, names)
		}
	}
	blocked := []string{
		"company_alias", "resource_search", "ticket_close", "timer_start", "timer_stop", "config_show", "config_set", "config_doctor", "ui",
	}
	for _, name := range blocked {
		if names[name] {
			t.Errorf("M365 surface exposed blocked tool %s", name)
		}
	}
}

func TestM365MCPSurfaceRejectsBlockedToolCall(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	params, _ := json.Marshal(map[string]any{
		"name":      "ticket_close",
		"arguments": map[string]any{"id": 123, "dry-run": true},
	})
	if _, rerr := app.mcpToolsCallWithSurface(params, m365MCPSurface()); rerr == nil {
		t.Fatal("expected blocked M365 tool call to return an RPC error")
	}
}

func TestMCPToolsCallDryRun(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	app.cfg.Defaults.QueueID = 8
	params, _ := json.Marshal(map[string]any{
		"name":      "ticket_create",
		"arguments": map[string]any{"company": "123", "title": "x", "desc": "what it's about", "dry-run": true},
	})
	res, rerr := app.mcpToolsCall(params)
	if rerr != nil {
		t.Fatalf("rpc error: %v", rerr)
	}
	if res["isError"] != false {
		t.Errorf("isError = %v", res["isError"])
	}
	if res["structuredContent"] == nil {
		t.Error("expected structuredContent alongside the text envelope")
	}
	var r Result
	if err := json.Unmarshal([]byte(toolText(t, res)), &r); err != nil {
		t.Fatalf("content not JSON: %v", err)
	}
	if !r.OK || !r.DryRun || r.Action != "ticket.create" {
		t.Errorf("result = %+v", r)
	}
}

func TestMCPSchemaEnum(t *testing.T) {
	for _, tool := range mcpTools() {
		if tool["name"] != "report" {
			continue
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		props, _ := schema["properties"].(map[string]any)
		format, _ := props["format"].(map[string]any)
		enum, _ := format["enum"].([]string)
		if len(enum) != 2 || enum[0] != "json" || enum[1] != "md" {
			t.Errorf("report --format enum = %v", format["enum"])
		}
		return
	}
	t.Fatal("report tool not found")
}

func TestMCPOutputSchema(t *testing.T) {
	schemas := map[string]map[string]any{}
	for _, tool := range mcpTools() {
		name, _ := tool["name"].(string)
		schema, _ := tool["outputSchema"].(map[string]any)
		schemas[name] = schema
	}
	// A dry-run command exposes a oneOf of its live and dry-run shapes.
	if oneOf, _ := schemas["ticket_create"]["oneOf"].([]map[string]any); len(oneOf) != 2 {
		t.Errorf("ticket_create outputSchema should be a oneOf of 2: %v", schemas["ticket_create"])
	}
	// A typed command gets generated properties (reflection over the struct).
	props, _ := schemas["timer_status"]["properties"].(map[string]any)
	if _, ok := props["totalHours"]; !ok {
		t.Errorf("timer_status schema missing totalHours: %v", schemas["timer_status"])
	}
	// A dynamic command (raw Autotask object) gets a loose object schema.
	if _, ok := schemas["ticket_show"]["properties"]; ok {
		t.Errorf("ticket_show should be a loose schema, got %v", schemas["ticket_show"])
	}
}

func TestMCPResources(t *testing.T) {
	app := newTestApp(t, &fakeClient{})

	resp, _ := app.handleRPC([]byte(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`))
	result, _ := resp.Result.(map[string]any)
	list, _ := result["resources"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("resources = %d", len(list))
	}

	resp, _ = app.handleRPC([]byte(`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"atem://describe"}}`))
	res2, _ := resp.Result.(map[string]any)
	contents, _ := res2["contents"].([]map[string]any)
	if len(contents) != 1 {
		t.Fatalf("contents = %v", res2)
	}
	text, _ := contents[0]["text"].(string)
	if !strings.Contains(text, "time add") {
		t.Error("describe resource missing commands")
	}

	resp, _ = app.handleRPC([]byte(`{"jsonrpc":"2.0","id":3,"method":"resources/read","params":{"uri":"atem://config"}}`))
	if resp.Error != nil {
		t.Errorf("config resource errored: %v", resp.Error)
	}

	resp, _ = app.handleRPC([]byte(`{"jsonrpc":"2.0","id":4,"method":"resources/read","params":{"uri":"atem://nope"}}`))
	if resp.Error == nil {
		t.Error("expected error for unknown resource")
	}
}

func TestM365MCPResourcesExcludeConfig(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	surface := m365MCPSurface()

	resp, _ := app.handleRPCWithSurface([]byte(`{"jsonrpc":"2.0","id":1,"method":"resources/list"}`), surface)
	result, _ := resp.Result.(map[string]any)
	list, _ := result["resources"].([]map[string]any)
	for _, resource := range list {
		if resource["uri"] == resourceConfigURI {
			t.Fatal("M365 surface must not expose config resource")
		}
	}

	resp, _ = app.handleRPCWithSurface([]byte(`{"jsonrpc":"2.0","id":2,"method":"resources/read","params":{"uri":"atem://config"}}`), surface)
	if resp.Error == nil {
		t.Fatal("M365 config resource read should fail")
	}
}

func TestHTTPMCPToolsListM365(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()

	app.mcpHTTPHandler(m365MCPSurface(), noAuthenticator{}).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
	var resp struct {
		Result struct {
			Tools []struct {
				Name string `json:"name"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("response JSON: %v", err)
	}
	names := map[string]bool{}
	for _, tool := range resp.Result.Tools {
		names[tool.Name] = true
	}
	if !names["time_add"] {
		t.Fatalf("HTTP M365 tools missing time_add: %v", names)
	}
	if names["config_set"] || names["ticket_close"] {
		t.Fatalf("HTTP M365 tools leaked blocked tools: %v", names)
	}
}

func TestHTTPMCPNotificationAccepted(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","method":"notifications/initialized"}`))
	req.Header.Set("Accept", "application/json, text/event-stream")
	rr := httptest.NewRecorder()

	app.mcpHTTPHandler(m365MCPSurface(), noAuthenticator{}).ServeHTTP(rr, req)

	if rr.Code != http.StatusAccepted {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
	if body := strings.TrimSpace(rr.Body.String()); body != "" {
		t.Fatalf("notification should not return a body, got %q", body)
	}
}

func TestHTTPMCPRequiresStreamableAccept(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/mcp", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`))
	req.Header.Set("Accept", "application/json")
	rr := httptest.NewRecorder()

	app.mcpHTTPHandler(m365MCPSurface(), noAuthenticator{}).ServeHTTP(rr, req)

	if rr.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d body = %q", rr.Code, rr.Body.String())
	}
}

func TestHTTPHealthz(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/healthz", http.NoBody)
	rr := httptest.NewRecorder()

	app.mcpHTTPHandler(m365MCPSurface(), noAuthenticator{}).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK || strings.TrimSpace(rr.Body.String()) != `{"ok":true}` {
		t.Fatalf("healthz status/body = %d %q", rr.Code, rr.Body.String())
	}
}

func TestMCPPrompts(t *testing.T) {
	app := newTestApp(t, &fakeClient{})

	resp, _ := app.handleRPC([]byte(`{"jsonrpc":"2.0","id":1,"method":"prompts/list"}`))
	result, _ := resp.Result.(map[string]any)
	list, _ := result["prompts"].([]map[string]any)
	if len(list) != 2 {
		t.Fatalf("prompts = %d", len(list))
	}

	resp, _ = app.handleRPC([]byte(`{"jsonrpc":"2.0","id":2,"method":"prompts/get","params":{"name":"log_day","arguments":{"date":"2026-06-16","notes":"fixed printer"}}}`))
	res2, _ := resp.Result.(map[string]any)
	msgs, _ := res2["messages"].([]map[string]any)
	if len(msgs) != 1 {
		t.Fatalf("messages = %v", res2)
	}
	content, _ := msgs[0]["content"].(map[string]any)
	text, _ := content["text"].(string)
	if !strings.Contains(text, "2026-06-16") || !strings.Contains(text, "fixed printer") {
		t.Errorf("prompt text = %q", text)
	}

	resp, _ = app.handleRPC([]byte(`{"jsonrpc":"2.0","id":3,"method":"prompts/get","params":{"name":"nope"}}`))
	if resp.Error == nil {
		t.Error("expected error for unknown prompt")
	}
}

func TestUsageTextGeneratedFromRegistry(t *testing.T) {
	u := usageText()
	if !strings.Contains(u, "USAGE") {
		t.Error("missing USAGE header")
	}
	for _, c := range commands {
		if !strings.Contains(u, "atem "+c.Name) {
			t.Errorf("usage missing command %q", c.Name)
		}
	}
}

func TestMCPToolsCallUnknown(t *testing.T) {
	app := newTestApp(t, &fakeClient{})
	params, _ := json.Marshal(map[string]any{"name": "does_not_exist", "arguments": map[string]any{}})
	if _, rerr := app.mcpToolsCall(params); rerr == nil {
		t.Error("expected rpc error for unknown tool")
	}
}

func TestBuildArgv(t *testing.T) {
	// Flags: numbers render without decimals, true bools become bare flags, false
	// bools are omitted.
	c := lookupCommand("time add")
	argv, err := buildArgv(*c, map[string]any{
		"ticket": float64(5), "windows": "11-12", "dry-run": true, "close": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	got := strings.Join(argv, " ")
	if got != "--ticket 5 --windows 11-12 --dry-run" {
		t.Errorf("argv = %q", got)
	}

	// Positionals come first, in registry order.
	alias := lookupCommand("company alias")
	argv, err = buildArgv(*alias, map[string]any{"name": "acme", "companyId": float64(0)})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(argv, " ") != "acme 0" {
		t.Errorf("positional argv = %q", argv)
	}

	// Missing required argument is an error.
	if _, err := buildArgv(*c, map[string]any{}); err == nil {
		t.Error("expected error for missing required --windows")
	}
}

func toolText(t *testing.T, res map[string]any) string {
	t.Helper()
	content, ok := res["content"].([]map[string]any)
	if !ok || len(content) == 0 {
		t.Fatalf("no content: %v", res)
	}
	text, _ := content[0]["text"].(string)
	return text
}
