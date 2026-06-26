package main

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// utf8BOM is tolerated at the start of a line: a correct MCP client never sends
// it, but some Windows pipes prepend one and it would otherwise break parsing.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// mcpProtocolVersion is the MCP revision atem defaults to when a client does not
// request one. atem echoes the client's requested version when present.
const mcpProtocolVersion = "2024-11-05"

// mcpSurface is the command/resource/prompt surface exposed to one class of MCP
// client. Local stdio agents get the full CLI-shaped surface; remote Microsoft
// 365 Copilot gets a deliberately smaller set that excludes local/admin tools.
type mcpSurface struct {
	commands              []command
	includeConfigResource bool
	prompts               []mcpPrompt
}

func localMCPSurface() mcpSurface {
	return mcpSurface{commands: commands, includeConfigResource: true, prompts: mcpPromptList}
}

var m365MCPCommandNames = map[string]bool{
	"company search":     true,
	"contact search":     true,
	"contact create":     true,
	"ticket search":      true,
	"ticket issue-types": true,
	"ticket show":        true,
	"ticket create":      true,
	"time add":           true,
	"report":             true,
}

func m365MCPSurface() mcpSurface {
	var safe []command
	for _, c := range commands {
		if m365MCPCommandNames[c.Name] {
			safe = append(safe, c)
		}
	}
	return mcpSurface{commands: safe, prompts: mcpPromptList}
}

// JSON-RPC 2.0 envelopes (the subset MCP over stdio uses).
type rpcRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"` // absent => notification (no reply)
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type rpcResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   *rpcError       `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// serveMCP runs the MCP server loop over newline-delimited JSON-RPC, exposing
// the command registry as tools. It blocks until stdin closes and returns a
// process exit code.
func (a *App) serveMCP(in io.Reader, out io.Writer) int {
	r := bufio.NewReader(in)
	enc := json.NewEncoder(out)
	enc.SetEscapeHTML(false)
	surface := localMCPSurface()
	for {
		line, err := r.ReadBytes('\n')
		line = bytes.TrimPrefix(line, utf8BOM)
		if strings.TrimSpace(string(line)) != "" {
			if resp, reply := a.handleRPCWithSurface(line, surface); reply {
				_ = enc.Encode(resp)
			}
		}
		if err != nil {
			return 0
		}
	}
}

// handleRPC processes one JSON-RPC message and returns the response to send (or
// reply=false for notifications, which get no response).
func (a *App) handleRPC(line []byte) (rpcResponse, bool) {
	return a.handleRPCWithSurface(line, localMCPSurface())
}

func (a *App) handleRPCWithSurface(line []byte, surface mcpSurface) (rpcResponse, bool) {
	var req rpcRequest
	if err := json.Unmarshal(line, &req); err != nil {
		return rpcResponse{JSONRPC: "2.0", Error: &rpcError{Code: -32700, Message: "parse error"}}, true
	}
	if len(req.ID) == 0 {
		return rpcResponse{}, false // notification (e.g. notifications/initialized)
	}

	resp := rpcResponse{JSONRPC: "2.0", ID: req.ID}
	switch req.Method {
	case "initialize":
		resp.Result = initializeResult(req.Params)
	case "ping":
		resp.Result = map[string]any{}
	case "tools/list":
		resp.Result = map[string]any{"tools": mcpToolsFor(surface.commands)}
	case "tools/call":
		result, rerr := a.mcpToolsCallWithSurface(req.Params, surface)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
	case "resources/list":
		resp.Result = map[string]any{"resources": mcpResourcesFor(surface)}
	case "resources/read":
		result, rerr := a.mcpResourcesReadWithSurface(req.Params, surface)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
	case "prompts/list":
		resp.Result = map[string]any{"prompts": mcpPromptsFor(surface.prompts)}
	case "prompts/get":
		result, rerr := mcpPromptsGetFrom(req.Params, surface.prompts)
		if rerr != nil {
			resp.Error = rerr
		} else {
			resp.Result = result
		}
	default:
		resp.Error = &rpcError{Code: -32601, Message: "method not found: " + req.Method}
	}
	return resp, true
}

func initializeResult(params json.RawMessage) map[string]any {
	pv := mcpProtocolVersion
	var p struct {
		ProtocolVersion string `json:"protocolVersion"`
	}
	if json.Unmarshal(params, &p) == nil && p.ProtocolVersion != "" {
		pv = p.ProtocolVersion
	}
	return map[string]any{
		"protocolVersion": pv,
		"capabilities": map[string]any{
			"tools":     map[string]any{},
			"resources": map[string]any{},
			"prompts":   map[string]any{},
		},
		"serverInfo": map[string]any{"name": "atem", "version": version},
	}
}

// mcpToolName converts a command name to a tool name ("ticket create" ->
// "ticket_create"), since MCP tool names cannot contain spaces.
func mcpToolName(name string) string { return strings.ReplaceAll(name, " ", "_") }

// mcpTools builds the tools/list payload from the registry.
func mcpTools() []map[string]any {
	return mcpToolsFor(localMCPSurface().commands)
}

func mcpToolsFor(cmds []command) []map[string]any {
	tools := make([]map[string]any, 0, len(cmds))
	for _, c := range cmds {
		if c.MCPHidden {
			continue
		}
		desc := c.Summary
		if c.Example != "" {
			desc += "\nExample: atem " + c.Example
		}
		tools = append(tools, map[string]any{
			"name":         mcpToolName(c.Name),
			"description":  desc,
			"inputSchema":  mcpInputSchema(c),
			"outputSchema": outputSchemaFor(c),
			"annotations": map[string]any{
				"title":           c.Name,
				"readOnlyHint":    c.ReadOnly,
				"destructiveHint": c.Destructive,
			},
		})
	}
	return tools
}

// mcpInputSchema renders a command's flags as a JSON Schema object.
func mcpInputSchema(c command) map[string]any {
	props := map[string]any{}
	var required []string
	for _, f := range c.Flags {
		prop := map[string]any{"description": f.Desc}
		switch f.Type {
		case "int", "int64", "float":
			prop["type"] = "number"
		case "bool":
			prop["type"] = "boolean"
		default:
			prop["type"] = "string"
		}
		if len(f.Enum) > 0 {
			prop["enum"] = f.Enum
		}
		props[f.Name] = prop
		if f.Required {
			required = append(required, f.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	return schema
}

// mcpToolsCall executes a tool by translating its arguments into a CLI argv and
// running the same handler the CLI uses, so behavior and write-guards match.
func (a *App) mcpToolsCall(params json.RawMessage) (map[string]any, *rpcError) {
	return a.mcpToolsCallWithSurface(params, localMCPSurface())
}

func (a *App) mcpToolsCallWithSurface(params json.RawMessage, surface mcpSurface) (map[string]any, *rpcError) {
	var p struct {
		Name      string         `json:"name"`
		Arguments map[string]any `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	c := commandByToolNameFor(p.Name, surface.commands)
	if c == nil {
		return nil, &rpcError{Code: -32602, Message: "unknown tool: " + p.Name}
	}
	if err := a.authorizeCommand(*c); err != nil {
		return mcpContent(resultText(resultFromError(err)), true), nil
	}
	argv, err := buildArgv(*c, p.Arguments)
	if err != nil {
		return mcpContent(resultText(resultFromError(err)), true), nil
	}
	res, runErr := c.run(a, argv)
	if runErr != nil {
		return mcpContent(resultText(resultFromError(runErr)), true), nil
	}
	out := mcpContent(resultText(Result{OK: true, Action: res.action, DryRun: res.dryRun, Data: res.data}), false)
	if res.data != nil {
		// Machine-readable copy of the payload alongside the text envelope.
		out["structuredContent"] = res.data
	}
	return out, nil
}

