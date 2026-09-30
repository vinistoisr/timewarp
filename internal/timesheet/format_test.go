package timesheet

import (
	"bytes"
	"strings"
	"testing"
	"time"
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

func TestGenerate_WarnsWhenRootsUnreadable(t *testing.T) {
	dir := t.TempDir()
	g := DefaultGenerateOptions()
	g.ProjectRoot, g.ProposalRoot = `Z:\no\projects`, `Z:\no\proposals`
	r, err := Generate(dir, t0, t0.Add(24*time.Hour), g)
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Warnings) != 2 {
		t.Fatalf("warnings = %v, want one per unreadable root", r.Warnings)
	}
	if out := FormatTable(r); !strings.Contains(out, `Z:\no\projects`) {
		t.Errorf("table does not show the warning:\n%s", out)
	}
}

func TestDefaultGenerateOptions_UsesConfiguredRoots(t *testing.T) {
	oldP, oldQ := ProjectRoot, ProposalRoot
	defer func() { ProjectRoot, ProposalRoot = oldP, oldQ }()
	ProjectRoot, ProposalRoot = `P:\x`, `Q:\y`
	if g := DefaultGenerateOptions(); g.ProjectRoot != `P:\x` || g.ProposalRoot != `Q:\y` {
		t.Errorf("roots = %q %q", g.ProjectRoot, g.ProposalRoot)
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
