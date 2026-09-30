package db

import (
	"fmt"
	"time"
)

// ClaudeEvent is one Claude Code hook event, or one project reference within it.
type ClaudeEvent struct {
	TS           time.Time
	Hostname     string
	Username     string
	SessionID    string
	SessionTitle string
	Event        string // prompt, tool, stop, end
	RefKind      string // project, proposal, mention, none
	RefNumber    string // NN-NNN or ""
	RefSource    string // path, prompt, cwd or ""
}

const claudeEventsTable = `CREATE TABLE IF NOT EXISTS claude_events (
	id            INTEGER PRIMARY KEY AUTOINCREMENT,
	ts            DATETIME NOT NULL,
	hostname      TEXT NOT NULL,
	username      TEXT NOT NULL,
	session_id    TEXT NOT NULL,
	session_title TEXT NOT NULL,
	event         TEXT NOT NULL,
	ref_kind      TEXT NOT NULL,
	ref_number    TEXT NOT NULL,
	ref_source    TEXT NOT NULL
)`

const claudeEventsIndex = `CREATE UNIQUE INDEX IF NOT EXISTS claude_events_dedupe
	ON claude_events(session_id, ts, event, ref_number)`

// InsertClaudeEvents writes events, skipping exact duplicates. Returns rows inserted.
func (t *Tracker) InsertClaudeEvents(evs []ClaudeEvent) (int, error) {
	if len(evs) == 0 {
		return 0, nil
	}
	tx, err := t.db.Begin()
	if err != nil {
		return 0, fmt.Errorf("db: begin: %w", err)
	}
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO claude_events
		(ts, hostname, username, session_id, session_title, event, ref_kind, ref_number, ref_source)
		VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		tx.Rollback()
		return 0, fmt.Errorf("db: prepare: %w", err)
	}
	defer stmt.Close()
	n := 0
	for _, e := range evs {
		res, err := stmt.Exec(e.TS.UTC(), e.Hostname, e.Username, e.SessionID, e.SessionTitle,
			e.Event, e.RefKind, e.RefNumber, e.RefSource)
		if err != nil {
			tx.Rollback()
			return 0, fmt.Errorf("db: claude_events insert: %w", err)
		}
		if a, err := res.RowsAffected(); err == nil {
			n += int(a)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, fmt.Errorf("db: commit: %w", err)
	}
	return n, nil
}

// LastSessionTitle returns the most recent non-empty title recorded for a session.
func (t *Tracker) LastSessionTitle(sessionID string) string {
	var s string
	t.db.QueryRow(`SELECT session_title FROM claude_events
		WHERE session_id = ? AND session_title <> '' ORDER BY id DESC LIMIT 1`, sessionID).Scan(&s)
	return s
}
