package mcp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"time"

	"github.com/vinistoisr/timewarp/internal/db"
	"github.com/vinistoisr/timewarp/internal/timesheet"
)

type jsonRPCRequest struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type toolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
}

var tools = []toolDef{
	{
		Name:        "get_weekly_summary",
		Description: "Get a weekly focus activity summary for timecard generation. Returns attributed project time, unattributed app time, meetings, and inactivity.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"week_start": {
					"type": "string",
					"description": "ISO date of the Monday starting the week (e.g. 2026-03-02). Defaults to current week."
				}
			}
		}`),
	},
	{
		Name:        "get_focus_time",
		Description: "Get total focused minutes for a specific process across a date range.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"process_name": {"type": "string", "description": "Process name (e.g. acad.exe)"},
				"date_from": {"type": "string", "description": "Start date (ISO, e.g. 2026-03-02)"},
				"date_to": {"type": "string", "description": "End date (ISO, e.g. 2026-03-08)"}
			},
			"required": ["process_name", "date_from", "date_to"]
		}`),
	},
	{
		Name:        "list_top_apps",
		Description: "List top 10 processes by total focused minutes for a given week.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"week_start": {
					"type": "string",
					"description": "ISO date of the Monday starting the week. Defaults to current week."
				}
			}
		}`),
	},
	{
		Name:        "get_daily_breakdown",
		Description: "Get focus time grouped by date, with project attribution, sample titles, and meetings per day. Ideal for generating QuickBooks Time entries or daily timecards.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"date_from": {"type": "string", "description": "Start date (ISO, e.g. 2026-03-02). Defaults to current week Monday."},
				"date_to": {"type": "string", "description": "End date (ISO, e.g. 2026-03-08). Defaults to Sunday after date_from."}
			}
		}`),
	},
	{
		Name:        "get_timesheet",
		Description: "Hours per billing code per day for QuickBooks Time. Claude sessions are attributed to the project they worked on; parallel work on different projects counts for each; proposals are billed to Admin. Includes unassigned time for manual review.",
		InputSchema: json.RawMessage(`{
			"type": "object",
			"properties": {
				"date_from": {"type": "string", "description": "Start date (ISO, e.g. 2026-09-01). Defaults to current week Monday."},
				"date_to": {"type": "string", "description": "End date, inclusive (ISO). Defaults to today."}
			}
		}`),
	},
}

// Run starts the MCP stdio server. It reads JSON-RPC 2.0 requests from stdin
// and writes responses to stdout. All logging goes to stderr.
func Run(dbpath string) error {
	log.SetOutput(os.Stderr)
	log.Println("MCP server starting, dbpath:", dbpath)

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

	for scanner.Scan() {
		line := scanner.Bytes()
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			writeError(nil, -32700, "Parse error")
			continue
		}

		handleRequest(dbpath, &req)
	}

	if err := scanner.Err(); err != nil && err != io.EOF {
		return fmt.Errorf("stdin read error: %w", err)
	}
	return nil
}

func handleRequest(dbpath string, req *jsonRPCRequest) {
	switch req.Method {
	case "initialize":
		result := map[string]interface{}{
			"protocolVersion": "2024-11-05",
			"capabilities": map[string]interface{}{
				"tools": map[string]interface{}{},
			},
			"serverInfo": map[string]interface{}{
				"name":    "timewarp",
				"version": "1.0.0",
			},
		}
		writeResult(req.ID, result)

	case "notifications/initialized":
		// No response needed for notifications

	case "ping":
		writeResult(req.ID, map[string]interface{}{})

	case "tools/list":
		result := map[string]interface{}{
			"tools": tools,
		}
		writeResult(req.ID, result)

	case "tools/call":
		handleToolCall(dbpath, req)

	default:
		if len(req.ID) > 0 {
			writeError(req.ID, -32601, fmt.Sprintf("Method not found: %s", req.Method))
		}
	}
}

func handleToolCall(dbpath string, req *jsonRPCRequest) {
	if len(req.Params) == 0 {
		writeError(req.ID, -32602, "params required")
		return
	}

	var params struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &params); err != nil {
		writeError(req.ID, -32602, "Invalid params")
		return
	}

	var result json.RawMessage
	var err error

	switch params.Name {
	case "get_weekly_summary":
		result, err = callGetWeeklySummary(dbpath, params.Arguments)
	case "get_focus_time":
		result, err = callGetFocusTime(dbpath, params.Arguments)
	case "list_top_apps":
		result, err = callListTopApps(dbpath, params.Arguments)
	case "get_daily_breakdown":
		result, err = callGetDailyBreakdown(dbpath, params.Arguments)
	case "get_timesheet":
		result, err = callGetTimesheet(dbpath, params.Arguments)
	default:
		toolResult := map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("Unknown tool: %s", params.Name)},
			},
			"isError": true,
		}
		writeResult(req.ID, toolResult)
		return
	}

	if err != nil {
		toolResult := map[string]interface{}{
			"content": []map[string]string{
				{"type": "text", "text": fmt.Sprintf("Error: %v", err)},
			},
			"isError": true,
		}
		writeResult(req.ID, toolResult)
		return
	}

	toolResult := map[string]interface{}{
		"content": []map[string]string{
			{"type": "text", "text": string(result)},
		},
	}
	writeResult(req.ID, toolResult)
}

