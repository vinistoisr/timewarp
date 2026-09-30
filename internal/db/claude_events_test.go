package db

import (
	"testing"
	"time"
)

func sampleEvent() ClaudeEvent {
	return ClaudeEvent{
		TS: time.Date(2026, 9, 30, 20, 0, 0, 123, time.UTC), Hostname: "H", Username: "u",
		SessionID: "s1", SessionTitle: "Title A", Event: "prompt",
		RefKind: "project", RefNumber: "26-104", RefSource: "path",
	}
}

func TestInsertClaudeEvents_Dedupes(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()
	ev := sampleEvent()
	n, err := tr.InsertClaudeEvents([]ClaudeEvent{ev, ev})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("first insert: %d rows, want 1", n)
	}
	if n, _ = tr.InsertClaudeEvents([]ClaudeEvent{ev}); n != 0 {
		t.Fatalf("re-insert: %d rows, want 0", n)
	}
	none := ev
	none.RefKind, none.RefNumber, none.RefSource = "none", "", ""
	if n, _ = tr.InsertClaudeEvents([]ClaudeEvent{none, none}); n != 1 {
		t.Fatalf("none rows: %d, want 1", n)
	}
}

func TestLastSessionTitle(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()
	a := sampleEvent()
	b := sampleEvent()
	b.TS = b.TS.Add(time.Minute)
	b.SessionTitle = "Title B"
	c := sampleEvent()
	c.TS = c.TS.Add(2 * time.Minute)
	c.SessionTitle = ""
	if _, err := tr.InsertClaudeEvents([]ClaudeEvent{a, b, c}); err != nil {
		t.Fatal(err)
	}
	if got := tr.LastSessionTitle("s1"); got != "Title B" {
		t.Errorf("LastSessionTitle = %q, want Title B", got)
	}
	if got := tr.LastSessionTitle("missing"); got != "" {
		t.Errorf("missing session = %q, want empty", got)
	}
}

func TestOpen_SetsBusyTimeout(t *testing.T) {
	tr, err := Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer tr.Close()
	var v int
	if err := tr.db.QueryRow("PRAGMA busy_timeout").Scan(&v); err != nil {
		t.Fatal(err)
	}
	if v != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", v)
	}
}
