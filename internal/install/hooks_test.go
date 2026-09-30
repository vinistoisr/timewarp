package install

import (
	"encoding/json"
	"strings"
	"testing"
)

const existingSettings = `{
  "model": "opus",
  "hooks": {
    "PostToolUse": [
      {"matcher": "Skill", "hooks": [{"type": "command", "command": "python.exe", "args": ["skill_usage_logger.py"], "async": true}]}
    ],
    "SessionEnd": [
      {"matcher": ".*", "hooks": [{"type": "command", "command": "python.exe", "args": ["token_usage.py", "--hook"]}]}
    ]
  }
}`

func hookCount(t *testing.T, data []byte, event string) (timewarp, other int) {
	t.Helper()
	var s map[string]any
	if err := json.Unmarshal(data, &s); err != nil {
		t.Fatal(err)
	}
	hooks, _ := s["hooks"].(map[string]any)
	groups, _ := hooks[event].([]any)
	for _, g := range groups {
		for _, h := range g.(map[string]any)["hooks"].([]any) {
			if isTimewarpHook(h) {
				timewarp++
			} else {
				other++
			}
		}
	}
	return
}

func TestMergeHooks_PreservesOthers(t *testing.T) {
	out, err := MergeHooks([]byte(existingSettings), `C:\tw\timewarp.exe`, `C:\db`)
	if err != nil {
		t.Fatal(err)
	}
	out, err = MergeHooks(out, `C:\tw\timewarp.exe`, `C:\db`) // idempotent
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"} {
		if tw, _ := hookCount(t, out, ev); tw != 1 {
			t.Errorf("%s: %d timewarp hooks, want 1", ev, tw)
		}
	}
	if _, other := hookCount(t, out, "PostToolUse"); other != 1 {
		t.Error("existing PostToolUse hook lost")
	}
	if _, other := hookCount(t, out, "SessionEnd"); other != 1 {
		t.Error("existing SessionEnd hook lost")
	}
	if !strings.Contains(string(out), `"model": "opus"`) {
		t.Error("other settings lost")
	}
	if !strings.Contains(string(out), `"-claudehook"`) || !strings.Contains(string(out), `"async": true`) {
		t.Error("hook entry missing -claudehook or async")
	}
}

func TestMergeHooks_EmptySettings(t *testing.T) {
	for _, in := range [][]byte{nil, []byte(""), []byte("{}")} {
		out, err := MergeHooks(in, `C:\tw\timewarp.exe`, `C:\db`)
		if err != nil {
			t.Fatal(err)
		}
		if tw, _ := hookCount(t, out, "Stop"); tw != 1 {
			t.Errorf("input %q: Stop hook not added", in)
		}
	}
}

func TestRemoveHooks(t *testing.T) {
	merged, _ := MergeHooks([]byte(existingSettings), `C:\tw\timewarp.exe`, `C:\db`)
	out, err := RemoveHooks(merged)
	if err != nil {
		t.Fatal(err)
	}
	for _, ev := range []string{"UserPromptSubmit", "PostToolUse", "Stop", "SessionEnd"} {
		if tw, _ := hookCount(t, out, ev); tw != 0 {
			t.Errorf("%s: timewarp hook not removed", ev)
		}
	}
	if _, other := hookCount(t, out, "PostToolUse"); other != 1 {
		t.Error("remove deleted another hook")
	}
	if strings.Contains(string(out), "UserPromptSubmit") {
		t.Error("empty event list should be dropped")
	}
}

func TestMergeHooks_InvalidJSON(t *testing.T) {
	if _, err := MergeHooks([]byte("{not json"), "x", "y"); err == nil {
		t.Error("invalid settings.json must be an error, not overwritten")
	}
}