func callGetWeeklySummary(dbpath string, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		WeekStart string `json:"week_start"`
	}
	if len(args) > 0 {
		json.Unmarshal(args, &a)
	}

	var weekStart time.Time
	if a.WeekStart == "" {
		weekStart = db.CurrentWeekMonday()
	} else {
		var err error
		weekStart, err = db.ParseWeekStart(a.WeekStart)
		if err != nil {
			return nil, err
		}
	}

	return db.GetWeeklySummary(dbpath, weekStart)
}

func callGetFocusTime(dbpath string, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		ProcessName string `json:"process_name"`
		DateFrom    string `json:"date_from"`
		DateTo      string `json:"date_to"`
	}
	if err := json.Unmarshal(args, &a); err != nil {
		return nil, fmt.Errorf("invalid arguments: %w", err)
	}
	if a.ProcessName == "" || a.DateFrom == "" || a.DateTo == "" {
		return nil, fmt.Errorf("process_name, date_from, and date_to are required")
	}

	dateFrom, err := time.Parse("2006-01-02", a.DateFrom)
	if err != nil {
		return nil, fmt.Errorf("invalid date_from: %w", err)
	}
	dateTo, err := time.Parse("2006-01-02", a.DateTo)
	if err != nil {
		return nil, fmt.Errorf("invalid date_to: %w", err)
	}

	return db.GetFocusTime(dbpath, a.ProcessName, dateFrom, dateTo)
}

func callListTopApps(dbpath string, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		WeekStart string `json:"week_start"`
	}
	if len(args) > 0 {
		json.Unmarshal(args, &a)
	}

	var weekStart time.Time
	if a.WeekStart == "" {
		weekStart = db.CurrentWeekMonday()
	} else {
		var err error
		weekStart, err = db.ParseWeekStart(a.WeekStart)
		if err != nil {
			return nil, err
		}
	}

	return db.ListTopApps(dbpath, weekStart)
}

func callGetDailyBreakdown(dbpath string, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	if len(args) > 0 {
		json.Unmarshal(args, &a)
	}

	var dateFrom time.Time
	if a.DateFrom == "" {
		dateFrom = db.CurrentWeekMonday()
	} else {
		var err error
		dateFrom, err = time.Parse("2006-01-02", a.DateFrom)
		if err != nil {
			return nil, fmt.Errorf("invalid date_from: %w", err)
		}
	}

	var dateTo time.Time
	if a.DateTo == "" {
		dateTo = dateFrom.AddDate(0, 0, 7)
	} else {
		var err error
		dateTo, err = time.Parse("2006-01-02", a.DateTo)
		if err != nil {
			return nil, fmt.Errorf("invalid date_to: %w", err)
		}
	}

	// When date_from == date_to the exclusive upper bound produces an empty range.
	// Auto-increment date_to by one day so a single-day query returns data.
	if dateTo.Equal(dateFrom) {
		dateTo = dateFrom.AddDate(0, 0, 1)
	}

	return db.GetDailyBreakdown(dbpath, dateFrom, dateTo)
}

func callGetTimesheet(dbpath string, args json.RawMessage) (json.RawMessage, error) {
	var a struct {
		DateFrom string `json:"date_from"`
		DateTo   string `json:"date_to"`
	}
	if len(args) > 0 {
		json.Unmarshal(args, &a)
	}
	from := db.CurrentWeekMonday()
	now := time.Now()
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	var err error
	if a.DateFrom != "" {
		if from, err = time.ParseInLocation("2006-01-02", a.DateFrom, time.Local); err != nil {
			return nil, fmt.Errorf("invalid date_from: %w", err)
		}
	}
	if a.DateTo != "" {
		if to, err = time.ParseInLocation("2006-01-02", a.DateTo, time.Local); err != nil {
			return nil, fmt.Errorf("invalid date_to: %w", err)
		}
	}
	r, err := timesheet.Generate(dbpath, from, to.AddDate(0, 0, 1), timesheet.DefaultGenerateOptions())
	if err != nil {
		return nil, err
	}
	return json.Marshal(r)
}

func writeResult(id json.RawMessage, result interface{}) {
	data, _ := json.Marshal(result)
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result:  data,
	}
	out, _ := json.Marshal(resp)
	fmt.Fprintf(os.Stdout, "%s\n", out)
}

func writeError(id json.RawMessage, code int, message string) {
	resp := jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &jsonRPCError{Code: code, Message: message},
	}
	out, _ := json.Marshal(resp)
	fmt.Fprintf(os.Stdout, "%s\n", out)
}
