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
	Warnings   []string `json:"warnings,omitempty"`
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
