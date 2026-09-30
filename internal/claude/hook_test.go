package claude

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLastTitle(t *testing.T) {
	p := filepath.Join(t.TempDir(), "s.jsonl")
	body := `{"type":"ai-title","aiTitle":"First","sessionId":"s"}` + "\n" +
		`{"type":"user","message":{"content":"hi"}}` + "\n" +
		`{"type":"ai-title","aiTitle":"Second","sessionId":"s"}` + "\n"
	os.WriteFile(p, []byte(body), 0o600)
	if got := LastTitle(p); got != "Second" {
		t.Errorf("LastTitle = %q, want Second", got)
	}
	if got := LastTitle(filepath.Join(t.TempDir(), "missing.jsonl")); got != "" {
		t.Errorf("missing file = %q, want empty", got)
	}
}

func TestBuildEvents(t *testing.T) {
	ts := time.Date(2026, 9, 30, 20, 0, 0, 0, time.UTC)
	in := HookInput{SessionID: "s1", HookEventName: "PostToolUse", Cwd: `C:\Users\vincent`,
		ToolInput: json.RawMessage(`{"file_path":"S:/PROJECT/2026/26-104 CBC/a.xlsx"}`)}
	evs := BuildEvents(in, ts, "DESK63", "vincent", "My title")
	if len(evs) != 1 || evs[0].Event != "tool" || evs[0].RefNumber != "26-104" || evs[0].RefKind != "project" || evs[0].SessionTitle != "My title" {
		t.Fatalf("unexpected events %+v", evs)
	}
	stop := BuildEvents(HookInput{SessionID: "s1", HookEventName: "Stop"}, ts, "H", "u", "")
	if len(stop) != 1 || stop[0].Event != "stop" || stop[0].RefKind != "none" {
		t.Fatalf("stop event %+v", stop)
	}
	if got := BuildEvents(HookInput{SessionID: "s1", HookEventName: "Notification"}, ts, "H", "u", ""); got != nil {
		t.Fatalf("unknown hook event must produce nothing, got %+v", got)
	}
}

func TestRunHook_WritesRow(t *testing.T) {
	dir := t.TempDir()
	in := `{"session_id":"s9","hook_event_name":"UserPromptSubmit","prompt":"project 26-104 please","cwd":"C:\\x"}`
	if err := RunHook(strings.NewReader(in), dir); err != nil {
		t.Fatal(err)
	}
	matches, _ := filepath.Glob(filepath.Join(dir, "timewarp-*.db"))
	if len(matches) != 1 {
		t.Fatalf("expected one DB file, got %v", matches)
	}
}

func TestRunHook_AcceptsByteOrderMark(t *testing.T) {
	dir := t.TempDir()
	in := string([]byte{0xEF, 0xBB, 0xBF}) + `{"session_id":"s9","hook_event_name":"Stop"}`
	if err := RunHook(strings.NewReader(in), dir); err != nil {
		t.Fatalf("input with a UTF-8 BOM (Windows PowerShell pipes add one) failed: %v", err)
	}
}

func TestRunHook_GarbageInput(t *testing.T) {
	dir := t.TempDir()
	if err := RunHook(strings.NewReader("not json"), dir); err == nil {
		t.Error("garbage input should return an error for logging")
	}
	if err := RunHook(strings.NewReader(`{"hook_event_name":"Stop"}`), dir); err != nil {
		t.Errorf("missing session id should be a silent no-op, got %v", err)
	}
	if err := RunHook(strings.NewReader(`{"session_id":"s","hook_event_name":"Stop"}`), filepath.Join(dir, "no", "such", "dir")); err == nil {
		t.Error("unwritable DB path should return an error for logging")
	}
}
