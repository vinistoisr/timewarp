package timesheet

import (
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/vinistoisr/timewarp/internal/db"
)

// Focus is one focused-window span from focus_events.
type Focus struct {
	Host, Process, Title, Project string
	Interval
}

// HostSpan is an interval on one PC (inactivity periods).
type HostSpan struct {
	Host string
	Interval
}

// Data is everything the attribution needs for a date range, from every PC.
type Data struct {
	Focus  []Focus
	Idle   []HostSpan
	Events []db.ClaudeEvent
}

// ParseDBTime accepts a time.Time or the text forms stored by the Go driver.
func ParseDBTime(v any) (time.Time, error) {
	switch t := v.(type) {
	case time.Time:
		return t, nil
	case []byte:
		v = string(t)
	}
	s, ok := v.(string)
	if !ok {
		return time.Time{}, fmt.Errorf("unsupported time value %T", v)
	}
	for _, layout := range []string{"2006-01-02 15:04:05.999999999 -0700 MST", time.RFC3339Nano, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, nil
		}
	}
	return time.Time{}, fmt.Errorf("unparseable time %q", s)
}

// Load reads focus events, inactivity periods and Claude events overlapping
// [from, to) from every timewarp-*.db in dbpath, skipping *.bak* files.
func Load(dbpath string, from, to time.Time) (Data, error) {
	matches, err := filepath.Glob(filepath.Join(dbpath, "timewarp-*.db"))
	if err != nil {
		return Data{}, err
	}
	var d Data
	// Stored text sorts chronologically in UTC; pad by a day and filter exactly below.
	lo := from.UTC().AddDate(0, 0, -1).Format("2006-01-02")
	hi := to.UTC().AddDate(0, 0, 1).Format("2006-01-02")
	for _, m := range matches {
		if strings.Contains(strings.ToLower(filepath.Base(m)), ".bak") {
			continue
		}
		conn, err := sql.Open("sqlite", "file:"+filepath.ToSlash(m)+"?mode=ro&_pragma=busy_timeout(5000)")
		if err != nil {
			return Data{}, fmt.Errorf("open %s: %w", m, err)
		}
		err = loadOne(conn, lo, hi, from, to, &d)
		conn.Close()
		if err != nil {
			return Data{}, fmt.Errorf("read %s: %w", m, err)
		}
	}
	return d, nil
}

func overlaps(i Interval, from, to time.Time) bool {
	return i.End.After(from) && i.Start.Before(to)
}

func loadOne(conn *sql.DB, lo, hi string, from, to time.Time, d *Data) error {
	rows, err := conn.Query(`SELECT hostname, process_name, window_title, COALESCE(project_number,''), started_at, ended_at
		FROM focus_events WHERE started_at >= ? AND started_at < ?`, lo, hi)
	if err != nil {
		return err
	}
	for rows.Next() {
		var f Focus
		var s, e any
		if err := rows.Scan(&f.Host, &f.Process, &f.Title, &f.Project, &s, &e); err != nil {
			rows.Close()
			return err
		}
		f.Start, _ = ParseDBTime(s)
		f.End, _ = ParseDBTime(e)
		if overlaps(f.Interval, from, to) {
			d.Focus = append(d.Focus, f)
		}
	}
	rows.Close()

	rows, err = conn.Query(`SELECT hostname, started_at, ended_at FROM inactivity_periods
		WHERE started_at >= ? AND started_at < ?`, lo, hi)
	if err != nil {
		return err
	}
	for rows.Next() {
		var h HostSpan
		var s, e any
		if err := rows.Scan(&h.Host, &s, &e); err != nil {
			rows.Close()
			return err
		}
		h.Start, _ = ParseDBTime(s)
		h.End, _ = ParseDBTime(e)
		if overlaps(h.Interval, from, to) {
			d.Idle = append(d.Idle, h)
		}
	}
	rows.Close()

	var hasTable int
	conn.QueryRow(`SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name='claude_events'`).Scan(&hasTable)
	if hasTable == 0 {
		return nil
	}
	rows, err = conn.Query(`SELECT ts, hostname, username, session_id, session_title, event, ref_kind, ref_number, ref_source
		FROM claude_events WHERE ts >= ? AND ts < ? ORDER BY ts`, lo, hi)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var ev db.ClaudeEvent
		var ts any
		if err := rows.Scan(&ts, &ev.Hostname, &ev.Username, &ev.SessionID, &ev.SessionTitle,
			&ev.Event, &ev.RefKind, &ev.RefNumber, &ev.RefSource); err != nil {
			return err
		}
		ev.TS, _ = ParseDBTime(ts)
		d.Events = append(d.Events, ev)
	}
	return rows.Err()
}
