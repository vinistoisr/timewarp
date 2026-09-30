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
