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
