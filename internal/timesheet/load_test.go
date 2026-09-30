package timesheet

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vinistoisr/timewarp/internal/db"
)

func TestParseDBTime(t *testing.T) {
	for _, s := range []string{
		"2026-09-30 20:36:25.2076655 +0000 UTC",
		"2026-04-20 19:00:00.0000000 +0000 UTC",
		"2026-05-02 00:02:41.207 +0000 UTC",
		"2026-09-30T20:36:25Z",
	} {
		if _, err := ParseDBTime(s); err != nil {
			t.Errorf("ParseDBTime(%q): %v", s, err)
		}
	}
	if _, err := ParseDBTime(time.Now()); err != nil {
		t.Errorf("time.Time value: %v", err)
	}
}

func TestLoad_IncludesEarlierEventsOfActiveSessions(t *testing.T) {
	dir := t.TempDir()
	tr, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	from := time.Date(2026, 9, 10, 7, 0, 0, 0, time.UTC)
	tr.InsertClaudeEvents([]db.ClaudeEvent{
		{TS: from.AddDate(0, 0, -3), Hostname: "H", Username: "u", SessionID: "long", Event: "prompt", RefKind: "project", RefNumber: "26-104"},
		{TS: from.Add(time.Hour), Hostname: "H", Username: "u", SessionID: "long", Event: "tool", RefKind: "none"},
		{TS: from.AddDate(0, 0, -3), Hostname: "H", Username: "u", SessionID: "old", Event: "prompt", RefKind: "project", RefNumber: "26-087"},
	})
	tr.Close()
	d, err := Load(dir, from, from.AddDate(0, 0, 1))
	if err != nil {
		t.Fatal(err)
	}
	var sessions []string
	for _, e := range d.Events {
		sessions = append(sessions, e.SessionID+" "+e.RefNumber)
	}
	if len(d.Events) != 2 || d.Events[0].RefNumber != "26-104" {
		t.Errorf("events = %v, want both rows of the active session and none of the old one", sessions)
	}
}

func TestLoad_DropsUnparseableTimes(t *testing.T) {
	dir := t.TempDir()
	tr, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tr.Close()
	matches, _ := filepath.Glob(filepath.Join(dir, "timewarp-*.db"))
	conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(matches[0]))
	if err != nil {
		t.Fatal(err)
	}
	conn.Exec(`INSERT INTO focus_events (hostname, username, process_name, window_title, started_at, ended_at, duration_seconds)
		VALUES ('H','u','EXCEL.EXE','26-104 x','2026-09-10 garbage','2026-09-10 12:00:00.0 +0000 UTC', 60)`)
	conn.Exec(`INSERT INTO inactivity_periods (hostname, username, started_at, ended_at, duration_seconds)
		VALUES ('H','u','2026-09-10 garbage','2026-09-10 12:00:00.0 +0000 UTC', 60)`)
	conn.Close()
	d, err := Load(dir, time.Date(2026, 9, 10, 0, 0, 0, 0, time.UTC), time.Date(2026, 9, 11, 0, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Focus) != 0 || len(d.Idle) != 0 {
		t.Errorf("rows with unparseable times must be dropped: focus=%d idle=%d", len(d.Focus), len(d.Idle))
	}
}

func TestLoad_SkipsBackups(t *testing.T) {
	dir := t.TempDir()
	tr, err := db.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)
	tr.InsertClaudeEvents([]db.ClaudeEvent{{TS: ts, Hostname: "H", Username: "u", SessionID: "s", Event: "prompt", RefKind: "none"}})
	tr.RecordFocus("H", "u", "EXCEL.EXE", "26-104 calc.xlsx", ts)
	tr.RecordFocus("H", "u", "EXCEL.EXE", "26-104 calc.xlsx", ts.Add(20*time.Second))
	tr.RecordFocus("H", "u", "chrome.exe", "x", ts.Add(40*time.Second))
	tr.Close()
	matches, _ := filepath.Glob(filepath.Join(dir, "timewarp-*.db"))
	data, _ := os.ReadFile(matches[0])
	os.WriteFile(filepath.Join(dir, "timewarp-H.bak-20260420.db"), data, 0o600)

	d, err := Load(dir, ts.Add(-time.Hour), ts.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Events) != 1 || len(d.Focus) != 1 {
		t.Fatalf("events=%d focus=%d, want 1 and 1 (backup must be skipped)", len(d.Events), len(d.Focus))
	}
	if d.Focus[0].Project != "26-104" || d.Focus[0].Host != "H" {
		t.Errorf("focus = %+v", d.Focus[0])
	}
}
