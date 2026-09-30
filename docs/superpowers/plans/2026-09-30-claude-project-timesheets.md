# Claude Project Timesheets Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make timewarp start reliably without admin rights, capture which project each Claude Code session works on, and produce per-project per-day hours for QuickBooks Time.

**Architecture:** The tracker (existing) keeps logging focused windows. A new `-claudehook` command, registered as Claude Code hooks, writes `claude_events` rows to the same per-PC SQLite file. A new `timesheet` package reads every PC's database, builds time intervals per billing code (union within a code, independent across codes) and renders a table, CSV or MCP JSON. Install moves to a non-elevated scheduled task named per user, plus a settings.json hook merge.

**Tech Stack:** Go 1.24+, modernc.org/sqlite (pure Go), golang.org/x/sys/windows, getlantern/systray. Windows only.

**Spec:** `docs/superpowers/specs/2026-09-30-claude-project-timesheets-design.md`

## Global Constraints

- Module path `github.com/vinistoisr/timewarp`; `go 1.24.0` in go.mod stays.
- SQLite DSN parameters must use modernc syntax `_pragma=name(value)`. `_busy_timeout` and `_journal_mode` are ignored by this driver (verified: the live DBs are in `delete` journal mode). Keep the rollback journal (the DB folder is in OneDrive); add `_pragma=busy_timeout(5000)`.
- Stored DATETIME values are text in Go `time.Time.String()` form, e.g. `2026-09-30 20:36:25.2076655 +0000 UTC`. Always write UTC times.
- Number pattern `2\d-\d{3}` bounded by non-digits and not part of a longer `\d+-\d+` token.
- Idle threshold 10 minutes; turn gap 10 minutes; recent-session window 2 minutes.
- Proposal time reports under billing code `Admin`.
- Rounding: nearest 0.5 h; 10 min to under 0.5 h rounds to 0.5 h; under 10 min shows 0.
- Claude processes: `WindowsTerminal.exe`, `claude.exe`, `Code.exe` (case-insensitive).
- Task name `Timewarp-<username>`; never elevate.
- Hook command entries are identified by the argument `-claudehook`; other hooks in settings.json must survive untouched.
- Prose (README, messages): no em-dashes.
- Toolchain on DESK63: portable Go in the session scratchpad. Every shell running go needs `$env:PATH = "$scratch\go\bin;" + $env:PATH` where `$scratch = "C:\Users\vincent\AppData\Local\Temp\claude\C--Users-vincent\4d580c62-0dd3-4763-b2a0-c4e989e1ddbd\scratchpad"`. Run go from `C:\Users\vincent\timewarp`.
- Commits: author `Vincent Royer <vincent@flowengineering.ca>` via `git -c user.name=... -c user.email=...`; end messages with the Co-Authored-By and Claude-Session lines. Do not push.

## Review Focus

1. Backup files `timewarp-*.bak-*.db` sit in the DB folder and match the glob: the timesheet must skip them or April is double counted. Test in Task 8 (`TestLoad_SkipsBackups`).
2. Transcript lines over 1 MB (tool results with file dumps): the backfill reader must not truncate or stop. Test in Task 5 (`TestEventsFromTranscript_LongLine`).
3. settings.json missing, or already holding other hooks on the same events: install must create or preserve. Test in Task 10 (`TestMergeHooks_PreservesOthers`, `TestMergeHooks_EmptySettings`).
4. Hook run with malformed stdin, empty session id, or unwritable DB: must exit 0 and never block Claude. Test in Task 4 (`TestRunHook_GarbageInput`).
5. A focus or busy interval crossing local midnight: hours must split across both dates. Test in Task 9 (`TestBuildReport_SplitsAtMidnight`).

---

### Task 1: Portable Go toolchain and baseline

**Files:** none in repo.

- [ ] **Step 1: Download the latest stable Go zip into the scratchpad**

```powershell
$scratch = "C:\Users\vincent\AppData\Local\Temp\claude\C--Users-vincent\4d580c62-0dd3-4763-b2a0-c4e989e1ddbd\scratchpad"
$rel = (Invoke-RestMethod 'https://go.dev/dl/?mode=json')[0].version
Invoke-WebRequest "https://go.dev/dl/$rel.windows-amd64.zip" -OutFile "$scratch\go.zip"
Expand-Archive "$scratch\go.zip" -DestinationPath $scratch -Force
& "$scratch\go\bin\go.exe" version
```
Expected: `go version go1.2x.y windows/amd64` with x >= 4.

- [ ] **Step 2: Run the existing tests**

```powershell
$env:PATH = "$scratch\go\bin;" + $env:PATH; Set-Location C:\Users\vincent\timewarp; go test ./...
```
Expected: all packages `ok`. Record any pre-existing failure before continuing; do not fix unrelated failures.

---

### Task 2: DB busy timeout, claude_events table, Claude window titles

**Files:**
- Create: `internal/claudewin/claudewin.go`, `internal/claudewin/claudewin_test.go`
- Create: `internal/db/claude_events.go`, `internal/db/claude_events_test.go`
- Modify: `internal/db/db.go` (Open DSN, initSchema, RecordFocus)
- Modify: `internal/db/db_test.go` (add title-split tests)

**Interfaces:**
- Produces: `claudewin.IsClaudeProcess(proc string) bool`, `claudewin.NormalizeTitle(title string) string`
- Produces: `db.ClaudeEvent{TS time.Time; Hostname, Username, SessionID, SessionTitle, Event, RefKind, RefNumber, RefSource string}`, `(*db.Tracker).InsertClaudeEvents([]db.ClaudeEvent) (int, error)`, `(*db.Tracker).LastSessionTitle(sessionID string) string`

- [ ] **Step 1: Write failing tests**

`internal/claudewin/claudewin_test.go`:
```go
package claudewin

import "testing"

func TestNormalizeTitle(t *testing.T) {
	cases := map[string]string{
		"✳ Timewarp reliability issues":       "Timewarp reliability issues",
		"⠏ vincent":                           "vincent",
		"◐ EPR from alex":                     "EPR from alex",
		"◑ Scirocco-boost Hardware Plan V2":   "Scirocco-boost Hardware Plan V2",
		"Claude":                              "Claude",
		"26-104 load calc.xlsx - Excel":       "26-104 load calc.xlsx - Excel",
		"  ✳  spaced":                         "spaced",
		"":                                    "",
	}
	for in, want := range cases {
		if got := NormalizeTitle(in); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsClaudeProcess(t *testing.T) {
	for _, p := range []string{"WindowsTerminal.exe", "claude.exe", "Claude.exe", "Code.exe"} {
		if !IsClaudeProcess(p) {
			t.Errorf("%s should be a Claude process", p)
		}
	}
	for _, p := range []string{"EXCEL.EXE", "acad.exe", ""} {
		if IsClaudeProcess(p) {
			t.Errorf("%s should not be a Claude process", p)
		}
	}
}
```

`internal/db/claude_events_test.go`:
```go
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
```

Append to `internal/db/db_test.go`:
```go
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
```

- [ ] **Step 2: Run tests, expect compile failures**

Run: `go test ./internal/claudewin/ ./internal/db/`
Expected: FAIL (`undefined: NormalizeTitle`, `undefined: ClaudeEvent`).

- [ ] **Step 3: Implement**

`internal/claudewin/claudewin.go`:
```go
// Package claudewin identifies Claude Code windows and normalises their titles.
package claudewin

import (
	"strings"
	"unicode"
)

var claudeProcs = map[string]bool{
	"windowsterminal.exe": true,
	"claude.exe":          true,
	"code.exe":            true,
}

// IsClaudeProcess reports whether a focused process can host a Claude Code session.
func IsClaudeProcess(proc string) bool {
	return claudeProcs[strings.ToLower(proc)]
}

// NormalizeTitle strips the leading status glyph (spinner, ✳, ◐ ...) and
// whitespace that Claude Code puts in front of the session title.
func NormalizeTitle(title string) string {
	return strings.TrimSpace(strings.TrimLeftFunc(title, func(r rune) bool {
		return unicode.IsSymbol(r) || unicode.IsSpace(r)
	}))
}
```

`internal/db/claude_events.go`:
```go
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
```

`internal/db/db.go` changes:
1. Import `github.com/vinistoisr/timewarp/internal/claudewin`.
2. In `Open`, replace the DSN line with:
```go
	dsn := fmt.Sprintf("file:%s/%s?_pragma=busy_timeout(5000)", dbpath, dbFile)
```
3. In `initSchema`, append `claudeEventsTable, claudeEventsIndex,` to `stmts`.
4. In `RecordFocus`, replace
```go
	sameProcess := strings.EqualFold(t.pending.processName, processName)
	gap := now.Sub(t.pending.lastSeen)

	if sameProcess && gap <= bridgeThreshold {
```
with
```go
	sameWindow := strings.EqualFold(t.pending.processName, processName)
	if sameWindow && claudewin.IsClaudeProcess(processName) {
		// Separate Claude sessions share one terminal process; split on title,
		// ignoring the status glyph that changes every second.
		sameWindow = claudewin.NormalizeTitle(t.pending.windowTitle) == claudewin.NormalizeTitle(windowTitle)
	}
	gap := now.Sub(t.pending.lastSeen)

	if sameWindow && gap <= bridgeThreshold {
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/claudewin/ ./internal/db/`
Expected: PASS (all, including the existing stitching tests).

- [ ] **Step 5: Commit**

```powershell
git add internal/claudewin internal/db
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Add claude_events table, apply SQLite busy timeout, split Claude windows by title" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 3: Project and proposal number detection

**Files:**
- Create: `internal/claude/refs.go`, `internal/claude/refs_test.go`

**Interfaces:**
- Produces: `claude.Ref{Kind, Number, Source string}`, `claude.ExtractRefs(prompt string, toolInput json.RawMessage, cwd string) []claude.Ref`, `claude.Strength(kind string) int`, `claude.Numbers(s string) []string`

- [ ] **Step 1: Write failing tests**

`internal/claude/refs_test.go`:
```go
package claude

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNumbers(t *testing.T) {
	got := Numbers(`26-104,26-105 on 2026-09-30, breaker 20-100A, 126-104, 26-1045`)
	want := []string{"26-104", "26-105", "20-100"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Numbers = %v, want %v", got, want)
	}
}

