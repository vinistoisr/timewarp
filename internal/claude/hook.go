package claude

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/vinistoisr/timewarp/internal/db"
)

// HookInput is the JSON Claude Code passes to a command hook on stdin.
type HookInput struct {
	SessionID      string          `json:"session_id"`
	TranscriptPath string          `json:"transcript_path"`
	Cwd            string          `json:"cwd"`
	HookEventName  string          `json:"hook_event_name"`
	Prompt         string          `json:"prompt"`
	ToolName       string          `json:"tool_name"`
	ToolInput      json.RawMessage `json:"tool_input"`
}

func eventKind(hookEvent string) string {
	switch hookEvent {
	case "UserPromptSubmit":
		return "prompt"
	case "PostToolUse":
		return "tool"
	case "Stop":
		return "stop"
	case "SessionEnd":
		return "end"
	}
	return ""
}

// BuildEvents turns one hook invocation into claude_events rows: one per
// number found, or a single ref_kind=none row.
func BuildEvents(in HookInput, ts time.Time, host, user, title string) []db.ClaudeEvent {
	kind := eventKind(in.HookEventName)
	if kind == "" {
		return nil
	}
	var prompt string
	var tool json.RawMessage
	switch kind {
	case "prompt":
		prompt = in.Prompt
	case "tool":
		tool = in.ToolInput
	}
	return eventsFor(ExtractRefs(prompt, tool, in.Cwd), db.ClaudeEvent{
		TS: ts.UTC(), Hostname: host, Username: user,
		SessionID: in.SessionID, SessionTitle: title, Event: kind,
	})
}

func eventsFor(refs []Ref, base db.ClaudeEvent) []db.ClaudeEvent {
	if len(refs) == 0 {
		e := base
		e.RefKind = "none"
		return []db.ClaudeEvent{e}
	}
	out := make([]db.ClaudeEvent, 0, len(refs))
	for _, r := range refs {
		e := base
		e.RefKind, e.RefNumber, e.RefSource = r.Kind, r.Number, r.Source
		out = append(out, e)
	}
	return out
}

// RunHook handles one hook invocation. Errors are returned for the caller to
// log; the caller always exits 0 so Claude is never blocked.
func RunHook(r io.Reader, dbpath string) error {
	var in HookInput
	br := bufio.NewReader(r)
	if b, err := br.Peek(3); err == nil && b[0] == 0xEF && b[1] == 0xBB && b[2] == 0xBF {
		br.Discard(3) // UTF-8 byte order mark, added by Windows PowerShell pipes
	}
	if err := json.NewDecoder(br).Decode(&in); err != nil {
		return fmt.Errorf("hook: decode stdin: %w", err)
	}
	if in.SessionID == "" {
		return nil
	}
	t, err := db.Open(dbpath)
	if err != nil {
		return err
	}
	defer t.Close()
	title := LastTitle(in.TranscriptPath)
	if title == "" {
		title = t.LastSessionTitle(in.SessionID)
	}
	host, _ := os.Hostname()
	_, err = t.InsertClaudeEvents(BuildEvents(in, time.Now(), host, os.Getenv("USERNAME"), title))
	return err
}