func commandByToolNameFor(tool string, cmds []command) *command {
	for i := range cmds {
		if !cmds[i].MCPHidden && mcpToolName(cmds[i].Name) == tool {
			return &cmds[i]
		}
	}
	return nil
}

// buildArgv turns MCP tool arguments into a CLI argument vector. Positional
// arguments come first (in registry order), then --flags.
func buildArgv(c command, args map[string]any) ([]string, error) {
	var positionals, flags []string
	for _, f := range c.Flags {
		v, ok := args[f.Name]
		if !ok {
			if f.Required {
				return nil, hinted("provide it in the tool arguments", "missing required argument %q", f.Name)
			}
			continue
		}
		if f.Positional {
			positionals = append(positionals, argString(v))
			continue
		}
		if f.Type == "bool" {
			if b, _ := v.(bool); b {
				flags = append(flags, "--"+f.Name)
			}
			continue
		}
		flags = append(flags, "--"+f.Name, argString(v))
	}
	return append(positionals, flags...), nil
}

// argString coerces a JSON-decoded value to a CLI token. Whole-number floats
// render without a decimal point so int flags parse cleanly.
func argString(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		if x == float64(int64(x)) {
			return strconv.FormatInt(int64(x), 10)
		}
		return strconv.FormatFloat(x, 'f', -1, 64)
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", x)
	}
}

func mcpContent(text string, isError bool) map[string]any {
	return map[string]any{
		"content": []map[string]any{{"type": "text", "text": text}},
		"isError": isError,
	}
}

// resultText renders a Result as the same JSON object the CLI prints, so an MCP
// caller sees the identical { ok, action, data | error, hint } envelope.
func resultText(r Result) string {
	b, err := json.MarshalIndent(r, "", "  ")
	if err != nil {
		return `{"ok":false,"error":"failed to encode result"}`
	}
	return string(b)
}

// --- resources -------------------------------------------------------------

const (
	resourceDescribeURI = "atem://describe"
	resourceConfigURI   = "atem://config"
)

func mcpResourcesFor(surface mcpSurface) []map[string]any {
	resources := []map[string]any{
		{"uri": resourceDescribeURI, "name": "commands", "mimeType": "application/json", "description": "Catalog of every command and flag (same as `atem describe`)."},
	}
	if surface.includeConfigResource {
		resources = append(resources, map[string]any{
			"uri": resourceConfigURI, "name": "config", "mimeType": "application/json", "description": "Current configuration, secrets redacted.",
		})
	}
	return resources
}

