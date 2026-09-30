package claudewin

import "testing"

func TestNormalizeTitle(t *testing.T) {
	cases := map[string]string{
		"✳ Timewarp reliability issues":     "Timewarp reliability issues",
		"⠏ vincent":                         "vincent",
		"◐ EPR from alex":                   "EPR from alex",
		"◑ Scirocco-boost Hardware Plan V2": "Scirocco-boost Hardware Plan V2",
		"Claude":                            "Claude",
		"26-104 load calc.xlsx - Excel":     "26-104 load calc.xlsx - Excel",
		"  ✳  spaced":                       "spaced",
		"":                                  "",
	}
	for in, want := range cases {
		if got := NormalizeTitle(in); got != want {
			t.Errorf("NormalizeTitle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestIsClaudeProcess(t *testing.T) {
	for _, p := range []string{"WindowsTerminal.exe", "claude.exe", "Claude.exe", "Code.exe"} {
		if !IsClaudeProcess(p) {
			t.Errorf("%s should be a Claude process", p)
		}
	}
	for _, p := range []string{"EXCEL.EXE", "acad.exe", ""} {
		if IsClaudeProcess(p) {
			t.Errorf("%s should not be a Claude process", p)
		}
	}
}
