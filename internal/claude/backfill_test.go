package claude

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleTranscript = `{"type":"user","sessionId":"s1","timestamp":"2026-09-30T20:00:00.000Z","cwd":"C:\\Users\\vincent","message":{"role":"user","content":"look at project 26-104"}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-09-30T20:00:05.000Z","cwd":"C:\\Users\\vincent","message":{"content":[{"type":"text","text":"ok"},{"type":"tool_use","id":"t1","name":"Read","input":{"file_path":"S:/PROJECT/2026/26-087 GI/a.docx"}}]}}
{"type":"user","sessionId":"s1","timestamp":"2026-09-30T20:00:06.000Z","message":{"content":[{"type":"tool_result","tool_use_id":"t1","content":"..."}]}}
{"type":"ai-title","aiTitle":"GI review","sessionId":"s1"}
{"type":"assistant","sessionId":"s1","timestamp":"2026-09-30T20:03:00.000Z","message":{"content":[{"type":"text","text":"done"}]}}
{"type":"user","sessionId":"s1","timestamp":"2026-09-30T21:00:00.000Z","message":{"content":[{"type":"text","text":"next thing"}]}}
{"type":"assistant","sessionId":"s1","timestamp":"2026-09-30T21:01:00.000Z","message":{"content":[{"type":"text","text":"ok"}]}}
`

func TestEventsFromTranscript(t *testing.T) {
	evs, err := EventsFromTranscript(strings.NewReader(sampleTranscript), "DESK63", "vincent")
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	for _, e := range evs {
		got = append(got, e.TS.Format("15:04:05")+" "+e.Event+" "+e.RefKind+" "+e.RefNumber+" "+e.SessionTitle)
	}
	want := []string{
		"20:00:00 prompt project 26-104 GI review",
		"20:00:05 tool project 26-087 GI review",
		"20:03:00 stop none  GI review",
		"21:00:00 prompt none  GI review",
		"21:01:00 stop none  GI review",
	}
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("events:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
}

func TestEventsFromTranscript_SkipsSyntheticPrompts(t *testing.T) {
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-09-30T20:00:00Z","isCompactSummary":true,"message":{"content":"This session is being continued. Files: S:\\PROJECT\\2026\\26-087 GI\\a.docx"}}
{"type":"user","sessionId":"s1","timestamp":"2026-09-30T20:01:00Z","message":{"content":"<task-notification>\nsee S:\\PROJECT\\2026\\26-139 Pender\\x.pdf\n</task-notification>"}}
{"type":"user","sessionId":"s1","timestamp":"2026-09-30T20:02:00Z","message":{"content":"work on project 26-104"}}
`
	evs, err := EventsFromTranscript(strings.NewReader(body), "H", "u")
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range evs {
		if e.RefNumber == "26-087" || e.RefNumber == "26-139" {
			t.Errorf("synthetic prompt produced ref %s at %s", e.RefNumber, e.TS)
		}
	}
	found := false
	for _, e := range evs {
		if e.RefNumber == "26-104" && e.Event == "prompt" {
			found = true
		}
	}
	if !found {
		t.Error("real prompt ref missing")
	}
}

func TestEventsFromTranscript_LongLine(t *testing.T) {
	big := strings.Repeat("x", 2<<20)
	body := `{"type":"user","sessionId":"s1","timestamp":"2026-09-30T20:00:00Z","message":{"content":"` + big + `"}}` + "\n" +
		`{"type":"user","sessionId":"s1","timestamp":"2026-09-30T20:05:00Z","message":{"content":"project 26-104"}}` + "\n"
	evs, err := EventsFromTranscript(strings.NewReader(body), "H", "u")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, e := range evs {
		if e.RefNumber == "26-104" {
			found = true
		}
	}
	if !found {
		t.Error("line after a 2 MB line was not read")
	}
}

func TestBackfill_Idempotent(t *testing.T) {
	root := t.TempDir()
	dbdir := t.TempDir()
	os.MkdirAll(filepath.Join(root, "proj"), 0o755)
	os.WriteFile(filepath.Join(root, "proj", "s1.jsonl"), []byte(sampleTranscript), 0o600)
	files, n1, err := Backfill(root, dbdir)
	if err != nil || files != 1 || n1 != 5 {
		t.Fatalf("first backfill files=%d inserted=%d err=%v", files, n1, err)
	}
	_, n2, err := Backfill(root, dbdir)
	if err != nil || n2 != 0 {
		t.Fatalf("second backfill inserted=%d err=%v, want 0", n2, err)
	}
}
