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
