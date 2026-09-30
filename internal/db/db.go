package db

import (
	"database/sql"
	"fmt"
	"log"
	"os"
	"regexp"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vinistoisr/timewarp/internal/claudewin"
)

const (
	bridgeThreshold = 30 * time.Second
	minimumDuration = 10 * time.Second
)

var suppressedProcesses = map[string]bool{
	"mstsc.exe":                true,
	"applicationframehost.exe": true,
	"shellexperiencehost.exe":  true,
	"lockapp.exe":              true,
	"logonui.exe":              true,
}

var projectNumberRe = regexp.MustCompile(`(?:^|\D)(\d{2}-\d{3})(?:\D|$)`)

// Tracker holds the database connection and in-memory pending session state.
type Tracker struct {
	db *sql.DB
	mu sync.Mutex

	pending *pendingSession

	// inactivity tracking
	inactiveStart *time.Time
}

type pendingSession struct {
	hostname    string
	username    string
	processName string
	windowTitle string
	startedAt   time.Time
	lastSeen    time.Time
}

func Open(dbpath string) (*Tracker, error) {
	hostname, err := os.Hostname()
	if err != nil {
		return nil, fmt.Errorf("db: hostname: %w", err)
	}

	dbFile := fmt.Sprintf("timewarp-%s.db", hostname)
	dsn := fmt.Sprintf("file:%s/%s?_pragma=busy_timeout(5000)", dbpath, dbFile)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("db: open: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := initSchema(db); err != nil {
		db.Close()
		return nil, err
	}

	return &Tracker{db: db}, nil
}

func initSchema(db *sql.DB) error {
	stmts := []string{
		`CREATE TABLE IF NOT EXISTS focus_events (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname        TEXT NOT NULL,
			username        TEXT NOT NULL,
			process_name    TEXT NOT NULL,
			window_title    TEXT NOT NULL,
			project_number  TEXT,
			started_at      DATETIME NOT NULL,
			ended_at        DATETIME NOT NULL,
			duration_seconds REAL NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS inactivity_periods (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname        TEXT NOT NULL,
			username        TEXT NOT NULL,
			started_at      DATETIME NOT NULL,
			ended_at        DATETIME NOT NULL,
			duration_seconds REAL NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS meeting_sessions (
			id              INTEGER PRIMARY KEY AUTOINCREMENT,
			hostname        TEXT NOT NULL,
			username        TEXT NOT NULL,
			process_name    TEXT NOT NULL,
			subject         TEXT NOT NULL,
			started_at      DATETIME NOT NULL,
			ended_at        DATETIME NOT NULL,
			duration_seconds REAL NOT NULL
		)`,
		claudeEventsTable,
		claudeEventsIndex,
	}
	for _, s := range stmts {
		if _, err := db.Exec(s); err != nil {
			return fmt.Errorf("db: schema: %w", err)
		}
	}
	return nil
}

// RecordFocus is called every tick with the current window info.
// It handles session stitching and writes completed sessions to the DB.
func (t *Tracker) RecordFocus(hostname, username, processName, windowTitle string, now time.Time) {
	procLower := strings.ToLower(processName)
	if suppressedProcesses[procLower] {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if t.pending == nil {
		t.pending = &pendingSession{
			hostname:    hostname,
			username:    username,
			processName: processName,
			windowTitle: windowTitle,
			startedAt:   now,
			lastSeen:    now,
		}
		return
	}

	sameWindow := strings.EqualFold(t.pending.processName, processName)
	if sameWindow && claudewin.IsClaudeProcess(processName) {
		// Separate Claude sessions share one terminal process; split on title,
		// ignoring the status glyph that changes every second.
		sameWindow = claudewin.NormalizeTitle(t.pending.windowTitle) == claudewin.NormalizeTitle(windowTitle)
	}
	gap := now.Sub(t.pending.lastSeen)

	if sameWindow && gap <= bridgeThreshold {
		// Bridge the gap — extend the session, update title to latest
		t.pending.lastSeen = now
		t.pending.windowTitle = windowTitle
		return
	}

	// Different process or gap too large — flush pending session
	t.flushPending(now)

	// Start new pending session
	t.pending = &pendingSession{
		hostname:    hostname,
		username:    username,
		processName: processName,
		windowTitle: windowTitle,
		startedAt:   now,
		lastSeen:    now,
	}
}

// RecordInactivityStart marks the beginning of an inactivity period.
func (t *Tracker) RecordInactivityStart(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactiveStart == nil {
		t.inactiveStart = &now
	}
}

// RecordInactivityEnd marks the end of an inactivity period and writes it to the DB.
func (t *Tracker) RecordInactivityEnd(hostname, username string, now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.inactiveStart == nil {
		return
	}
	start := *t.inactiveStart
	t.inactiveStart = nil
	dur := now.Sub(start).Seconds()
	if dur < 1 {
		return
	}
	if _, err := t.db.Exec(
		`INSERT INTO inactivity_periods (hostname, username, started_at, ended_at, duration_seconds) VALUES (?,?,?,?,?)`,
		hostname, username, start.UTC(), now.UTC(), dur,
	); err != nil {
		log.Printf("db: inactivity insert: %v", err)
	}
}

func (t *Tracker) flushPending(now time.Time) {
	if t.pending == nil {
		return
	}
	dur := t.pending.lastSeen.Sub(t.pending.startedAt)
	if dur < minimumDuration {
		t.pending = nil
		return
	}

	var projNum *string
	if matches := projectNumberRe.FindStringSubmatch(t.pending.windowTitle); len(matches) > 1 {
		projNum = &matches[1]
	}

	// Check if this is a meeting
	isMeeting := (strings.EqualFold(t.pending.processName, "ms-teams.exe") ||
		strings.EqualFold(t.pending.processName, "zoom.exe")) &&
		strings.Contains(t.pending.windowTitle, "Meeting")

	if isMeeting {
		subject := extractMeetingSubject(t.pending.windowTitle)
		if _, err := t.db.Exec(
			`INSERT INTO meeting_sessions (hostname, username, process_name, subject, started_at, ended_at, duration_seconds) VALUES (?,?,?,?,?,?,?)`,
			t.pending.hostname, t.pending.username, t.pending.processName, subject,
			t.pending.startedAt.UTC(), t.pending.lastSeen.UTC(), dur.Seconds(),
		); err != nil {
			log.Printf("db: meeting insert: %v", err)
		}
	}

	if _, err := t.db.Exec(
		`INSERT INTO focus_events (hostname, username, process_name, window_title, project_number, started_at, ended_at, duration_seconds) VALUES (?,?,?,?,?,?,?,?)`,
		t.pending.hostname, t.pending.username, t.pending.processName,
		t.pending.windowTitle, projNum,
		t.pending.startedAt.UTC(), t.pending.lastSeen.UTC(), dur.Seconds(),
	); err != nil {
		log.Printf("db: focus_events insert: %v", err)
	}

	t.pending = nil
}

func extractMeetingSubject(title string) string {
	re := regexp.MustCompile(`Meeting\s*(.*)`)
	matches := re.FindStringSubmatch(title)
	if len(matches) > 1 {
		return strings.TrimSpace(matches[1])
	}
	return title
}

// Close flushes any pending session and closes the database.
func (t *Tracker) Close() error {
	t.mu.Lock()
	t.flushPending(time.Now())
	t.mu.Unlock()
	return t.db.Close()
}

// DB returns the underlying sql.DB for use by queries.
func (t *Tracker) DB() *sql.DB {
	return t.db
}
