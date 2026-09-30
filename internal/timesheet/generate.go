package timesheet

import "time"

// GenerateOptions configure a timesheet run.
type GenerateOptions struct {
	ProjectRoot  string
	ProposalRoot string
	Idle         time.Duration
	Loc          *time.Location
}

// DefaultGenerateOptions uses the S: drive roots, 10-minute idle and local time.
func DefaultGenerateOptions() GenerateOptions {
	return GenerateOptions{ProjectRoot: DefaultProjectRoot, ProposalRoot: DefaultProposalRoot,
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
	return BuildReport(asg, clock, f, g.Loc, from, to), nil
}