func (a *App) mcpResourcesReadWithSurface(params json.RawMessage, surface mcpSurface) (map[string]any, *rpcError) {
	var p struct {
		URI string `json:"uri"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	var payload any
	switch p.URI {
	case resourceDescribeURI:
		payload = map[string]any{"version": version, "commands": surface.commands}
	case resourceConfigURI:
		if !surface.includeConfigResource {
			return nil, &rpcError{Code: -32602, Message: "unknown resource: " + p.URI}
		}
		payload = a.configData()
	default:
		return nil, &rpcError{Code: -32602, Message: "unknown resource: " + p.URI}
	}
	text, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return nil, &rpcError{Code: -32603, Message: "failed to encode resource"}
	}
	return map[string]any{
		"contents": []map[string]any{
			{"uri": p.URI, "mimeType": "application/json", "text": string(text)},
		},
	}, nil
}

// --- prompts ---------------------------------------------------------------

// mcpPrompt is a reusable workflow template. render fills it from the supplied
// arguments; the text bakes in atem's conventions so the agent drives correctly.
type mcpPrompt struct {
	name        string
	description string
	arguments   []map[string]any
	render      func(map[string]string) string
}

var mcpPromptList = []mcpPrompt{
	{
		name:        "log_day",
		description: "Log a day of work as Autotask time entries from a freeform description.",
		arguments: []map[string]any{
			{"name": "date", "description": "day worked, YYYY-MM-DD (default: today)", "required": false},
			{"name": "notes", "description": "freeform description of what you did", "required": false},
		},
		render: func(a map[string]string) string {
			date := a["date"]
			if date == "" {
				date = "today"
			}
			return "Help me log my work in Autotask for " + date + ".\n\n" +
				"What I did:\n" + a["notes"] + "\n\n" +
				"Guidance:\n" +
				"- Use the atem tools. For split times (e.g. 11-12 and 13-15) use time_add with " +
				"--windows so each window becomes its own entry — never one merged block.\n" +
				"- One ticket per distinct task; search for an existing ticket and attach to it, " +
				"else create one. company id 0 is valid (the owner org).\n" +
				"- If the work involved a customer contact or the user mentions who they spoke with, " +
				"ask who it was if needed, use contact_search within the same company as the ticket, " +
				"and pass that contact id when creating the ticket. Never reuse a contact id from another company. " +
				"If the contact is missing, ask before creating it and collect first name, last name, and email. " +
				"Omit contact when no person is known or the work is internal/system-only.\n" +
				"- When creating a ticket, treat issue-type/sub-issue-type as expected, not optional. " +
				"Use ticket_issue-types to choose them. Omit them only for genuinely unclear or unusual cases; " +
				"ask first if it is ambiguous.\n" +
				"- Every time entry needs a note. Preview writes with dry-run and confirm with me " +
				"before logging anything."
		},
	},
	{
		name:        "weekly_report",
		description: "Summarize a week of logged time for a company into a customer-ready report.",
		arguments: []map[string]any{
			{"name": "company", "description": "customer alias or companyID", "required": false},
			{"name": "from", "description": "start date YYYY-MM-DD", "required": false},
			{"name": "to", "description": "end date YYYY-MM-DD", "required": false},
		},
		render: func(a map[string]string) string {
			return "Produce a customer-ready summary of logged time" +
				argClause(" for company ", a["company"]) +
				argClause(" from ", a["from"]) + argClause(" to ", a["to"]) + ".\n\n" +
				"Use the report tool (e.g. report --company ... --from ... --to ... --format md). " +
				"Act on the JSON `flagged` list: if any entries are flagged, tell me which and offer " +
				"to break those lumps into a per-day breakdown before exporting."
		},
	},
}

func argClause(prefix, val string) string {
	if strings.TrimSpace(val) == "" {
		return ""
	}
	return prefix + val
}

func mcpPromptsFor(prompts []mcpPrompt) []map[string]any {
	out := make([]map[string]any, 0, len(prompts))
	for _, p := range prompts {
		out = append(out, map[string]any{
			"name": p.name, "description": p.description, "arguments": p.arguments,
		})
	}
	return out
}

func mcpPromptsGetFrom(params json.RawMessage, prompts []mcpPrompt) (map[string]any, *rpcError) {
	var p struct {
		Name      string            `json:"name"`
		Arguments map[string]string `json:"arguments"`
	}
	if err := json.Unmarshal(params, &p); err != nil {
		return nil, &rpcError{Code: -32602, Message: "invalid params"}
	}
	for _, pr := range prompts {
		if pr.name == p.Name {
			return map[string]any{
				"description": pr.description,
				"messages": []map[string]any{
					{"role": "user", "content": map[string]any{"type": "text", "text": pr.render(p.Arguments)}},
				},
			}, nil
		}
	}
	return nil, &rpcError{Code: -32602, Message: "unknown prompt: " + p.Name}
}
