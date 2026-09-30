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