func TestExtractRefs_ProjectPathInToolInput(t *testing.T) {
	in := json.RawMessage(`{"file_path":"S:\\PROJECT\\2026\\26-104 CBC Arc Flash\\Working\\calc.xlsx"}`)
	got := ExtractRefs("", in, `C:\Users\vincent`)
	want := []Ref{{"project", "26-104", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_ForwardSlashCommand(t *testing.T) {
	in := json.RawMessage(`{"command":"python \"s:/project/2025/25-245 Canada Place/x.py\" && ls"}`)
	got := ExtractRefs("", in, "")
	want := []Ref{{"project", "25-245", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_ProposalPath(t *testing.T) {
	in := json.RawMessage(`{"path":"S:\\Proposals\\VR\\2025\\October\\prop 25-415 - Beta Ave Burnaby"}`)
	got := ExtractRefs("", in, "")
	want := []Ref{{"proposal", "25-415", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_NestedToolInput(t *testing.T) {
	in := json.RawMessage(`{"edits":[{"file":"S:/PROJECT/2026/26-087 GI/a.docx"}],"n":3}`)
	got := ExtractRefs("", in, "")
	want := []Ref{{"project", "26-087", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_PromptWords(t *testing.T) {
	got := ExtractRefs("update proposal 26-555 and project 26-104, also look at 26-060 on 2026-09-30", nil, "")
	want := []Ref{
		{"project", "26-104", "prompt"},
		{"proposal", "26-555", "prompt"},
		{"mention", "26-060", "prompt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_CwdAndStrongestWins(t *testing.T) {
	got := ExtractRefs("check 26-104 please", nil, `S:\PROJECT\2026\26-104 CBC`)
	want := []Ref{{"project", "26-104", "cwd"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_ToolInputBareNumberIgnored(t *testing.T) {
	got := ExtractRefs("", json.RawMessage(`{"query":"26-104 invoice"}`), "")
	if len(got) != 0 {
		t.Errorf("bare numbers in tool input must be ignored, got %v", got)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/claude/`
Expected: FAIL (`undefined: Numbers`).

- [ ] **Step 3: Implement**

`internal/claude/refs.go`:
```go
// Package claude records Claude Code activity for project time attribution.
package claude

import (
	"encoding/json"
	"regexp"
	"sort"
)

// Ref is a project or proposal number found in a Claude event.
type Ref struct {
	Kind   string // project, proposal, mention
	Number string
	Source string // path, prompt, cwd
}

var (
	tokenRe    = regexp.MustCompile(`\d+-\d+`)
	numberRe   = regexp.MustCompile(`^2\d-\d{3}$`)
	projPathRe = regexp.MustCompile(`(?i)[\\/]PROJECT[\\/]+20\d\d[\\/]+(2\d-\d{3})(?:\D|$)`)
	propPathRe = regexp.MustCompile(`(?i)[\\/]Proposals[\\/][^"'\r\n]*?(?:^|\D)(2\d-\d{3})(?:\D|$)`)
	propWordRe = regexp.MustCompile(`(?i)\b(?:proposal|prop)\.?\s*#?\s*(2\d-\d{3})(?:\D|$)`)
	projWordRe = regexp.MustCompile(`(?i)\bproject\s*#?\s*(2\d-\d{3})(?:\D|$)`)
)

// Strength orders ref kinds: project > proposal > mention.
func Strength(kind string) int {
	switch kind {
	case "project":
		return 3
	case "proposal":
		return 2
	case "mention":
		return 1
	}
	return 0
}

// Numbers returns the NN-NNN tokens in s that are not part of a longer
// digit-dash-digit token (so dates like 2026-09-30 do not match).
func Numbers(s string) []string {
	var out []string
	for _, tok := range tokenRe.FindAllString(s, -1) {
		if numberRe.MatchString(tok) {
			out = append(out, tok)
		}
	}
	return out
}

// ExtractRefs finds project and proposal numbers in a prompt, a tool input
// and the working directory. Each number appears once, with its strongest kind.
func ExtractRefs(prompt string, toolInput json.RawMessage, cwd string) []Ref {
	var found []Ref
	add := func(kind, num, src string) { found = append(found, Ref{kind, num, src}) }

	pathRefs := func(s, src string) {
		for _, m := range projPathRe.FindAllStringSubmatch(s, -1) {
			add("project", m[1], src)
		}
		for _, m := range propPathRe.FindAllStringSubmatch(s, -1) {
			add("proposal", m[1], src)
		}
	}

	for _, s := range jsonStrings(toolInput) {
		pathRefs(s, "path")
	}
	pathRefs(cwd, "cwd")
	if prompt != "" {
		pathRefs(prompt, "path")
		for _, m := range projWordRe.FindAllStringSubmatch(prompt, -1) {
			add("project", m[1], "prompt")
		}
		for _, m := range propWordRe.FindAllStringSubmatch(prompt, -1) {
			add("proposal", m[1], "prompt")
		}
		for _, n := range Numbers(prompt) {
			add("mention", n, "prompt")
		}
	}

	// Keep the strongest ref per number; order by strength, then first appearance.
	best := map[string]int{}
	var order []string
	for i, r := range found {
		j, ok := best[r.Number]
		if !ok {
			best[r.Number] = i
			order = append(order, r.Number)
		} else if Strength(r.Kind) > Strength(found[j].Kind) {
			best[r.Number] = i
		}
	}
	out := make([]Ref, 0, len(order))
	for _, n := range order {
		out = append(out, found[best[n]])
	}
	sort.SliceStable(out, func(a, b int) bool { return Strength(out[a].Kind) > Strength(out[b].Kind) })
	return out
}

// jsonStrings returns every string value in a JSON document, recursively.
func jsonStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}
```

Note on `TestExtractRefs_CwdAndStrongestWins`: the cwd ref (project, cwd) is found before the prompt mention, so it wins on both order and strength.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/claude/`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/claude
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Detect project and proposal numbers in Claude prompts, tool inputs and paths" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 4: Hook command

**Files:**
- Create: `internal/claude/title.go`, `internal/claude/hook.go`, `internal/claude/hook_test.go`
- Modify: `exporter.go` (flag and dispatch)

**Interfaces:**
- Consumes: `ExtractRefs`, `db.Open`, `(*db.Tracker).InsertClaudeEvents`, `LastSessionTitle`
- Produces: `claude.LastTitle(transcriptPath string) string`, `claude.HookInput`, `claude.BuildEvents(in HookInput, ts time.Time, host, user, title string) []db.ClaudeEvent`, `claude.RunHook(r io.Reader, dbpath string) error`

- [ ] **Step 1: Write failing tests**

`internal/claude/hook_test.go`:
```go
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
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/claude/`
Expected: FAIL (`undefined: LastTitle`).

- [ ] **Step 3: Implement**

`internal/claude/title.go`:
```go
package claude

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
)

const titleTailBytes = 1 << 20

// LastTitle returns the last ai-title recorded in a transcript, reading only
// the final 1 MB (transcripts reach tens of MB). Empty if none found.
func LastTitle(transcriptPath string) string {
	f, err := os.Open(transcriptPath)
	if err != nil {
		return ""
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > titleTailBytes {
		f.Seek(st.Size()-titleTailBytes, io.SeekStart)
	}
	data, err := io.ReadAll(f)
	if err != nil {
		return ""
	}
	title := ""
	for _, line := range bytes.Split(data, []byte("\n")) {
		if !bytes.Contains(line, []byte(`"ai-title"`)) {
			continue
		}
		var rec struct {
			Type    string `json:"type"`
			AITitle string `json:"aiTitle"`
		}
		if json.Unmarshal(line, &rec) == nil && rec.Type == "ai-title" && rec.AITitle != "" {
			title = rec.AITitle
		}
	}
	return title
}
```

`internal/claude/hook.go`:
```go
package claude

import (
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
	if err := json.NewDecoder(r).Decode(&in); err != nil {
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
```

`db.Open` on a missing directory fails at `initSchema` (SQLite cannot create the file), which satisfies the unwritable-path test.

`exporter.go` changes:
1. Add to the flag var block: `claudeHookMode bool`.
2. In `init()`: `flag.BoolVar(&claudeHookMode, "claudehook", false, "Record a Claude Code hook event from stdin (used by Claude Code hooks)")`
3. Import `"github.com/vinistoisr/timewarp/internal/claude"`.
4. In `main()`, directly after `flag.Parse()`:
```go
	if claudeHookMode {
		path := dbpath
		if path == "" {
			path = db.ExeDir()
		}
		if err := claude.RunHook(os.Stdin, path); err != nil {
			logHookError(path, err)
		}
		return // always exit 0: never block Claude
	}
```
5. Add:
```go
// logHookError appends a hook failure to timewarp-hook.log in the DB folder.
func logHookError(dir string, err error) {
	f, ferr := os.OpenFile(filepath.Join(dir, "timewarp-hook.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if ferr != nil {
		return
	}
	defer f.Close()
	fmt.Fprintf(f, "%s %v\n", time.Now().Format(time.RFC3339), err)
}
```

- [ ] **Step 4: Run tests and build**

Run: `go test ./internal/claude/ && go build -o $env:TEMP\tw-check.exe .`
Expected: PASS and a clean build.

- [ ] **Step 5: Commit**

```powershell
git add internal/claude exporter.go
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Add -claudehook command that records Claude Code events" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 5: Transcript backfill

**Files:**
- Create: `internal/claude/backfill.go`, `internal/claude/backfill_test.go`
- Modify: `exporter.go` (flag and dispatch)

**Interfaces:**
- Consumes: `ExtractRefs`, `eventsFor`, `db.Open`, `InsertClaudeEvents`
- Produces: `claude.EventsFromTranscript(r io.Reader, host, user string) ([]db.ClaudeEvent, error)`, `claude.Backfill(root, dbpath string) (files, inserted int, err error)`

- [ ] **Step 1: Write failing tests**

`internal/claude/backfill_test.go`:
```go
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
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/claude/`
Expected: FAIL (`undefined: EventsFromTranscript`).

- [ ] **Step 3: Implement**

`internal/claude/backfill.go`:
```go
package claude

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vinistoisr/timewarp/internal/db"
)

type transcriptLine struct {
	Type      string `json:"type"`
	SessionID string `json:"sessionId"`
	Timestamp string `json:"timestamp"`
	Cwd       string `json:"cwd"`
	AITitle   string `json:"aiTitle"`
	IsMeta    bool   `json:"isMeta"`
	Message   *struct {
		Content json.RawMessage `json:"content"`
	} `json:"message"`
}

type contentBlock struct {
	Type  string          `json:"type"`
	Text  string          `json:"text"`
	Input json.RawMessage `json:"input"`
}

func blocks(raw json.RawMessage) (text string, list []contentBlock) {
	if len(raw) == 0 {
		return "", nil
	}
	if raw[0] == '"' {
		json.Unmarshal(raw, &text)
		return text, nil
	}
	json.Unmarshal(raw, &list)
	var parts []string
	for _, b := range list {
		if b.Type == "text" {
			parts = append(parts, b.Text)
		}
	}
	return strings.Join(parts, "\n"), list
}

// EventsFromTranscript rebuilds the events the hook would have written from a
// Claude Code transcript: prompts, tool calls, and a stop at the last
// timestamped line of each turn. Titles are the latest ai-title seen, and the
// session's final title fills events that came before the first one.
func EventsFromTranscript(r io.Reader, host, user string) ([]db.ClaudeEvent, error) {
	br := bufio.NewReaderSize(r, 1<<20)
	var evs []db.ClaudeEvent
	title := ""
	var lastTS time.Time
	lastSession := ""
	inTurn := false

	stop := func() {
		if inTurn && !lastTS.IsZero() {
			evs = append(evs, eventsFor(nil, db.ClaudeEvent{TS: lastTS, Hostname: host, Username: user,
				SessionID: lastSession, SessionTitle: title, Event: "stop"})...)
		}
		inTurn = false
	}

	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			var l transcriptLine
			if json.Unmarshal(line, &l) == nil {
				if l.Type == "ai-title" && l.AITitle != "" {
					title = l.AITitle
				} else if ts, perr := time.Parse(time.RFC3339Nano, l.Timestamp); perr == nil && l.SessionID != "" {
					base := db.ClaudeEvent{TS: ts.UTC(), Hostname: host, Username: user,
						SessionID: l.SessionID, SessionTitle: title}
					var content json.RawMessage
					if l.Message != nil {
						content = l.Message.Content
					}
					text, list := blocks(content)
					switch l.Type {
					case "user":
						if text != "" && !l.IsMeta {
							stop()
							base.Event = "prompt"
							evs = append(evs, eventsFor(ExtractRefs(text, nil, l.Cwd), base)...)
							inTurn = true
						}
					case "assistant":
						for _, b := range list {
							if b.Type == "tool_use" {
								base.Event = "tool"
								evs = append(evs, eventsFor(ExtractRefs("", b.Input, l.Cwd), base)...)
							}
						}
					}
					lastTS, lastSession = ts.UTC(), l.SessionID
				}
			}
		}
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return nil, err
		}
	}
	stop()
	for i := range evs {
		if evs[i].SessionTitle == "" {
			evs[i].SessionTitle = title
		}
	}
	return evs, nil
}

// Backfill imports every *.jsonl transcript under root into the DB in dbpath.
func Backfill(root, dbpath string) (files, inserted int, err error) {
	t, err := db.Open(dbpath)
	if err != nil {
		return 0, 0, err
	}
	defer t.Close()
	host, _ := os.Hostname()
	user := os.Getenv("USERNAME")
	walkErr := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(p, ".jsonl") {
			return nil
		}
		f, err := os.Open(p)
		if err != nil {
			return nil
		}
		evs, err := EventsFromTranscript(f, host, user)
		f.Close()
		if err != nil {
			return nil
		}
		n, err := t.InsertClaudeEvents(evs)
		if err != nil {
			return err
		}
		files++
		inserted += n
		return nil
	})
	return files, inserted, walkErr
}
```

`exporter.go` changes:
1. Flag var `claudeBackfillMode bool`; in `init()`: `flag.BoolVar(&claudeBackfillMode, "claudebackfill", false, "Import existing Claude Code transcripts into the DB")`.
2. In `main()` after the `claudeHookMode` block:
```go
	if claudeBackfillMode {
		path := dbpath
		if path == "" {
			path = db.ExeDir()
		}
		root := filepath.Join(os.Getenv("USERPROFILE"), ".claude", "projects")
		files, n, err := claude.Backfill(root, path)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Backfill error: %v\n", err)
			os.Exit(1)
		}
		fmt.Printf("Backfill: %d transcripts read, %d events added.\n", files, n)
		return
	}
```

- [ ] **Step 4: Run tests and build**

Run: `go test ./internal/claude/ && go build -o $env:TEMP\tw-check.exe .`
Expected: PASS, clean build.

- [ ] **Step 5: Commit**

```powershell
git add internal/claude exporter.go
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Add -claudebackfill to import existing Claude transcripts" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 6: Interval arithmetic

**Files:**
- Create: `internal/timesheet/interval.go`, `internal/timesheet/interval_test.go`

**Interfaces:**
- Produces: `timesheet.Interval{Start, End time.Time}`, `Union([]Interval) []Interval`, `Subtract(a, b []Interval) []Interval`, `Total([]Interval) time.Duration`, `SplitDays(iv Interval, loc *time.Location) map[string]Interval`, `CutAt(iv Interval, cuts []time.Time) []Interval`

- [ ] **Step 1: Write failing tests**

`internal/timesheet/interval_test.go`:
```go
package timesheet

import (
	"testing"
	"time"
)

var t0 = time.Date(2026, 9, 30, 9, 0, 0, 0, time.UTC)

func iv(a, b int) Interval { // minutes after t0
	return Interval{t0.Add(time.Duration(a) * time.Minute), t0.Add(time.Duration(b) * time.Minute)}
}

func TestUnion(t *testing.T) {
	got := Union([]Interval{iv(30, 40), iv(0, 10), iv(5, 20), iv(20, 25), iv(50, 50)})
	want := []Interval{iv(0, 25), iv(30, 40)}
	if len(got) != len(want) || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Union = %v, want %v", got, want)
	}
	if Total(got) != 35*time.Minute {
		t.Errorf("Total = %v", Total(got))
	}
}

func TestSubtract(t *testing.T) {
	got := Subtract([]Interval{iv(0, 60)}, []Interval{iv(10, 20), iv(50, 70)})
	want := []Interval{iv(0, 10), iv(20, 50)}
	if len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Errorf("Subtract = %v, want %v", got, want)
	}
}

func TestCutAt(t *testing.T) {
	got := CutAt(iv(0, 60), []time.Time{t0.Add(-time.Minute), t0.Add(20 * time.Minute), t0.Add(40 * time.Minute), t0.Add(90 * time.Minute)})
	if len(got) != 3 || got[0] != iv(0, 20) || got[1] != iv(20, 40) || got[2] != iv(40, 60) {
		t.Errorf("CutAt = %v", got)
	}
}

func TestSplitDays(t *testing.T) {
	loc, _ := time.LoadLocation("America/Vancouver")
	start := time.Date(2026, 9, 30, 23, 0, 0, 0, loc)
	parts := SplitDays(Interval{start, start.Add(3 * time.Hour)}, loc)
	if len(parts) != 2 || parts["2026-09-30"].End.Sub(parts["2026-09-30"].Start) != time.Hour ||
		parts["2026-10-01"].End.Sub(parts["2026-10-01"].Start) != 2*time.Hour {
		t.Errorf("SplitDays = %v", parts)
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/timesheet/`
Expected: FAIL (`undefined: Interval`).

- [ ] **Step 3: Implement**

`internal/timesheet/interval.go`:
```go
// Package timesheet turns focus and Claude activity into hours per billing code.
package timesheet

import (
	"sort"
	"time"
)

// Interval is a half-open time span [Start, End).
type Interval struct {
	Start, End time.Time
}

func (i Interval) empty() bool { return !i.End.After(i.Start) }

// Union merges overlapping or touching intervals and drops empty ones.
func Union(in []Interval) []Interval {
	var s []Interval
	for _, i := range in {
		if !i.empty() {
			s = append(s, i)
		}
	}
	sort.Slice(s, func(a, b int) bool { return s[a].Start.Before(s[b].Start) })
	var out []Interval
	for _, i := range s {
		if n := len(out); n > 0 && !i.Start.After(out[n-1].End) {
			if i.End.After(out[n-1].End) {
				out[n-1].End = i.End
			}
			continue
		}
		out = append(out, i)
	}
	return out
}

// Subtract removes every part of a covered by b.
func Subtract(a, b []Interval) []Interval {
	b = Union(b)
	var out []Interval
	for _, x := range Union(a) {
		cur := x
		for _, y := range b {
			if !y.End.After(cur.Start) || !y.Start.Before(cur.End) {
				continue
			}
			if y.Start.After(cur.Start) {
				out = append(out, Interval{cur.Start, y.Start})
			}
			cur.Start = y.End
			if cur.empty() {
				break
			}
		}
		if !cur.empty() {
			out = append(out, cur)
		}
	}
	return out
}

// Total sums interval lengths (callers pass a Union for exclusive totals).
func Total(in []Interval) time.Duration {
	var d time.Duration
	for _, i := range in {
		if !i.empty() {
			d += i.End.Sub(i.Start)
		}
	}
	return d
}

// CutAt splits iv at each cut time strictly inside it.
func CutAt(iv Interval, cuts []time.Time) []Interval {
	sorted := append([]time.Time(nil), cuts...)
	sort.Slice(sorted, func(a, b int) bool { return sorted[a].Before(sorted[b]) })
	var out []Interval
	cur := iv
	for _, c := range sorted {
		if c.After(cur.Start) && c.Before(cur.End) {
			out = append(out, Interval{cur.Start, c})
			cur.Start = c
		}
	}
	return append(out, cur)
}

// SplitDays splits iv at local midnights, keyed by local date YYYY-MM-DD.
func SplitDays(iv Interval, loc *time.Location) map[string]Interval {
	out := map[string]Interval{}
	cur := iv.Start
	for cur.Before(iv.End) {
		l := cur.In(loc)
		next := time.Date(l.Year(), l.Month(), l.Day()+1, 0, 0, 0, 0, loc)
		end := iv.End
		if next.Before(end) {
			end = next
		}
		out[l.Format("2006-01-02")] = Interval{cur, end}
		cur = end
	}
	return out
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/timesheet/`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/timesheet
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Add interval arithmetic for timesheet attribution" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 7: Project and proposal folder index

**Files:**
- Create: `internal/timesheet/folders.go`, `internal/timesheet/folders_test.go`

**Interfaces:**
- Consumes: `claude.Numbers`
- Produces: `timesheet.Folders{Projects, Proposals map[string][]string}`, `ScanFolders(projectRoot, proposalRoot string) Folders`, `(Folders).Classify(kind, number string) string` returning `"project"`, `"proposal"` or `""`

- [ ] **Step 1: Write failing tests**

`internal/timesheet/folders_test.go`:
```go
package timesheet

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanFoldersAndClassify(t *testing.T) {
	proj := t.TempDir()
	prop := t.TempDir()
	for _, d := range []string{
		filepath.Join(proj, "2026", "26-000 Flow HQ (M&E)"),
		filepath.Join(proj, "2026", "26-000 KITT (M&E)"),
		filepath.Join(proj, "2026", "26-104 CBC Arc Flash (E)"),
		filepath.Join(proj, "misc"),
		filepath.Join(prop, "VR", "2025", "October", "prop 25-415 - Beta Ave", "sub 26-999"),
		filepath.Join(prop, "VR", "2026", "26-555 BCS 561 Pacific Pointe"),
	} {
		os.MkdirAll(d, 0o755)
	}
	f := ScanFolders(proj, prop)
	if len(f.Projects["26-000"]) != 2 || f.Projects["26-104"][0] != "26-104 CBC Arc Flash (E)" {
		t.Errorf("projects = %v", f.Projects)
	}
	if f.Proposals["25-415"][0] != "prop 25-415 - Beta Ave" || f.Proposals["26-555"] == nil {
		t.Errorf("proposals = %v", f.Proposals)
	}
	if _, ok := f.Proposals["26-999"]; ok {
		t.Error("must not descend into a proposal folder")
	}
	cases := []struct{ kind, num, want string }{
		{"project", "26-777", "project"},
		{"proposal", "26-104", "proposal"},
		{"mention", "26-104", "project"},
		{"mention", "25-415", "proposal"},
		{"mention", "26-321", ""},
	}
	for _, c := range cases {
		if got := f.Classify(c.kind, c.num); got != c.want {
			t.Errorf("Classify(%s,%s) = %q, want %q", c.kind, c.num, got, c.want)
		}
	}
}

func TestScanFolders_MissingRoots(t *testing.T) {
	f := ScanFolders(`Z:\does\not\exist`, `Z:\nope`)
	if f.Classify("mention", "26-104") != "" {
		t.Error("missing roots should resolve mentions to empty")
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/timesheet/`
Expected: FAIL (`undefined: ScanFolders`).

- [ ] **Step 3: Implement**

`internal/timesheet/folders.go`:
```go
package timesheet

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vinistoisr/timewarp/internal/claude"
)

// Default folder roots on the Flow S: drive.
const (
	DefaultProjectRoot  = `S:\PROJECT`
	DefaultProposalRoot = `S:\Proposals`
)

var yearDirRe = regexp.MustCompile(`^20\d\d$`)

// Folders maps numbers to folder names for projects and proposals.
type Folders struct {
	Projects  map[string][]string
	Proposals map[string][]string
}

// ScanFolders indexes <projectRoot>\<year>\<NN-NNN ...> folders and any folder
// up to five levels under proposalRoot whose name contains a number.
// Missing roots give empty maps.
func ScanFolders(projectRoot, proposalRoot string) Folders {
	f := Folders{Projects: map[string][]string{}, Proposals: map[string][]string{}}
	years, _ := os.ReadDir(projectRoot)
	for _, y := range years {
		if !y.IsDir() || !yearDirRe.MatchString(y.Name()) {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(projectRoot, y.Name()))
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if nums := claude.Numbers(e.Name()); len(nums) > 0 && strings.HasPrefix(e.Name(), nums[0]) {
				f.Projects[nums[0]] = append(f.Projects[nums[0]], e.Name())
			}
		}
	}
	base := strings.Count(filepath.Clean(proposalRoot), string(os.PathSeparator))
	filepath.WalkDir(proposalRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if strings.Count(filepath.Clean(p), string(os.PathSeparator))-base > 5 {
			return fs.SkipDir
		}
		if p == proposalRoot {
			return nil
		}
		if nums := claude.Numbers(d.Name()); len(nums) > 0 {
			f.Proposals[nums[0]] = append(f.Proposals[nums[0]], d.Name())
			return fs.SkipDir
		}
		return nil
	})
	return f
}

// Classify resolves a ref kind to "project", "proposal" or "" (unresolved).
func (f Folders) Classify(kind, number string) string {
	switch kind {
	case "project", "proposal":
		return kind
	case "mention":
		if len(f.Projects[number]) > 0 {
			return "project"
		}
		if len(f.Proposals[number]) > 0 {
			return "proposal"
		}
	}
	return ""
}
```

- [ ] **Step 4: Run tests**

Run: `go test ./internal/timesheet/`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/timesheet
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Index project and proposal folders for number resolution" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 8: Load all PC databases

**Files:**
- Create: `internal/timesheet/load.go`, `internal/timesheet/load_test.go`

**Interfaces:**
- Consumes: `db.ClaudeEvent`, `db.Open`, `InsertClaudeEvents`
- Produces: `timesheet.Focus{Host, Process, Title, Project string; Interval}`, `timesheet.HostSpan{Host string; Interval}`, `timesheet.Data{Focus []Focus; Idle []HostSpan; Events []db.ClaudeEvent}`, `Load(dbpath string, from, to time.Time) (Data, error)`, `ParseDBTime(v any) (time.Time, error)`

- [ ] **Step 1: Write failing tests**

`internal/timesheet/load_test.go`:
```go
package timesheet

import (
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
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/timesheet/`
Expected: FAIL (`undefined: Load`).

- [ ] **Step 3: Implement**

`internal/timesheet/load.go`:
```go
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
```

Events are loaded with a one-day pad on both sides and not clipped, so a turn that started before `from` still has its prompt.

- [ ] **Step 4: Run tests**

Run: `go test ./internal/timesheet/`
Expected: PASS.

- [ ] **Step 5: Commit**

```powershell
git add internal/timesheet
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Load focus, idle and Claude events from every PC database, skipping backups" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 9: Attribution and report

**Files:**
- Create: `internal/timesheet/attribute.go`, `internal/timesheet/attribute_test.go`
- Create: `internal/timesheet/report.go`, `internal/timesheet/report_test.go`

**Interfaces:**
- Consumes: `Interval`, `Union`, `Subtract`, `CutAt`, `SplitDays`, `Total`, `Folders`, `Data`, `Focus`, `HostSpan`, `claudewin.*`, `claude.Strength`
- Produces:
  - `timesheet.Options{Idle, TurnGap, Recent time.Duration}`, `DefaultOptions()`
  - `timesheet.Assignment{Kind, Code, Label string; Interval}` with Kind in `project`, `admin`, `unassigned`, `other`
  - `Attribute(d Data, f Folders, o Options) (asg []Assignment, clock []Interval)`
  - `timesheet.Row{Date, Code, Name string; RawHours, Hours float64; Notes []string}`, `timesheet.Day{Date string; ProjectHours, ClockHours float64}`, `timesheet.Report{From, To string; Rows, Unassigned, Other []Row; Days []Day}`
  - `BuildReport(asg []Assignment, clock []Interval, f Folders, loc *time.Location, from, to time.Time) Report`
  - `RoundHours(raw float64) float64`

- [ ] **Step 1: Write failing tests**

`internal/timesheet/attribute_test.go`:
```go
package timesheet

import (
	"testing"
	"time"

	"github.com/vinistoisr/timewarp/internal/db"
)

func ev(min int, session, title, event, kind, num string) db.ClaudeEvent {
	return db.ClaudeEvent{TS: t0.Add(time.Duration(min) * time.Minute), Hostname: "H", Username: "u",
		SessionID: session, SessionTitle: title, Event: event, RefKind: kind, RefNumber: num}
}

func focus(a, b int, proc, title, proj string) Focus {
	return Focus{Host: "H", Process: proc, Title: title, Project: proj, Interval: iv(a, b)}
}

func codeTotals(asg []Assignment) map[string]time.Duration {
	by := map[string][]Interval{}
	for _, a := range asg {
		by[a.Code] = append(by[a.Code], a.Interval)
	}
	out := map[string]time.Duration{}
	for k, v := range by {
		out[k] = Total(Union(v))
	}
	return out
}

var testFolders = Folders{
	Projects:  map[string][]string{"26-104": {"26-104 CBC"}, "26-087": {"26-087 GI"}},
	Proposals: map[string][]string{"26-555": {"prop 26-555 Pacific Pointe"}},
}

func TestAttribute_ParallelSessionsBothCount(t *testing.T) {
	d := Data{Events: []db.ClaudeEvent{
		ev(0, "a", "A", "prompt", "project", "26-104"), ev(30, "a", "A", "tool", "none", ""), ev(60, "a", "A", "stop", "none", ""),
		ev(0, "b", "B", "prompt", "project", "26-087"), ev(30, "b", "B", "tool", "none", ""), ev(60, "b", "B", "stop", "none", ""),
	}}
	got := codeTotals(mustAttr(t, d))
	if got["26-104"] != time.Hour || got["26-087"] != time.Hour {
		t.Errorf("totals = %v, want 1h each", got)
	}
}

func TestAttribute_SameProjectCountsOnce(t *testing.T) {
	d := Data{
		Events: []db.ClaudeEvent{ev(0, "a", "A", "prompt", "project", "26-104"), ev(9, "a", "A", "tool", "none", ""), ev(60, "a", "A", "stop", "none", "")},
		Focus:  []Focus{focus(0, 60, "EXCEL.EXE", "26-104 calc.xlsx", "26-104")},
	}
	// Event gap 9 -> 60 exceeds TurnGap, so the busy run is 0-9; Excel covers 0-60.
	got := codeTotals(mustAttr(t, d))
	if got["26-104"] != time.Hour {
		t.Errorf("26-104 = %v, want 1h (union, not sum)", got["26-104"])
	}
}

func TestAttribute_IdleOverTenMinutesExcluded(t *testing.T) {
	d := Data{
		Focus: []Focus{focus(0, 60, "EXCEL.EXE", "26-104 calc.xlsx", "26-104")},
		Idle:  []HostSpan{{"H", iv(10, 15)}, {"H", iv(20, 40)}},
	}
	got := codeTotals(mustAttr(t, d))
	if got["26-104"] != 40*time.Minute {
		t.Errorf("26-104 = %v, want 40m (5 min idle kept, 20 min idle removed)", got["26-104"])
	}
}

func TestAttribute_UnattendedTurnCounts(t *testing.T) {
	d := Data{
		Events: []db.ClaudeEvent{ev(0, "a", "A", "prompt", "project", "26-104"), ev(5, "a", "A", "tool", "none", ""), ev(12, "a", "A", "tool", "none", ""), ev(20, "a", "A", "stop", "none", "")},
		Idle:   []HostSpan{{"H", iv(1, 120)}},
	}
	got := codeTotals(mustAttr(t, d))
	if got["26-104"] != 20*time.Minute {
		t.Errorf("26-104 = %v, want 20m", got["26-104"])
	}
}

func TestAttribute_TurnSplitsAtGap(t *testing.T) {
	d := Data{Events: []db.ClaudeEvent{
		ev(0, "a", "A", "prompt", "project", "26-104"), ev(5, "a", "A", "tool", "none", ""),
		ev(100, "a", "A", "tool", "none", ""), ev(104, "a", "A", "stop", "none", ""),
	}}
	got := codeTotals(mustAttr(t, d))
	if got["26-104"] != 9*time.Minute {
		t.Errorf("26-104 = %v, want 9m (0-5 and 100-104)", got["26-104"])
	}
}

func TestAttribute_ProjectSwitchWithinSession(t *testing.T) {
	d := Data{Events: []db.ClaudeEvent{
		ev(0, "a", "Inbox", "prompt", "none", ""), ev(5, "a", "Inbox", "tool", "project", "26-104"),
		ev(10, "a", "Inbox", "tool", "project", "26-087"), ev(15, "a", "Inbox", "stop", "none", ""),
	}}
	got := codeTotals(mustAttr(t, d))
	if got["26-104"] != 10*time.Minute || got["26-087"] != 5*time.Minute {
		t.Errorf("totals = %v, want 26-104 10m (incl. before first ref), 26-087 5m", got)
	}
}

func TestAttribute_FocusMatchesSessionByTitle(t *testing.T) {
	d := Data{
		Events: []db.ClaudeEvent{ev(0, "a", "Arc flash calc", "prompt", "project", "26-104"), ev(1, "a", "Arc flash calc", "stop", "none", "")},
		Focus:  []Focus{focus(0, 30, "WindowsTerminal.exe", "✳ Arc flash calc", "")},
	}
	got := codeTotals(mustAttr(t, d))
	if got["26-104"] != 30*time.Minute {
		t.Errorf("26-104 = %v, want 30m (reading and typing time counts)", got["26-104"])
	}
}

func TestAttribute_GenericTitleUsesRecentSession(t *testing.T) {
	d := Data{
		Events: []db.ClaudeEvent{ev(0, "a", "X", "prompt", "project", "26-104"), ev(1, "a", "X", "stop", "none", "")},
		Focus:  []Focus{focus(1, 5, "claude.exe", "Claude", ""), focus(20, 25, "claude.exe", "Claude", "")},
	}
	asg := mustAttr(t, d)
	got := codeTotals(asg)
	if got["26-104"] != 4*time.Minute {
		t.Errorf("26-104 = %v, want 4m (0-1 busy, minutes 1-4 start within 2 min of the event at minute 1)", got["26-104"])
	}
	if got["Unassigned"] == 0 {
		t.Error("focus with no recent session should be Unassigned")
	}
}

func TestAttribute_ProposalsGoToAdmin(t *testing.T) {
	d := Data{Events: []db.ClaudeEvent{
		ev(0, "a", "P", "prompt", "mention", "26-555"), ev(10, "a", "P", "stop", "none", ""),
		ev(0, "b", "Q", "prompt", "proposal", "25-415"), ev(10, "b", "Q", "stop", "none", ""),
	}}
	got := codeTotals(mustAttr(t, d))
	if got["Admin"] != 10*time.Minute {
		t.Errorf("Admin = %v, want 10m (two parallel proposals count once)", got["Admin"])
	}
}

func TestAttribute_SessionWithoutNumberIsUnassigned(t *testing.T) {
	d := Data{Events: []db.ClaudeEvent{ev(0, "a", "Health dashboard", "prompt", "mention", "26-321"), ev(10, "a", "Health dashboard", "stop", "none", "")}}
	asg := mustAttr(t, d)
	if len(asg) == 0 || asg[0].Kind != "unassigned" || asg[0].Label != "Health dashboard" {
		t.Errorf("assignments = %+v", asg)
	}
}

func mustAttr(t *testing.T, d Data) []Assignment {
	t.Helper()
	asg, _ := Attribute(d, testFolders, DefaultOptions())
	return asg
}
```

`internal/timesheet/report_test.go`:
```go
package timesheet

import (
	"testing"
	"time"
)

func TestRoundHours(t *testing.T) {
	cases := map[float64]float64{0.1: 0, 0.17: 0.5, 0.4: 0.5, 0.74: 0.5, 0.75: 1, 1.2: 1, 3.3: 3.5}
	for in, want := range cases {
		if got := RoundHours(in); got != want {
			t.Errorf("RoundHours(%v) = %v, want %v", in, got, want)
		}
	}
}

func TestBuildReport_SplitsAtMidnight(t *testing.T) {
	loc, _ := time.LoadLocation("America/Vancouver")
	start := time.Date(2026, 9, 30, 23, 0, 0, 0, loc)
	asg := []Assignment{{Kind: "project", Code: "26-104", Label: "Arc flash", Interval: Interval{start, start.Add(3 * time.Hour)}}}
	r := BuildReport(asg, []Interval{asg[0].Interval}, testFolders, loc, start.Add(-24*time.Hour), start.Add(24*time.Hour))
	if len(r.Rows) != 2 || r.Rows[0].Date != "2026-09-30" || r.Rows[0].Hours != 1 || r.Rows[1].Hours != 2 {
		t.Fatalf("rows = %+v", r.Rows)
	}
	if r.Rows[0].Name != "26-104 CBC" || r.Rows[0].Notes[0] != "Arc flash" {
		t.Errorf("name/notes = %q %v", r.Rows[0].Name, r.Rows[0].Notes)
	}
	if len(r.Days) != 2 || r.Days[1].ClockHours != 2 {
		t.Errorf("days = %+v", r.Days)
	}
}

func TestBuildReport_Sections(t *testing.T) {
	loc := time.UTC
	asg := []Assignment{
		{Kind: "admin", Code: "Admin", Label: "prop 26-555 Pacific Pointe", Interval: iv(0, 30)},
		{Kind: "unassigned", Code: "Unassigned", Label: "Health dashboard", Interval: iv(0, 60)},
		{Kind: "other", Code: "Other: msedge.exe", Label: "News", Interval: iv(0, 20)},
	}
	r := BuildReport(asg, nil, testFolders, loc, t0.Add(-time.Hour), t0.Add(24*time.Hour))
	if len(r.Rows) != 1 || r.Rows[0].Code != "Admin" || r.Rows[0].Name != "Admin" {
		t.Errorf("rows = %+v", r.Rows)
	}
	if len(r.Unassigned) != 1 || len(r.Other) != 1 {
		t.Errorf("unassigned=%d other=%d", len(r.Unassigned), len(r.Other))
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/timesheet/`
Expected: FAIL (`undefined: Attribute`).

- [ ] **Step 3: Implement attribution**

`internal/timesheet/attribute.go`:
```go
package timesheet

import (
	"sort"
	"time"

	"github.com/vinistoisr/timewarp/internal/claude"
	"github.com/vinistoisr/timewarp/internal/claudewin"
	"github.com/vinistoisr/timewarp/internal/db"
)

// Options are the attribution thresholds.
type Options struct {
	Idle    time.Duration // inactivity at or above this is away time
	TurnGap time.Duration // event gap that ends a busy run
	Recent  time.Duration // how recent a session event must be for a generic Claude window
}

// DefaultOptions returns the thresholds from the spec.
func DefaultOptions() Options {
	return Options{Idle: 10 * time.Minute, TurnGap: 10 * time.Minute, Recent: 2 * time.Minute}
}

// Assignment is time credited to one billing code.
type Assignment struct {
	Kind  string // project, admin, unassigned, other
	Code  string // project number, "Admin", "Unassigned", "Other: <process>"
	Label string // session title, window title, or proposal folder name
	Interval
}

type point struct {
	at    time.Time
	kind  string // project or admin
	code  string
	label string
}

type session struct {
	host, id string
	titles   map[string]bool
	title    string // latest non-empty normalised title
	events   []db.ClaudeEvent
	busy     []Interval
	focus    []Interval
	points   []point
}

// Attribute assigns time to billing codes and returns the clock intervals
// (all present focus plus all busy Claude time).
func Attribute(d Data, f Folders, o Options) ([]Assignment, []Interval) {
	sessions := buildSessions(d.Events, f, o)
	byHost := map[string][]*session{}
	for _, s := range sessions {
		byHost[s.host] = append(byHost[s.host], s)
	}
	idleByHost := map[string][]Interval{}
	for _, h := range d.Idle {
		if h.End.Sub(h.Start) >= o.Idle {
			idleByHost[h.Host] = append(idleByHost[h.Host], h.Interval)
		}
	}

	var out []Assignment
	var clock []Interval
	for _, fc := range d.Focus {
		for _, piece := range Subtract([]Interval{fc.Interval}, idleByHost[fc.Host]) {
			clock = append(clock, piece)
			if claudewin.IsClaudeProcess(fc.Process) {
				out = append(out, assignClaudeFocus(fc, piece, byHost[fc.Host], o)...)
				continue
			}
			out = append(out, windowAssignment(fc, piece, f))
		}
	}
	for _, s := range sessions {
		clock = append(clock, s.busy...)
		out = append(out, s.assignments()...)
	}
	return out, clock
}

func buildSessions(evs []db.ClaudeEvent, f Folders, o Options) []*session {
	idx := map[string]*session{}
	var list []*session
	for _, e := range evs {
		key := e.Hostname + "|" + e.SessionID
		s := idx[key]
		if s == nil {
			s = &session{host: e.Hostname, id: e.SessionID, titles: map[string]bool{}}
			idx[key] = s
			list = append(list, s)
		}
		s.events = append(s.events, e)
	}
	for _, s := range list {
		sort.SliceStable(s.events, func(a, b int) bool { return s.events[a].TS.Before(s.events[b].TS) })
		for _, e := range s.events {
			if t := claudewin.NormalizeTitle(e.SessionTitle); t != "" {
				s.titles[t] = true
				s.title = t
			}
		}
		s.busy = busyRuns(s.events, o.TurnGap)
		s.points = refPoints(s.events, f)
	}
	return list
}

// busyRuns groups events into runs: a run ends at a stop/end event or when
// the next event is more than gap later.
func busyRuns(evs []db.ClaudeEvent, gap time.Duration) []Interval {
	var out []Interval
	var cur *Interval
	for _, e := range evs {
		if cur != nil && e.TS.Sub(cur.End) > gap {
			out = append(out, *cur)
			cur = nil
		}
		if cur == nil {
			cur = &Interval{e.TS, e.TS}
		} else if e.TS.After(cur.End) {
			cur.End = e.TS
		}
		if e.Event == "stop" || e.Event == "end" {
			out = append(out, *cur)
			cur = nil
		}
	}
	if cur != nil {
		out = append(out, *cur)
	}
	return Union(out)
}

// refPoints returns the moments a session's current billing code is set:
// the strongest resolvable ref of each event timestamp.
func refPoints(evs []db.ClaudeEvent, f Folders) []point {
	var pts []point
	for i := 0; i < len(evs); {
		j := i
		var best *point
		bestStrength := 0
		for ; j < len(evs) && evs[j].TS.Equal(evs[i].TS); j++ {
			e := evs[j]
			class := f.Classify(e.RefKind, e.RefNumber)
			if class == "" || claude.Strength(e.RefKind) <= bestStrength {
				continue
			}
			p := point{at: e.TS, kind: "project", code: e.RefNumber}
			if class == "proposal" {
				p = point{at: e.TS, kind: "admin", code: "Admin", label: proposalLabel(e.RefNumber, f)}
			}
			best, bestStrength = &p, claude.Strength(e.RefKind)
		}
		if best != nil {
			pts = append(pts, *best)
		}
		i = j
	}
	return pts
}

func proposalLabel(num string, f Folders) string {
	if names := f.Proposals[num]; len(names) > 0 {
		return names[0]
	}
	return "prop " + num
}

// assignments splits the session's active time at each ref point.
func (s *session) assignments() []Assignment {
	active := Union(append(append([]Interval(nil), s.busy...), s.focus...))
	var cuts []time.Time
	for _, p := range s.points {
		cuts = append(cuts, p.at)
	}
	var out []Assignment
	for _, a := range active {
		for _, piece := range CutAt(a, cuts) {
			out = append(out, s.assignmentAt(piece))
		}
	}
	return out
}

func (s *session) assignmentAt(piece Interval) Assignment {
	if len(s.points) == 0 {
		return Assignment{Kind: "unassigned", Code: "Unassigned", Label: s.title, Interval: piece}
	}
	cur := s.points[0] // time before the first number goes to the first number
	for _, p := range s.points {
		if !p.at.After(piece.Start) {
			cur = p
		}
	}
	label := s.title
	if cur.kind == "admin" {
		label = cur.label
	}
	return Assignment{Kind: cur.kind, Code: cur.code, Label: label, Interval: piece}
}

// assignClaudeFocus credits a present Claude-window focus piece to the
// matching session, or to the most recently active one, else Unassigned.
func assignClaudeFocus(fc Focus, piece Interval, sessions []*session, o Options) []Assignment {
	title := claudewin.NormalizeTitle(fc.Title)
	var match *session
	var bestDist time.Duration = -1
	mid := piece.Start.Add(piece.End.Sub(piece.Start) / 2)
	for _, s := range sessions {
		if !s.titles[title] {
			continue
		}
		for _, e := range s.events {
			dist := e.TS.Sub(mid)
			if dist < 0 {
				dist = -dist
			}
			if bestDist < 0 || dist < bestDist {
				match, bestDist = s, dist
			}
		}
	}
	if match != nil {
		match.focus = append(match.focus, piece)
		return nil
	}
	var out []Assignment
	for start := piece.Start; start.Before(piece.End); start = start.Add(time.Minute) {
		end := start.Add(time.Minute)
		if end.After(piece.End) {
			end = piece.End
		}
		slice := Interval{start, end}
		if s := recentSession(sessions, start, o.Recent); s != nil {
			s.focus = append(s.focus, slice)
			continue
		}
		label := title
		if label == "" {
			label = fc.Process
		}
		out = append(out, Assignment{Kind: "unassigned", Code: "Unassigned", Label: label, Interval: slice})
	}
	return out
}

func recentSession(sessions []*session, at time.Time, window time.Duration) *session {
	var best *session
	var bestTS time.Time
	for _, s := range sessions {
		for _, e := range s.events {
			if e.TS.After(at) || at.Sub(e.TS) > window {
				continue
			}
			if best == nil || e.TS.After(bestTS) {
				best, bestTS = s, e.TS
			}
		}
	}
	return best
}

// windowAssignment credits a non-Claude focus piece by the number in its title.
func windowAssignment(fc Focus, piece Interval, f Folders) Assignment {
	if fc.Project != "" {
		switch f.Classify("mention", fc.Project) {
		case "project":
			return Assignment{Kind: "project", Code: fc.Project, Label: fc.Title, Interval: piece}
		case "proposal":
			return Assignment{Kind: "admin", Code: "Admin", Label: proposalLabel(fc.Project, f), Interval: piece}
		}
		return Assignment{Kind: "unassigned", Code: "Unassigned", Label: fc.Title, Interval: piece}
	}
	return Assignment{Kind: "other", Code: "Other: " + fc.Process, Label: fc.Title, Interval: piece}
}
```

Check against `TestAttribute_GenericTitleUsesRecentSession`: busy run is 0 to 1 min (prompt at 0, stop at 1). Focus 1 to 5 is sliced per minute: slices starting at 1, 2 and 3 have the minute-1 event at most 2 min earlier and go to the session; the slice starting at 4 is Unassigned. 26-104 = 0 to 4 = 4 min. Focus 20 to 25 is Unassigned.

- [ ] **Step 4: Implement the report**

`internal/timesheet/report.go`:
```go
package timesheet

import (
	"math"
	"sort"
	"strings"
	"time"
)

// Row is one billing code on one day.
type Row struct {
	Date     string   `json:"date"`
	Code     string   `json:"code"`
	Name     string   `json:"name"`
	RawHours float64  `json:"raw_hours"`
	Hours    float64  `json:"hours"`
	Notes    []string `json:"notes"`
}

// Day compares billed project hours with clock hours.
type Day struct {
	Date         string  `json:"date"`
	ProjectHours float64 `json:"project_hours"`
	ClockHours   float64 `json:"clock_hours"`
}

// Report is the timesheet for a date range.
type Report struct {
	From       string `json:"from"`
	To         string `json:"to"`
	Rows       []Row  `json:"rows"`
	Unassigned []Row  `json:"unassigned"`
	Other      []Row  `json:"other"`
	Days       []Day  `json:"days"`
}

// RoundHours: nearest 0.5 h; 10 min to under 0.5 h rounds up to 0.5; under 10 min is 0.
func RoundHours(raw float64) float64 {
	switch {
	case raw < 10.0/60:
		return 0
	case raw < 0.5:
		return 0.5
	}
	return math.Round(raw*2) / 2
}

func hours(d time.Duration) float64 { return math.Round(d.Hours()*100) / 100 }

type bucket struct {
	kind   string
	ivs    []Interval
	labels map[string]time.Duration
}

// BuildReport groups assignments by local day and code, unions each group,
// and rounds. Days run [from, to) in loc.
func BuildReport(asg []Assignment, clock []Interval, f Folders, loc *time.Location, from, to time.Time) Report {
	window := []Interval{{from, to}}
	buckets := map[[2]string]*bucket{}
	for _, a := range asg {
		for _, clipped := range intersect(a.Interval, window) {
			for date, part := range SplitDays(clipped, loc) {
				key := [2]string{date, a.Code}
				b := buckets[key]
				if b == nil {
					b = &bucket{kind: a.Kind, labels: map[string]time.Duration{}}
					buckets[key] = b
				}
				b.ivs = append(b.ivs, part)
				if a.Label != "" {
					b.labels[a.Label] += part.End.Sub(part.Start)
				}
			}
		}
	}

	r := Report{From: from.In(loc).Format("2006-01-02"), To: to.In(loc).Format("2006-01-02")}
	days := map[string]*Day{}
	dayOf := func(date string) *Day {
		if days[date] == nil {
			days[date] = &Day{Date: date}
		}
		return days[date]
	}
	for key, b := range buckets {
		raw := hours(Total(Union(b.ivs)))
		row := Row{Date: key[0], Code: key[1], RawHours: raw, Hours: RoundHours(raw), Notes: topLabels(b.labels, 5)}
		switch b.kind {
		case "project":
			row.Name = strings.Join(f.Projects[key[1]], " / ")
			if row.Name == "" {
				row.Name = key[1]
			}
			r.Rows = append(r.Rows, row)
			dayOf(key[0]).ProjectHours += row.Hours
		case "admin":
			row.Name = "Admin"
			r.Rows = append(r.Rows, row)
			dayOf(key[0]).ProjectHours += row.Hours
		case "unassigned":
			r.Unassigned = append(r.Unassigned, row)
		default:
			r.Other = append(r.Other, row)
		}
	}
	clockByDay := map[string][]Interval{}
	for _, c := range intersectAll(clock, window) {
		for date, part := range SplitDays(c, loc) {
			clockByDay[date] = append(clockByDay[date], part)
		}
	}
	for date, ivs := range clockByDay {
		dayOf(date).ClockHours = hours(Total(Union(ivs)))
	}
	for _, d := range days {
		r.Days = append(r.Days, *d)
	}
	sortRows := func(rows []Row) {
		sort.Slice(rows, func(a, b int) bool {
			if rows[a].Date != rows[b].Date {
				return rows[a].Date < rows[b].Date
			}
			return rows[a].Code < rows[b].Code
		})
	}
	sortRows(r.Rows)
	sortRows(r.Unassigned)
	sortRows(r.Other)
	sort.Slice(r.Days, func(a, b int) bool { return r.Days[a].Date < r.Days[b].Date })
	return r
}

func intersect(i Interval, window []Interval) []Interval {
	return Subtract([]Interval{i}, Subtract([]Interval{{i.Start, i.End}}, window))
}

func intersectAll(in, window []Interval) []Interval {
	var out []Interval
	for _, i := range in {
		out = append(out, intersect(i, window)...)
	}
	return out
}

func topLabels(m map[string]time.Duration, n int) []string {
	type kv struct {
		k string
		v time.Duration
	}
	var s []kv
	for k, v := range m {
		s = append(s, kv{k, v})
	}
	sort.Slice(s, func(a, b int) bool {
		if s[a].v != s[b].v {
			return s[a].v > s[b].v
		}
		return s[a].k < s[b].k
	})
	var out []string
	for i := 0; i < len(s) && i < n; i++ {
		out = append(out, s[i].k)
	}
	return out
}
```

- [ ] **Step 5: Run tests**

Run: `go test ./internal/timesheet/`
Expected: PASS. If a test fails, fix the implementation, not the expectation.

- [ ] **Step 6: Commit**

```powershell
git add internal/timesheet
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Attribute focus and Claude activity to billing codes and build the timesheet report" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 10: Timesheet CLI and MCP tool

**Files:**
- Create: `internal/timesheet/generate.go`, `internal/timesheet/format.go`, `internal/timesheet/format_test.go`
- Modify: `exporter.go`, `internal/mcp/server.go`, `internal/mcp/server_test.go`

**Interfaces:**
- Consumes: `Load`, `ScanFolders`, `Attribute`, `BuildReport`
- Produces: `timesheet.GenerateOptions{ProjectRoot, ProposalRoot string; Idle time.Duration; Loc *time.Location}`, `Generate(dbpath string, from, to time.Time, g GenerateOptions) (Report, error)`, `FormatTable(r Report) string`, `WriteCSV(w io.Writer, r Report) error`

- [ ] **Step 1: Write failing tests**

`internal/timesheet/format_test.go`:
```go
package timesheet

import (
	"bytes"
	"strings"
	"testing"
)

func sampleReport() Report {
	return Report{
		From: "2026-09-30", To: "2026-10-01",
		Rows:       []Row{{Date: "2026-09-30", Code: "26-104", Name: "26-104 CBC", RawHours: 3.3, Hours: 3.5, Notes: []string{"Arc flash", "calc.xlsx"}}},
		Unassigned: []Row{{Date: "2026-09-30", Code: "Unassigned", RawHours: 1, Hours: 1, Notes: []string{"Health dashboard"}}},
		Days:       []Day{{Date: "2026-09-30", ProjectHours: 3.5, ClockHours: 4.2}},
	}
}

func TestFormatTable(t *testing.T) {
	out := FormatTable(sampleReport())
	for _, want := range []string{"26-104", "3.5", "Arc flash; calc.xlsx", "Unassigned", "Health dashboard", "clock 4.20"} {
		if !strings.Contains(out, want) {
			t.Errorf("table missing %q:\n%s", want, out)
		}
	}
}

func TestWriteCSV(t *testing.T) {
	var b bytes.Buffer
	if err := WriteCSV(&b, sampleReport()); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(b.String()), "\n")
	if lines[0] != "section,date,code,name,raw_hours,hours,notes" || len(lines) != 3 {
		t.Errorf("csv:\n%s", b.String())
	}
}
```

Append to `internal/mcp/server_test.go`:
```go
func TestToolsIncludeGetTimesheet(t *testing.T) {
	for _, tl := range tools {
		if tl.Name == "get_timesheet" {
			return
		}
	}
	t.Error("get_timesheet tool not registered")
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/timesheet/ ./internal/mcp/`
Expected: FAIL (`undefined: FormatTable`; missing tool).

- [ ] **Step 3: Implement**

`internal/timesheet/generate.go`:
```go
package timesheet

import "time"

// GenerateOptions configure a timesheet run.
type GenerateOptions struct {
	ProjectRoot  string
	ProposalRoot string
	Idle         time.Duration
	Loc          *time.Location
}

// DefaultGenerateOptions uses the S: drive roots, 10-minute idle and local time.
func DefaultGenerateOptions() GenerateOptions {
	return GenerateOptions{ProjectRoot: DefaultProjectRoot, ProposalRoot: DefaultProposalRoot,
		Idle: 10 * time.Minute, Loc: time.Local}
}

// Generate builds the report for local dates [from, to).
func Generate(dbpath string, from, to time.Time, g GenerateOptions) (Report, error) {
	d, err := Load(dbpath, from, to)
	if err != nil {
		return Report{}, err
	}
	f := ScanFolders(g.ProjectRoot, g.ProposalRoot)
	o := DefaultOptions()
	if g.Idle > 0 {
		o.Idle = g.Idle
	}
	asg, clock := Attribute(d, f, o)
	return BuildReport(asg, clock, f, g.Loc, from, to), nil
}
```

`internal/timesheet/format.go`:
```go
package timesheet

import (
	"encoding/csv"
	"fmt"
	"io"
	"strings"
)

// FormatTable renders the report as plain text for the console.
func FormatTable(r Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Timesheet %s to %s\n\n", r.From, r.To)
	fmt.Fprintf(&b, "%-10s  %-10s  %6s  %5s  %-40s  %s\n", "date", "code", "raw", "hours", "name", "notes")
	for _, row := range r.Rows {
		fmt.Fprintf(&b, "%-10s  %-10s  %6.2f  %5.1f  %-40.40s  %s\n", row.Date, row.Code, row.RawHours, row.Hours, row.Name, strings.Join(row.Notes, "; "))
	}
	b.WriteString("\nPer day:\n")
	for _, d := range r.Days {
		fmt.Fprintf(&b, "  %s  projects %5.1f h  clock %.2f h\n", d.Date, d.ProjectHours, d.ClockHours)
	}
	if len(r.Unassigned) > 0 {
		b.WriteString("\nUnassigned (assign manually):\n")
		for _, row := range r.Unassigned {
			fmt.Fprintf(&b, "  %s  %6.2f h  %s\n", row.Date, row.RawHours, strings.Join(row.Notes, "; "))
		}
	}
	if len(r.Other) > 0 {
		b.WriteString("\nOther apps (not billed):\n")
		for _, row := range r.Other {
			fmt.Fprintf(&b, "  %s  %-30s  %6.2f h\n", row.Date, row.Code, row.RawHours)
		}
	}
	return b.String()
}

// WriteCSV writes billed rows, then unassigned rows, with a section column.
func WriteCSV(w io.Writer, r Report) error {
	cw := csv.NewWriter(w)
	cw.Write([]string{"section", "date", "code", "name", "raw_hours", "hours", "notes"})
	write := func(section string, rows []Row) {
		for _, row := range rows {
			cw.Write([]string{section, row.Date, row.Code, row.Name,
				fmt.Sprintf("%.2f", row.RawHours), fmt.Sprintf("%.1f", row.Hours), strings.Join(row.Notes, "; ")})
		}
	}
	write("billed", r.Rows)
	write("unassigned", r.Unassigned)
	cw.Flush()
	return cw.Error()
}
```

`exporter.go` changes:
1. Flag vars: `timesheetMode bool; tsFrom, tsTo, tsCSV string; tsIdleMin int`.
2. In `init()`:
```go
	flag.BoolVar(&timesheetMode, "timesheet", false, "Print hours per project per day")
	flag.StringVar(&tsFrom, "from", "", "Timesheet start date YYYY-MM-DD (default: Monday this week)")
	flag.StringVar(&tsTo, "to", "", "Timesheet end date YYYY-MM-DD, inclusive (default: today)")
	flag.StringVar(&tsCSV, "csv", "", "Also write the timesheet to this CSV file")
	flag.IntVar(&tsIdleMin, "idle", 10, "Timesheet: inactivity of this many minutes or more is away time")
```
3. Import `"github.com/vinistoisr/timewarp/internal/timesheet"`.
4. In `main()` after the backfill block:
```go
	if timesheetMode {
		if err := runTimesheet(); err != nil {
			fmt.Fprintf(os.Stderr, "Timesheet error: %v\n", err)
			os.Exit(1)
		}
		return
	}
```
5. Add:
```go
func runTimesheet() error {
	path := dbpath
	if path == "" {
		path = db.ExeDir()
	}
	now := time.Now()
	from := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	from = from.AddDate(0, 0, -((int(from.Weekday()) + 6) % 7))
	to := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)
	var err error
	if tsFrom != "" {
		if from, err = time.ParseInLocation("2006-01-02", tsFrom, time.Local); err != nil {
			return fmt.Errorf("invalid -from: %w", err)
		}
	}
	if tsTo != "" {
		if to, err = time.ParseInLocation("2006-01-02", tsTo, time.Local); err != nil {
			return fmt.Errorf("invalid -to: %w", err)
		}
	}
	g := timesheet.DefaultGenerateOptions()
	g.Idle = time.Duration(tsIdleMin) * time.Minute
	r, err := timesheet.Generate(path, from, to.AddDate(0, 0, 1), g)
	if err != nil {
		return err
	}
	fmt.Print(timesheet.FormatTable(r))
	if tsCSV != "" {
		f, err := os.Create(tsCSV)
		if err != nil {
			return err
		}
		defer f.Close()
		return timesheet.WriteCSV(f, r)
	}
	return nil
}
```

`internal/mcp/server.go` changes:
1. Import `"github.com/vinistoisr/timewarp/internal/timesheet"`.
2. Append to `tools`:
```go
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
```
3. In `handleToolCall` switch: `case "get_timesheet": result, err = callGetTimesheet(dbpath, params.Arguments)`.
4. Add:
```go
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
```

- [ ] **Step 4: Run tests and build**

Run: `go test ./... && go build -o $env:TEMP\tw-check.exe .`
Expected: PASS, clean build.

- [ ] **Step 5: Commit**

```powershell
git add internal/timesheet internal/mcp exporter.go
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Add -timesheet command and get_timesheet MCP tool" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 11: Non-admin install, hook registration, README

**Files:**
- Create: `internal/install/task.go`, `internal/install/task_test.go`
- Create: `internal/install/hooks.go`, `internal/install/hooks_test.go`
- Create: `internal/install/user_windows.go`
- Modify: `exporter.go` (install/uninstall, remove elevation code and task XML), `internal/tray/onboarding.go`, `README.md`

**Interfaces:**
- Produces: `install.CurrentUser() (string, error)`, `install.TaskName(user string) string`, `install.TaskXML(user, exePath, dbpath string) string`, `install.InstallTask(user, exePath, dbpath string) error`, `install.UninstallTask(user string) error`, `install.HasTask(user string) bool`, `install.LegacyTaskPresent() bool`, `install.MergeHooks(settings []byte, exePath, dbpath string) ([]byte, error)`, `install.RemoveHooks(settings []byte) ([]byte, error)`, `install.InstallHooks(exePath, dbpath string) error`, `install.UninstallHooks() error`

- [ ] **Step 1: Write failing tests**

`internal/install/task_test.go`:
```go
package install

import (
	"strings"
	"testing"
)

func TestTaskName(t *testing.T) {
	if got := TaskName(`FGROUP\vincent`); got != "Timewarp-vincent" {
		t.Errorf("TaskName = %q", got)
	}
	if got := TaskName("solo"); got != "Timewarp-solo" {
		t.Errorf("TaskName = %q", got)
	}
}

func TestTaskXML(t *testing.T) {
	x := TaskXML(`FGROUP\vincent`, `C:\Users\vincent\timewarp\timewarp.exe`, `C:\Users\vincent\OneDrive - Flow Engineering\Timewarp Db`)
	for _, want := range []string{
		`<UserId>FGROUP\vincent</UserId>`,
		`<Command>C:\Users\vincent\timewarp\timewarp.exe</Command>`,
		`<Arguments>-dbpath "C:\Users\vincent\OneDrive - Flow Engineering\Timewarp Db"</Arguments>`,
		`<LogonType>InteractiveToken</LogonType>`,
		`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("task XML missing %s", want)
		}
	}
	if strings.Contains(x, "-silent") {
		t.Error("task must run with the tray icon")
	}
	if !strings.Contains(TaskXML("u", `C:\a&b\t.exe`, ""), `C:\a&amp;b\t.exe`) {
		t.Error("XML special characters must be escaped")
	}
}
```

`internal/install/hooks_test.go`:
```go
package install

import (
	"encoding/json"
	"strings"
	"testing"
)

const existingSettings = `{
  "model": "opus",
  "hooks": {
    "PostToolUse": [
      {"matcher": "Skill", "hooks": [{"type": "command", "command": "python.exe", "args": ["skill_usage_logger.py"], "async": true}]}
    ],
    "SessionEnd": [
      {"matcher": ".*", "hooks": [{"type": "command", "command": "python.exe", "args": ["token_usage.py", "--hook"]}]}
    ]
  }
}`

func hookCount(t *testing.T, data []byte, event string) (timewarp, other int) {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	hooks, _ := s["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			if isTimewarpHook(h) {
				timewarp++
			} else {
				other++
			}
		}
	}
	return
}

func TestMergeHooks_PreservesOthers(t *testing.T) {
	out, err := MergeHooks([]byte(existingSettings), `C:\tw\timewarp.exe`, `C:\db`)
	if err != nil {
		t.Fatal(err)
	}
	out, err = MergeHooks(out, `C:\tw\timewarp.exe`, `C:\db`) // idempotent
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"} {
		if tw, _ := hookCount(t, out, ev); tw != 1 {
			t.Errorf("%s: %d timewarp hooks, want 1", ev, tw)
		}
	}
	if _, other := hookCount(t, out, "PostToolUse"); other != 1 {
		t.Error("existing PostToolUse hook lost")
	}
	if _, other := hookCount(t, out, "SessionEnd"); other != 1 {
		t.Error("existing SessionEnd hook lost")
	}
	if !strings.Contains(string(out), `"model": "opus"`) {
		t.Error("other settings lost")
	}
	if !strings.Contains(string(out), `"-claudehook"`) || !strings.Contains(string(out), `"async": true`) {
		t.Error("hook entry missing -claudehook or async")
	}
}

func TestMergeHooks_EmptySettings(t *testing.T) {
	for _, in := range [][]byte{nil, []byte(""), []byte("{}")} {
		out, err := MergeHooks(in, `C:\tw\timewarp.exe`, `C:\db`)
		if err != nil {
			t.Fatal(err)
		}
		if tw, _ := hookCount(t, out, "Stop"); tw != 1 {
			t.Errorf("input %q: Stop hook not added", in)
		}
	}
}

func TestRemoveHooks(t *testing.T) {
	merged, _ := MergeHooks([]byte(existingSettings), `C:\tw\timewarp.exe`, `C:\db`)
	out, err := RemoveHooks(merged)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"} {
		if tw, _ := hookCount(t, out, ev); tw != 0 {
			t.Errorf("%s: timewarp hook not removed", ev)
		}
	}
	if _, other := hookCount(t, out, "PostToolUse"); other != 1 {
		t.Error("remove deleted another hook")
	}
	if strings.Contains(string(out), "UserPromptSubmit") {
		t.Error("empty event list should be dropped")
	}
}

func TestMergeHooks_InvalidJSON(t *testing.T) {
	if _, err := MergeHooks([]byte("{not json"), "x", "y"); err == nil {
		t.Error("invalid settings.json must be an error, not overwritten")
	}
}
```

- [ ] **Step 2: Run, expect failure**

Run: `go test ./internal/install/`
Expected: FAIL (`undefined: TaskName`).

- [ ] **Step 3: Implement**

`internal/install/user_windows.go`:
```go
package install

import "golang.org/x/sys/windows"

// CurrentUser returns DOMAIN\user for the account that owns this process token.
func CurrentUser() (string, error) {
	tu, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	account, domain, _, err := tu.User.Sid.LookupAccount("")
	if err != nil {
		return "", err
	}
	return domain + `\` + account, nil
}
```

`internal/install/task.go`:
```go
// Package install registers the startup task and the Claude Code hooks
// for the current user, without elevation.
package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Timewarp activity tracker. Starts at logon and restarts within 5 minutes if it stops.</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>%[1]s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
  </Settings>
  <Triggers>
    <LogonTrigger>
      <Repetition>
        <Interval>PT5M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <Enabled>true</Enabled>
      <UserId>%[1]s</UserId>
      <Delay>PT30S</Delay>
    </LogonTrigger>
    <CalendarTrigger>
      <Repetition>
        <Interval>PT5M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <StartBoundary>2020-01-01T00:05:00</StartBoundary>
      <Enabled>true</Enabled>
      <ScheduleByDay>
        <DaysInterval>1</DaysInterval>
      </ScheduleByDay>
    </CalendarTrigger>
  </Triggers>
  <Actions Context="Author">
    <Exec>
      <Command>%[2]s</Command>%[3]s
    </Exec>
  </Actions>
</Task>
`

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// TaskName is the per-user scheduled task name, e.g. Timewarp-vincent.
func TaskName(user string) string {
	if i := strings.LastIndex(user, `\`); i >= 0 {
		user = user[i+1:]
	}
	return "Timewarp-" + user
}

// TaskXML builds the task definition for user.
func TaskXML(user, exePath, dbpath string) string {
	args := ""
	if dbpath != "" {
		args = "\n      <Arguments>-dbpath \"" + xmlEscape(dbpath) + "\"</Arguments>"
	}
	return fmt.Sprintf(taskXML, xmlEscape(user), xmlEscape(exePath), args)
}

// utf16leBytes encodes s as UTF-16LE with a BOM; schtasks rejects UTF-8 XML.
func utf16leBytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2+len(u)*2)
	b = append(b, 0xFF, 0xFE)
	for _, r := range u {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}

func schtasks(args ...string) (string, error) {
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// InstallTask registers (or replaces) the user's startup task. No elevation.
func InstallTask(user, exePath, dbpath string) error {
	tmp := filepath.Join(os.TempDir(), "timewarp-task.xml")
	if err := os.WriteFile(tmp, utf16leBytes(TaskXML(user, exePath, dbpath)), 0o600); err != nil {
		return fmt.Errorf("could not write task definition: %w", err)
	}
	defer os.Remove(tmp)
	if out, err := schtasks("/create", "/tn", TaskName(user), "/xml", tmp, "/f"); err != nil {
		return fmt.Errorf("schtasks create failed: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// UninstallTask removes the user's startup task.
func UninstallTask(user string) error {
	if out, err := schtasks("/delete", "/tn", TaskName(user), "/f"); err != nil {
		return fmt.Errorf("schtasks delete failed: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// HasTask reports whether the user's startup task exists.
func HasTask(user string) bool {
	_, err := schtasks("/query", "/tn", TaskName(user))
	return err == nil
}

// LegacyTaskPresent reports whether the old shared "\Timewarp" task exists,
// including when it belongs to another account and cannot be read.
func LegacyTaskPresent() bool {
	out, err := schtasks("/query", "/tn", "Timewarp")
	return err == nil || strings.Contains(strings.ToLower(out), "access is denied")
}
```

`internal/install/hooks.go`:
```go
package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var hookEvents = []struct {
	name    string
	matcher bool
}{
	{"UserPromptSubmit", false},
	{"PostToolUse", true},
	{"Stop", false},
	{"SessionEnd", true},
}

// SettingsPath is the user's Claude Code settings file.
func SettingsPath() string {
	return filepath.Join(os.Getenv("USERPROFILE"), ".claude", "settings.json")
}

func isTimewarpHook(h any) bool {
	m, ok := h.(map[string]any)
	if !ok {
		return false
	}
	args, _ := m["args"].([]any)
	for _, a := range args {
		if a == "-claudehook" {
			return true
		}
	}
	return false
}

func parseSettings(data []byte) (map[string]any, error) {
	s := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("settings.json is not valid JSON: %w", err)
	}
	return s, nil
}

// strip removes timewarp hook entries, dropping groups and events left empty.
func strip(s map[string]any) {
	hooks, _ := s["hooks"].(map[string]any)
	for event, v := range hooks {
		groups, _ := v.([]any)
		var keptGroups []any
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				keptGroups = append(keptGroups, g)
				continue
			}
			list, _ := gm["hooks"].([]any)
			var kept []any
			for _, h := range list {
				if !isTimewarpHook(h) {
					kept = append(kept, h)
				}
			}
			if len(kept) > 0 {
				gm["hooks"] = kept
				keptGroups = append(keptGroups, gm)
			}
		}
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	if hooks != nil && len(hooks) == 0 {
		delete(s, "hooks")
	}
}

func marshal(s map[string]any) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// MergeHooks returns settings with exactly one timewarp hook per event,
// leaving every other setting and hook as it was.
func MergeHooks(data []byte, exePath, dbpath string) ([]byte, error) {
	s, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	strip(s)
	hooks, _ := s["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		s["hooks"] = hooks
	}
	for _, ev := range hookEvents {
		entry := map[string]any{
			"type":    "command",
			"command": exePath,
			"args":    []any{"-claudehook", "-dbpath", dbpath},
			"timeout": 30,
			"async":   true,
		}
		group := map[string]any{"hooks": []any{entry}}
		if ev.matcher {
			group["matcher"] = ".*"
		}
		existing, _ := hooks[ev.name].([]any)
		hooks[ev.name] = append(existing, group)
	}
	return marshal(s)
}

// RemoveHooks returns settings with every timewarp hook removed.
func RemoveHooks(data []byte) ([]byte, error) {
	s, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	strip(s)
	return marshal(s)
}

func rewriteSettings(change func([]byte) ([]byte, error)) error {
	p := SettingsPath()
	old, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	updated, err := change(old)
	if err != nil {
		return err
	}
	if old != nil {
		if err := os.WriteFile(p+".timewarp-bak", old, 0o644); err != nil {
			return fmt.Errorf("could not back up settings.json: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, updated, 0o644)
}

// InstallHooks adds the timewarp hooks to the user's Claude Code settings.
func InstallHooks(exePath, dbpath string) error {
	return rewriteSettings(func(b []byte) ([]byte, error) { return MergeHooks(b, exePath, dbpath) })
}

// UninstallHooks removes the timewarp hooks from the user's Claude Code settings.
func UninstallHooks() error {
	return rewriteSettings(RemoveHooks)
}
```

`json.MarshalIndent` sorts keys, so settings.json keys are reordered; content is unchanged and a backup is written first.

`exporter.go` changes:
1. Delete `elevateAndRun`, `isElevated`, the `taskXML` const, `xmlEscape`, `utf16leBytes`, and the bodies of `doInstall`/`doUninstall`. Remove the now-unused imports (`os/exec`, `unicode/utf16`, `unsafe`, and `windows` if nothing else uses it; `acquireSingleInstanceLock` still uses `windows`).
2. Import `"github.com/vinistoisr/timewarp/internal/install"`.
3. Replace `doInstall` and `doUninstall` with:
```go
func doInstall() error {
	exePath, err := os.Executable()
	if err != nil {
		return fmt.Errorf("could not determine executable path: %w", err)
	}
	user, err := install.CurrentUser()
	if err != nil {
		return fmt.Errorf("could not determine current user: %w", err)
	}
	path := dbpath
	if path == "" {
		path = db.ExeDir()
	}
	if err := install.InstallTask(user, exePath, path); err != nil {
		return err
	}
	fmt.Printf("Startup task %s installed for %s.\n", install.TaskName(user), user)
	if err := install.InstallHooks(exePath, path); err != nil {
		return fmt.Errorf("Claude Code hooks: %w", err)
	}
	fmt.Println("Claude Code hooks added to", install.SettingsPath())
	if install.LegacyTaskPresent() {
		fmt.Println(`An old "Timewarp" task from a previous install still exists. To remove it, run in an administrator prompt: schtasks /delete /tn Timewarp /f`)
	}
	return nil
}

func doUninstall() error {
	user, err := install.CurrentUser()
	if err != nil {
		return err
	}
	if err := install.UninstallTask(user); err != nil {
		fmt.Fprintln(os.Stderr, err)
	}
	if err := install.UninstallHooks(); err != nil {
		return fmt.Errorf("Claude Code hooks: %w", err)
	}
	fmt.Println("Timewarp startup task and Claude Code hooks removed.")
	return nil
}
```
4. In `main()`, replace the onboarding block `if !silentMode && !tray.HasScheduledTask() {` with:
```go
	if user, err := install.CurrentUser(); err == nil && !silentMode && !install.HasTask(user) {
```
(the body calling `tray.OfferScheduledTask(exePath, p)` stays).

`internal/tray/onboarding.go` changes:
1. Delete `HasScheduledTask`.
2. Replace `OfferScheduledTask` and `elevatedRun` with:
```go
// OfferScheduledTask asks the user if they want Timewarp to start
// automatically and, if so, runs "timewarp.exe -install" as the current user.
func OfferScheduledTask(exePath, dbpath string) bool {
	ret := messageBox("Start Automatically?",
		"Would you like Timewarp to start automatically when you log in?\r\n\r\n"+
			"This creates a scheduled task for your account and records Claude Code sessions. "+
			"No administrator rights are needed.\r\n\r\n"+
			"You can remove it later with:\r\n"+
			"  timewarp.exe -uninstall",
		mbYesNo|mbIconQuestion)
	if ret != idYes {
		return false
	}
	out, err := hiddenCmd(exePath, "-install", "-dbpath", dbpath).CombinedOutput()
	if err != nil {
		messageBox("Timewarp", fmt.Sprintf("Failed to set up automatic start:\n%v\n%s", err, out), mbOK|mbIconInfo)
		return false
	}
	return true
}
```
Remove now-unused imports from onboarding.go if the compiler reports any.

`README.md`: replace the install section's UAC/Startup-folder text with a section:

```markdown
## Install on a PC

1. Build: `go build -o timewarp.exe .` (Go 1.24 or newer).
2. Install for your account (no administrator rights needed):
   `timewarp.exe -install -dbpath "C:\Users\<you>\OneDrive - <org>\Timewarp Db"`
   This creates the scheduled task `Timewarp-<username>`, which starts Timewarp at logon and restarts it within 5 minutes if it stops, and adds the Claude Code hooks to `%USERPROFILE%\.claude\settings.json` (a backup is saved as `settings.json.timewarp-bak`).
3. Import existing Claude Code sessions: `timewarp.exe -claudebackfill -dbpath "<same folder>"`
4. Restart open Claude Code sessions so they load the hooks.

To remove: `timewarp.exe -uninstall`. If an older install left a task named `Timewarp`, delete it from an administrator prompt with `schtasks /delete /tn Timewarp /f`.

## Timesheets

`timewarp.exe -timesheet -from 2026-09-01 -to 2026-09-30 -dbpath "<folder>" [-csv sept.csv]`

Hours per billing code per day from every PC's database in the folder. Claude Code sessions are credited to the project they work on (from `S:\PROJECT\<year>\<number> ...` paths and project numbers in prompts). A project counts whenever you or Claude were working on it; work on the same project at the same time counts once, work on different projects at the same time counts for each. Proposal work is billed to Admin. Idle time of 10 minutes or more is excluded (`-idle` to change). Rows are rounded to 0.5 h. The same report is available to Claude through the `get_timesheet` MCP tool.
```

- [ ] **Step 4: Run tests and build**

Run: `go vet ./... && go test ./... && go build -o $env:TEMP\tw-check.exe .`
Expected: PASS, clean vet and build.

- [ ] **Step 5: Commit**

```powershell
git add internal/install internal/tray exporter.go README.md
git -c user.name="Vincent Royer" -c user.email="vincent@flowengineering.ca" commit -m "Install per user without elevation and register Claude Code hooks" -m "Co-Authored-By: Claude Opus 5.5 <noreply@anthropic.com>`nClaude-Session: https://claude.ai/code/session_01LQdpA3A7Fq6HCfeChWpEDz"
```

---

### Task 12: Roll out on DESK63 and produce the September timesheet

**Files:** none in repo (the local interim `timewarp-vincent-task.xml` is deleted).

- [ ] **Step 1: Build next to the running exe**

```powershell
Set-Location C:\Users\vincent\timewarp; go build -o timewarp-new.exe .
```
Expected: `timewarp-new.exe` created.

- [ ] **Step 2: Swap the exe** (running tracker and `-mcp` processes lock `timewarp.exe`; Windows allows renaming a running exe)

```powershell
Rename-Item timewarp.exe timewarp-prev.exe -Force; Rename-Item timewarp-new.exe timewarp.exe
```
Expected: both renames succeed. If `timewarp-prev.exe` exists from an earlier run, delete it first; if it is locked, rename it to `timewarp-prev2.exe`.

- [ ] **Step 3: Install and restart the tracker**

```powershell
.\timewarp.exe -install -dbpath "C:\Users\vincent\OneDrive - Flow Engineering\Timewarp Db"
Get-CimInstance Win32_Process -Filter "Name='timewarp.exe'" | Where-Object { $_.CommandLine -notmatch '-mcp' } | ForEach-Object { Stop-Process -Id $_.ProcessId }
schtasks /run /tn Timewarp-vincent
```
Expected: install prints the task name, the hooks path and the legacy-task note. After about 20 s, one non-mcp `timewarp.exe` is running with `-dbpath` and no `-silent`, and the user sees the tray icon. Ask the user to confirm the icon.

- [ ] **Step 4: Verify hooks fire**

Confirm `~/.claude/settings.json` has the four `-claudehook` entries and all previous hooks. Hooks load at session start, so ask the user to start one new Claude Code session and send one prompt that names a project folder. Then query `claude_events` for that session (read-only copy of the DB) and confirm a `prompt` row with the right `ref_number`.

- [ ] **Step 5: Backfill**

```powershell
.\timewarp.exe -claudebackfill -dbpath "C:\Users\vincent\OneDrive - Flow Engineering\Timewarp Db"
```
Expected: `Backfill: N transcripts read, M events added.` with N near the transcript count (265 on 2026-09-30). Check `timewarp-hook.log` does not exist or has no new lines.

- [ ] **Step 6: September timesheet**

```powershell
.\timewarp.exe -timesheet -from 2026-09-01 -to 2026-09-30 -dbpath "C:\Users\vincent\OneDrive - Flow Engineering\Timewarp Db" -csv "$scratch\timesheet-2026-09.csv"
```
Show the user the table. Point out the known gaps plainly: DESK63 had no window tracking between 07-23 and 09-30 (so September on the desktop comes from Claude busy time only), and the laptop has not been backfilled yet. Ask the user to review.

- [ ] **Step 7: Clean up and hand over**

Delete `C:\Users\vincent\timewarp\timewarp-vincent-task.xml`. Update memory file `timewarp-office-pc-flowadmin-task.md` with the new install state. Ask the user for approval to push to `origin main`; push only on a yes. Give the laptop steps (README "Install on a PC", run as the laptop user).
