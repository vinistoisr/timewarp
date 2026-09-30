package claude

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestNumbers(t *testing.T) {
	got := Numbers(`26-104,26-105 on 2026-09-30, breaker 20-100A, 126-104, 26-1045`)
	want := []string{"26-104", "26-105", "20-100"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Numbers = %v, want %v", got, want)
	}
}

func TestExtractRefs_ProjectPathInToolInput(t *testing.T) {
	in := json.RawMessage(`{"file_path":"S:\\PROJECT\\2026\\26-104 CBC Arc Flash\\Working\\calc.xlsx"}`)
	got := ExtractRefs("", in, `C:\Users\vincent`)
	want := []Ref{{"project", "26-104", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_ForwardSlashCommand(t *testing.T) {
	in := json.RawMessage(`{"command":"python \"s:/project/2025/25-245 Canada Place/x.py\" && ls"}`)
	got := ExtractRefs("", in, "")
	want := []Ref{{"project", "25-245", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_ProposalPath(t *testing.T) {
	in := json.RawMessage(`{"path":"S:\\Proposals\\VR\\2025\\October\\prop 25-415 - Beta Ave Burnaby"}`)
	got := ExtractRefs("", in, "")
	want := []Ref{{"proposal", "25-415", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_NestedToolInput(t *testing.T) {
	in := json.RawMessage(`{"edits":[{"file":"S:/PROJECT/2026/26-087 GI/a.docx"}],"n":3}`)
	got := ExtractRefs("", in, "")
	want := []Ref{{"project", "26-087", "path"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_PromptWords(t *testing.T) {
	got := ExtractRefs("update proposal 26-555 and project 26-104, also look at 26-060 on 2026-09-30", nil, "")
	want := []Ref{
		{"project", "26-104", "prompt"},
		{"proposal", "26-555", "prompt"},
		{"mention", "26-060", "prompt"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_CwdAndStrongestWins(t *testing.T) {
	got := ExtractRefs("check 26-104 please", nil, `S:\PROJECT\2026\26-104 CBC`)
	want := []Ref{{"project", "26-104", "cwd"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestExtractRefs_ToolInputBareNumberIgnored(t *testing.T) {
	got := ExtractRefs("", json.RawMessage(`{"query":"26-104 invoice"}`), "")
	if len(got) != 0 {
		t.Errorf("bare numbers in tool input must be ignored, got %v", got)
	}
}
