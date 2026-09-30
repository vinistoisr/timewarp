package timesheet

import (
	"fmt"
	"os"
	"time"
)

// Folder roots used for name lookup and bare-number resolution. The CLI sets
// these from -projectroot and -proposalroot before any report runs.
var (
	ProjectRoot  = DefaultProjectRoot
	ProposalRoot = DefaultProposalRoot
)

// GenerateOptions configure a timesheet run.
type GenerateOptions struct {
	ProjectRoot  string
	ProposalRoot string
	Idle         time.Duration
	Loc          *time.Location
}

// DefaultGenerateOptions uses the configured roots, 10-minute idle and local time.
func DefaultGenerateOptions() GenerateOptions {
	return GenerateOptions{ProjectRoot: ProjectRoot, ProposalRoot: ProposalRoot,
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
	r := BuildReport(asg, clock, f, g.Loc, from, to)
	for _, root := range []string{g.ProjectRoot, g.ProposalRoot} {
		if _, err := os.ReadDir(root); err != nil {
			r.Warnings = append(r.Warnings, fmt.Sprintf(
				"Folder %s could not be read: project names are missing and bare numbers go to Unassigned.", root))
		}
	}
	return r, nil
}
