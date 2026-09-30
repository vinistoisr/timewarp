package mcp

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"testing"
)

// captureStdout runs fn with stdout redirected and returns the output.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()

	old := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = w

	fn()

	w.Close()
	os.Stdout = old

	var buf bytes.Buffer
	io.Copy(&buf, r)
	r.Close()
	return buf.String()
}

func TestInitialize(t *testing.T) {
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`1`),
		Method:  "initialize",
	}

	output := captureStdout(t, func() {
		handleRequest(".", req)
	})

	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(output), &resp); err != nil {
		t.Fatalf("failed to parse response: %v\nraw: %s", err, output)
	}

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result map[string]interface{}
	json.Unmarshal(resp.Result, &result)

	if result["protocolVersion"] != "2024-11-05" {
		t.Errorf("unexpected protocol version: %v", result["protocolVersion"])
	}

	serverInfo := result["serverInfo"].(map[string]interface{})
	if serverInfo["name"] != "timewarp" {
		t.Errorf("unexpected server name: %v", serverInfo["name"])
	}
}

func TestPing(t *testing.T) {
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`2`),
		Method:  "ping",
	}

	output := captureStdout(t, func() {
		handleRequest(".", req)
	})

	var resp jsonRPCResponse
	json.Unmarshal([]byte(output), &resp)

	if resp.Error != nil {
		t.Fatalf("ping should not error: %v", resp.Error)
	}
}

func TestToolsList(t *testing.T) {
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`3`),
		Method:  "tools/list",
	}

	output := captureStdout(t, func() {
		handleRequest(".", req)
	})

	var resp jsonRPCResponse
	json.Unmarshal([]byte(output), &resp)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %v", resp.Error)
	}

	var result struct {
		Tools []struct {
			Name string `json:"name"`
		} `json:"tools"`
	}
	json.Unmarshal(resp.Result, &result)

	if len(result.Tools) != 5 {
		t.Fatalf("expected 5 tools, got %d", len(result.Tools))
	}

	names := map[string]bool{}
	for _, tool := range result.Tools {
		names[tool.Name] = true
	}
	for _, expected := range []string{"get_weekly_summary", "get_focus_time", "list_top_apps", "get_daily_breakdown", "get_timesheet"} {
		if !names[expected] {
			t.Errorf("missing tool: %s", expected)
		}
	}
}

func TestUnknownTool(t *testing.T) {
	params, _ := json.Marshal(map[string]interface{}{
		"name":      "nonexistent_tool",
		"arguments": map[string]interface{}{},
	})
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`4`),
		Method:  "tools/call",
		Params:  params,
	}

	output := captureStdout(t, func() {
		handleRequest(".", req)
	})

	var resp jsonRPCResponse
	json.Unmarshal([]byte(output), &resp)

	// Should be a successful response with isError in the tool result, not a JSON-RPC error
	if resp.Error != nil {
		t.Fatalf("expected tool-level error, got JSON-RPC error: %v", resp.Error)
	}

	var result map[string]interface{}
	json.Unmarshal(resp.Result, &result)

	if result["isError"] != true {
		t.Error("expected isError: true for unknown tool")
	}
}

func TestUnknownMethod(t *testing.T) {
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`5`),
		Method:  "nonexistent/method",
	}

	output := captureStdout(t, func() {
		handleRequest(".", req)
	})

	var resp jsonRPCResponse
	json.Unmarshal([]byte(output), &resp)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected -32601, got %d", resp.Error.Code)
	}
}

func TestNotification_NoResponse(t *testing.T) {
	// Notification has no ID
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  "notifications/initialized",
	}

	output := captureStdout(t, func() {
		handleRequest(".", req)
	})

	if output != "" {
		t.Errorf("notifications should produce no output, got: %s", output)
	}
}

func TestToolsCall_NilParams(t *testing.T) {
	req := &jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      json.RawMessage(`6`),
		Method:  "tools/call",
		Params:  nil,
	}

	output := captureStdout(t, func() {
		handleRequest(".", req)
	})

	var resp jsonRPCResponse
	json.Unmarshal([]byte(output), &resp)

	if resp.Error == nil {
		t.Fatal("expected error for nil params")
	}
	if resp.Error.Code != -32602 {
		t.Errorf("expected -32602, got %d", resp.Error.Code)
	}
}

func TestToolsIncludeGetTimesheet(t *testing.T) {
	for _, tl := range tools {
		if tl.Name == "get_timesheet" {
			return
		}
	}
	t.Error("get_timesheet tool not registered")
}
