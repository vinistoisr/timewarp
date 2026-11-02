package install

import "testing"

func TestMCPAddCommand(t *testing.T) {
	got := MCPAddCommand(`C:\Tools\timewarp.exe`, `C:\Users\joe\OneDrive - Flow Engineering\Timewarp Db`)
	want := `claude mcp add -s user timewarp -- C:\Tools\timewarp.exe -mcp -dbpath "C:\Users\joe\OneDrive - Flow Engineering\Timewarp Db"`
	if got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
	if args := MCPAddArgs(`C:\t.exe`, ""); len(args) != 8 || args[7] != "-mcp" {
		t.Fatalf("no dbpath: %v", args)
	}
}
