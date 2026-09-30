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
