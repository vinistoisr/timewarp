package install

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

var hookEvents = []struct {
	name    string
	matcher bool
}{
	{"UserPromptSubmit", false},
	{"PostToolUse", true},
	{"Stop", false},
	{"SessionEnd", true},
}

// SettingsPath is the user's Claude Code settings file.
func SettingsPath() string {
	return filepath.Join(os.Getenv("USERPROFILE"), ".claude", "settings.json")
}

func isTimewarpHook(h any) bool {
	m, ok := h.(map[string]any)
	if !ok {
		return false
	}
	args, _ := m["args"].([]any)
	for _, a := range args {
		if a == "-claudehook" {
			return true
		}
	}
	return false
}

func parseSettings(data []byte) (map[string]any, error) {
	s := map[string]any{}
	if len(bytes.TrimSpace(data)) == 0 {
		return s, nil
	}
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, fmt.Errorf("settings.json is not valid JSON: %w", err)
	}
	return s, nil
}

// strip removes timewarp hook entries, dropping groups and events left empty.
func strip(s map[string]any) {
	hooks, _ := s["hooks"].(map[string]any)
	for event, v := range hooks {
		groups, _ := v.([]any)
		var keptGroups []any
		for _, g := range groups {
			gm, ok := g.(map[string]any)
			if !ok {
				keptGroups = append(keptGroups, g)
				continue
			}
			list, _ := gm["hooks"].([]any)
			var kept []any
			for _, h := range list {
				if !isTimewarpHook(h) {
					kept = append(kept, h)
				}
			}
			if len(kept) > 0 {
				gm["hooks"] = kept
				keptGroups = append(keptGroups, gm)
			}
		}
		if len(keptGroups) == 0 {
			delete(hooks, event)
		} else {
			hooks[event] = keptGroups
		}
	}
	if hooks != nil && len(hooks) == 0 {
		delete(s, "hooks")
	}
}

func marshal(s map[string]any) ([]byte, error) {
	return json.MarshalIndent(s, "", "  ")
}

// MergeHooks returns settings with exactly one timewarp hook per event,
// leaving every other setting and hook as it was.
func MergeHooks(data []byte, exePath, dbpath string) ([]byte, error) {
	s, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	strip(s)
	hooks, _ := s["hooks"].(map[string]any)
	if hooks == nil {
		hooks = map[string]any{}
		s["hooks"] = hooks
	}
	for _, ev := range hookEvents {
		entry := map[string]any{
			"type":    "command",
			"command": exePath,
			"args":    []any{"-claudehook", "-dbpath", dbpath},
			"timeout": 30,
			"async":   true,
		}
		group := map[string]any{"hooks": []any{entry}}
		if ev.matcher {
			group["matcher"] = ".*"
		}
		existing, _ := hooks[ev.name].([]any)
		hooks[ev.name] = append(existing, group)
	}
	return marshal(s)
}

// RemoveHooks returns settings with every timewarp hook removed.
func RemoveHooks(data []byte) ([]byte, error) {
	s, err := parseSettings(data)
	if err != nil {
		return nil, err
	}
	strip(s)
	return marshal(s)
}

func rewriteSettings(change func([]byte) ([]byte, error)) error {
	p := SettingsPath()
	old, err := os.ReadFile(p)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	updated, err := change(old)
	if err != nil {
		return err
	}
	if old != nil {
		if err := os.WriteFile(p+".timewarp-bak", old, 0o644); err != nil {
			return fmt.Errorf("could not back up settings.json: %w", err)
		}
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return err
	}
	return os.WriteFile(p, updated, 0o644)
}

// InstallHooks adds the timewarp hooks to the user's Claude Code settings.
func InstallHooks(exePath, dbpath string) error {
	return rewriteSettings(func(b []byte) ([]byte, error) { return MergeHooks(b, exePath, dbpath) })
}

// UninstallHooks removes the timewarp hooks from the user's Claude Code settings.
func UninstallHooks() error {
	return rewriteSettings(RemoveHooks)
}
