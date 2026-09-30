// Package claudewin identifies Claude Code windows and normalises their titles.
package claudewin

import (
	"strings"
	"unicode"
)

var claudeProcs = map[string]bool{
	"windowsterminal.exe": true,
	"claude.exe":          true,
	"code.exe":            true,
}

// IsClaudeProcess reports whether a focused process can host a Claude Code session.
func IsClaudeProcess(proc string) bool {
	return claudeProcs[strings.ToLower(proc)]
}

// NormalizeTitle strips the leading status glyph (spinner, ✳, ◐ ...) and
// whitespace that Claude Code puts in front of the session title.
func NormalizeTitle(title string) string {
	return strings.TrimSpace(strings.TrimLeftFunc(title, func(r rune) bool {
		return unicode.IsSymbol(r) || unicode.IsSpace(r)
	}))
}
