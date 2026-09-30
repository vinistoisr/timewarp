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
