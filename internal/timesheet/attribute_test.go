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
	// Two sessions working continuously (events every 5 minutes) for one hour.
	var d Data
	for m := 0; m <= 60; m += 5 {
		kindA, numA, kindB, numB := "none", "", "none", ""
		if m == 0 {
			kindA, numA, kindB, numB = "project", "26-104", "project", "26-087"
		}
		evt := "tool"
		if m == 60 {
			evt = "stop"
		}
		d.Events = append(d.Events, ev(m, "a", "A", evt, kindA, numA), ev(m, "b", "B", evt, kindB, numB))
	}
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

func TestAttribute_GenericFocusScalesWithEvents(t *testing.T) {
	// A month of activity: 20 sessions x 10,000 events, plus 10,000 minutes of
	// "Claude" desktop focus that must be matched to a recent session.
	var d Data
	for s := 0; s < 20; s++ {
		id := string(rune('a' + s))
		for i := 0; i < 10000; i++ {
			d.Events = append(d.Events, ev(i*4+s%4, id, "T"+id, "tool", "none", ""))
		}
	}
	d.Focus = []Focus{focus(0, 10000, "claude.exe", "Claude", "")}
	start := time.Now()
	Attribute(d, testFolders, DefaultOptions())
	if el := time.Since(start); el > 3*time.Second {
		t.Errorf("Attribute took %v for 200k events, want under 3s", el)
	}
}

func mustAttr(t *testing.T, d Data) []Assignment {
	t.Helper()
	asg, _ := Attribute(d, testFolders, DefaultOptions())
	return asg
}
