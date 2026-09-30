// Package claude records Claude Code activity for project time attribution.
package claude

import (
	"encoding/json"
	"regexp"
	"sort"
)

// Ref is a project or proposal number found in a Claude event.
type Ref struct {
	Kind   string // project, proposal, mention
	Number string
	Source string // path, prompt, cwd
}

var (
	tokenRe    = regexp.MustCompile(`\d+-\d+`)
	numberRe   = regexp.MustCompile(`^2\d-\d{3}$`)
	projPathRe = regexp.MustCompile(`(?i)[\\/]PROJECT[\\/]+20\d\d[\\/]+(2\d-\d{3})(?:\D|$)`)
	propPathRe = regexp.MustCompile(`(?i)[\\/]Proposals[\\/][^"'\r\n]*?(?:^|\D)(2\d-\d{3})(?:\D|$)`)
	propWordRe = regexp.MustCompile(`(?i)\b(?:proposal|prop)\.?\s*#?\s*(2\d-\d{3})(?:\D|$)`)
	projWordRe = regexp.MustCompile(`(?i)\bproject\s*#?\s*(2\d-\d{3})(?:\D|$)`)
)

// Strength orders ref kinds: project > proposal > mention.
func Strength(kind string) int {
	switch kind {
	case "project":
		return 3
	case "proposal":
		return 2
	case "mention":
		return 1
	}
	return 0
}

// Numbers returns the NN-NNN tokens in s that are not part of a longer
// digit-dash-digit token (so dates like 2026-09-30 do not match).
func Numbers(s string) []string {
	var out []string
	for _, tok := range tokenRe.FindAllString(s, -1) {
		if numberRe.MatchString(tok) {
			out = append(out, tok)
		}
	}
	return out
}

// ExtractRefs finds project and proposal numbers in a prompt, a tool input
// and the working directory. Each number appears once, with its strongest kind.
func ExtractRefs(prompt string, toolInput json.RawMessage, cwd string) []Ref {
	var found []Ref
	add := func(kind, num, src string) { found = append(found, Ref{kind, num, src}) }

	pathRefs := func(s, src string) {
		for _, m := range projPathRe.FindAllStringSubmatch(s, -1) {
			add("project", m[1], src)
		}
		for _, m := range propPathRe.FindAllStringSubmatch(s, -1) {
			add("proposal", m[1], src)
		}
	}

	for _, s := range jsonStrings(toolInput) {
		pathRefs(s, "path")
	}
	pathRefs(cwd, "cwd")
	if prompt != "" {
		pathRefs(prompt, "path")
		for _, m := range projWordRe.FindAllStringSubmatch(prompt, -1) {
			add("project", m[1], "prompt")
		}
		for _, m := range propWordRe.FindAllStringSubmatch(prompt, -1) {
			add("proposal", m[1], "prompt")
		}
		for _, n := range Numbers(prompt) {
			add("mention", n, "prompt")
		}
	}

	// Keep the strongest ref per number; order by strength, then first appearance.
	best := map[string]int{}
	var order []string
	for i, r := range found {
		j, ok := best[r.Number]
		if !ok {
			best[r.Number] = i
			order = append(order, r.Number)
		} else if Strength(r.Kind) > Strength(found[j].Kind) {
			best[r.Number] = i
		}
	}
	out := make([]Ref, 0, len(order))
	for _, n := range order {
		out = append(out, found[best[n]])
	}
	sort.SliceStable(out, func(a, b int) bool { return Strength(out[a].Kind) > Strength(out[b].Kind) })
	return out
}

// jsonStrings returns every string value in a JSON document, recursively.
func jsonStrings(raw json.RawMessage) []string {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return nil
	}
	var out []string
	var walk func(any)
	walk = func(x any) {
		switch t := x.(type) {
		case string:
			out = append(out, t)
		case []any:
			for _, e := range t {
				walk(e)
			}
		case map[string]any:
			for _, e := range t {
				walk(e)
			}
		}
	}
	walk(v)
	return out
}
