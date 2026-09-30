package db

import (
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func tempTracker(t *testing.T) (*Tracker, string) {
	t.Helper()
	dir := t.TempDir()

	dsn := "file:" + filepath.Join(dir, "test.db") + "?_journal_mode=WAL"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	d.SetMaxOpenConns(1)
	if err := initSchema(d); err != nil {
		t.Fatal(err)
	}
	return &Tracker{db: d}, dir
}

func countRows(t *testing.T, d *sql.DB, table string) int {
	t.Helper()
	var n int
	if err := d.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

func TestClaudeWindowTitleChangeSplitsSession(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()
	base := time.Now().Truncate(time.Second)
	for i := 0; i < 15; i++ {
		tr.RecordFocus("HOST", "user", "WindowsTerminal.exe", "✳ Session A", base.Add(time.Duration(i)*time.Second))
	}
	for i := 15; i < 30; i++ {
		tr.RecordFocus("HOST", "user", "WindowsTerminal.exe", "⠏ Session B", base.Add(time.Duration(i)*time.Second))
	}
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(31*time.Second))
	if n := countRows(t, tr.db, "focus_events"); n != 2 {
		t.Errorf("expected 2 sessions for 2 Claude titles, got %d", n)
	}
}

func TestClaudeSpinnerChangeDoesNotSplit(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()
	base := time.Now().Truncate(time.Second)
	glyphs := []string{"✳", "⠏", "◐", "◑"}
	for i := 0; i < 20; i++ {
		tr.RecordFocus("HOST", "user", "WindowsTerminal.exe", glyphs[i%4]+" Session A", base.Add(time.Duration(i)*time.Second))
	}
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(21*time.Second))
	if n := countRows(t, tr.db, "focus_events"); n != 1 {
		t.Errorf("spinner changes must not split, got %d rows", n)
	}
}

func TestSessionStitching_BridgesGap(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	// Same process, ticks every second for 15 seconds
	for i := 0; i < 15; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "25-125_drawing.dwg", base.Add(time.Duration(i)*time.Second))
	}

	// Gap of 20 seconds (under 30s threshold), then same process again
	for i := 0; i < 15; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "25-125_drawing.dwg", base.Add(35*time.Second+time.Duration(i)*time.Second))
	}

	// Switch to different process to flush
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(60*time.Second))

	// Should be ONE stitched session (gap was bridged)
	n := countRows(t, tr.db, "focus_events")
	if n != 1 {
		t.Errorf("expected 1 stitched session, got %d", n)
	}
}

func TestSessionStitching_FlushesOnProcessChange(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	// 12 seconds of acad
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "drawing.dwg", base.Add(time.Duration(i)*time.Second))
	}
	// Switch to chrome for 12 seconds
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(12*time.Second+time.Duration(i)*time.Second))
	}
	// Switch again to flush chrome
	tr.RecordFocus("HOST", "user", "notepad.exe", "Untitled", base.Add(30*time.Second))

	n := countRows(t, tr.db, "focus_events")
	if n != 2 {
		t.Errorf("expected 2 sessions, got %d", n)
	}
}

func TestSessionStitching_DiscardsShortSessions(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	// 5 seconds of acad (under 10s minimum)
	for i := 0; i < 5; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "drawing.dwg", base.Add(time.Duration(i)*time.Second))
	}
	// Switch to chrome for 12 seconds
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(5*time.Second+time.Duration(i)*time.Second))
	}
	// Flush
	tr.RecordFocus("HOST", "user", "notepad.exe", "Untitled", base.Add(20*time.Second))

	n := countRows(t, tr.db, "focus_events")
	if n != 1 {
		t.Errorf("expected 1 session (short one discarded), got %d", n)
	}
}

func TestSessionStitching_LargeGapFlushes(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	// 12 seconds of acad
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "drawing.dwg", base.Add(time.Duration(i)*time.Second))
	}
	// Gap of 35 seconds (over 30s threshold), same process
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "drawing.dwg", base.Add(47*time.Second+time.Duration(i)*time.Second))
	}
	// Flush
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(65*time.Second))

	// Should be TWO sessions (gap was too large to bridge)
	n := countRows(t, tr.db, "focus_events")
	if n != 2 {
		t.Errorf("expected 2 sessions (gap too large), got %d", n)
	}
}

func TestSuppressedProcesses(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	// 15 seconds of mstsc.exe (suppressed)
	for i := 0; i < 15; i++ {
		tr.RecordFocus("HOST", "user", "mstsc.exe", "Remote Desktop", base.Add(time.Duration(i)*time.Second))
	}
	// Flush with a different process
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(20*time.Second))

	n := countRows(t, tr.db, "focus_events")
	if n != 0 {
		t.Errorf("expected 0 sessions (suppressed), got %d", n)
	}
}

