package timesheet

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/vinistoisr/timewarp/internal/claude"
)

// Default folder roots on the Flow S: drive.
const (
	DefaultProjectRoot  = `S:\PROJECT`
	DefaultProposalRoot = `S:\Proposals`
)

var yearDirRe = regexp.MustCompile(`^20\d\d$`)

// Folders maps numbers to folder names for projects and proposals.
type Folders struct {
	Projects  map[string][]string
	Proposals map[string][]string
}

// ScanFolders indexes <projectRoot>\<year>\<NN-NNN ...> folders and any folder
// up to five levels under proposalRoot whose name contains a number.
// Missing roots give empty maps.
func ScanFolders(projectRoot, proposalRoot string) Folders {
	f := Folders{Projects: map[string][]string{}, Proposals: map[string][]string{}}
	years, _ := os.ReadDir(projectRoot)
	for _, y := range years {
		if !y.IsDir() || !yearDirRe.MatchString(y.Name()) {
			continue
		}
		entries, _ := os.ReadDir(filepath.Join(projectRoot, y.Name()))
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			if nums := claude.Numbers(e.Name()); len(nums) > 0 && strings.HasPrefix(e.Name(), nums[0]) {
				f.Projects[nums[0]] = append(f.Projects[nums[0]], e.Name())
			}
		}
	}
	base := strings.Count(filepath.Clean(proposalRoot), string(os.PathSeparator))
	filepath.WalkDir(proposalRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if strings.Count(filepath.Clean(p), string(os.PathSeparator))-base > 5 {
			return fs.SkipDir
		}
		if p == proposalRoot {
			return nil
		}
		if nums := claude.Numbers(d.Name()); len(nums) > 0 {
			f.Proposals[nums[0]] = append(f.Proposals[nums[0]], d.Name())
			return fs.SkipDir
		}
		return nil
	})
	return f
}

// Classify resolves a ref kind to "project", "proposal" or "" (unresolved).
func (f Folders) Classify(kind, number string) string {
	switch kind {
	case "project", "proposal":
		return kind
	case "mention":
		if len(f.Projects[number]) > 0 {
			return "project"
		}
		if len(f.Proposals[number]) > 0 {
			return "proposal"
		}
	}
	return ""
}