func TestSuppressedProcesses_CaseInsensitive(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	for i := 0; i < 15; i++ {
		tr.RecordFocus("HOST", "user", "MSTSC.EXE", "Remote Desktop", base.Add(time.Duration(i)*time.Second))
	}
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(20*time.Second))

	n := countRows(t, tr.db, "focus_events")
	if n != 0 {
		t.Errorf("expected 0 sessions (suppressed), got %d", n)
	}
}

func TestProjectNumberExtraction(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "25-125_SLD-E101.dwg - AutoCAD", base.Add(time.Duration(i)*time.Second))
	}
	// Flush
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(15*time.Second))

	var projNum sql.NullString
	tr.db.QueryRow("SELECT project_number FROM focus_events LIMIT 1").Scan(&projNum)
	if !projNum.Valid || projNum.String != "25-125" {
		t.Errorf("expected project_number '25-125', got %v", projNum)
	}
}

func TestProjectNumberExtraction_NoMatch(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "chrome.exe", "ESPN - NBA Scores", base.Add(time.Duration(i)*time.Second))
	}
	tr.RecordFocus("HOST", "user", "notepad.exe", "Untitled", base.Add(15*time.Second))

	var projNum sql.NullString
	tr.db.QueryRow("SELECT project_number FROM focus_events LIMIT 1").Scan(&projNum)
	if projNum.Valid {
		t.Errorf("expected NULL project_number, got %v", projNum.String)
	}
}

func TestMeetingDetection(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "ms-teams.exe", "Meeting 25-019 Design Review", base.Add(time.Duration(i)*time.Second))
	}
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(15*time.Second))

	n := countRows(t, tr.db, "meeting_sessions")
	if n != 1 {
		t.Errorf("expected 1 meeting session, got %d", n)
	}

	var subject string
	tr.db.QueryRow("SELECT subject FROM meeting_sessions LIMIT 1").Scan(&subject)
	if subject != "25-019 Design Review" {
		t.Errorf("expected subject '25-019 Design Review', got %q", subject)
	}
}

func TestInactivity(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	tr.RecordInactivityStart(base)
	tr.RecordInactivityEnd("HOST", "user", base.Add(5*time.Minute))

	n := countRows(t, tr.db, "inactivity_periods")
	if n != 1 {
		t.Errorf("expected 1 inactivity period, got %d", n)
	}

	var dur float64
	tr.db.QueryRow("SELECT duration_seconds FROM inactivity_periods LIMIT 1").Scan(&dur)
	if dur != 300 {
		t.Errorf("expected 300s duration, got %f", dur)
	}
}

func TestInactivity_NoDoubleStart(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	tr.RecordInactivityStart(base)
	tr.RecordInactivityStart(base.Add(30 * time.Second)) // should be ignored
	tr.RecordInactivityEnd("HOST", "user", base.Add(5*time.Minute))

	var dur float64
	tr.db.QueryRow("SELECT duration_seconds FROM inactivity_periods LIMIT 1").Scan(&dur)
	if dur != 300 {
		t.Errorf("expected 300s (from first start), got %f", dur)
	}
}

func TestInactivity_EndWithoutStart(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	tr.RecordInactivityEnd("HOST", "user", time.Now())

	n := countRows(t, tr.db, "inactivity_periods")
	if n != 0 {
		t.Errorf("expected 0 inactivity periods, got %d", n)
	}
}

func TestCloseFlushes(t *testing.T) {
	tr, dir := tempTracker(t)

	base := time.Now().Truncate(time.Second)
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "drawing.dwg", base.Add(time.Duration(i)*time.Second))
	}

	// Verify nothing written yet (still pending)
	n := countRows(t, tr.db, "focus_events")
	if n != 0 {
		t.Errorf("expected 0 rows before close, got %d", n)
	}

	tr.Close()

	// Reopen the same db file to verify the flush happened
	dsn := "file:" + filepath.Join(dir, "test.db") + "?_journal_mode=WAL&mode=ro"
	d, err := sql.Open("sqlite", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()

	n = countRows(t, d, "focus_events")
	if n != 1 {
		t.Errorf("expected 1 row after close flush, got %d", n)
	}
}

func TestBridgeThreshold_ExactlyAtBoundary(t *testing.T) {
	tr, _ := tempTracker(t)
	defer tr.Close()

	base := time.Now().Truncate(time.Second)

	// 12 seconds of acad: ticks at 0..11, lastSeen = base+11s
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "drawing.dwg", base.Add(time.Duration(i)*time.Second))
	}
	// Resume at base+41s: gap = 41-11 = exactly 30 seconds (should bridge with <=)
	for i := 0; i < 12; i++ {
		tr.RecordFocus("HOST", "user", "acad.exe", "drawing.dwg", base.Add(41*time.Second+time.Duration(i)*time.Second))
	}
	// Flush
	tr.RecordFocus("HOST", "user", "chrome.exe", "Google", base.Add(60*time.Second))

	n := countRows(t, tr.db, "focus_events")
	if n != 1 {
		t.Errorf("expected 1 session (30s gap should bridge), got %d", n)
	}
}
