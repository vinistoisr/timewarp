package install

import (
	"fmt"
	"os/exec"
	"strings"
	"syscall"
)

// MCPName is the server name registered with Claude Code.
const MCPName = "timewarp"

// MCPAddArgs is the claude CLI argument list that registers the Timewarp MCP
// server for the current user in every project.
func MCPAddArgs(exePath, dbpath string) []string {
	args := []string{"mcp", "add", "-s", "user", MCPName, "--", exePath, "-mcp"}
	if dbpath != "" {
		args = append(args, "-dbpath", dbpath)
	}
	return args
}

// MCPAddCommand is the same command as text, for the user to run by hand.
func MCPAddCommand(exePath, dbpath string) string {
	parts := []string{"claude"}
	for _, a := range MCPAddArgs(exePath, dbpath) {
		if strings.ContainsAny(a, " \t") {
			a = `"` + a + `"`
		}
		parts = append(parts, a)
	}
	return strings.Join(parts, " ")
}

func runClaude(args ...string) (string, error) {
	bin, err := exec.LookPath("claude")
	if err != nil {
		return "", err
	}
	cmd := exec.Command(bin, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// InstallMCP registers the MCP server with Claude Code, replacing any earlier
// user-scope entry so a moved exe or data folder is picked up.
func InstallMCP(exePath, dbpath string) error {
	if _, err := exec.LookPath("claude"); err != nil {
		return fmt.Errorf("Claude Code (claude) is not on PATH; register Timewarp later with:\n  %s", MCPAddCommand(exePath, dbpath))
	}
	_, _ = runClaude("mcp", "remove", "-s", "user", MCPName)
	if out, err := runClaude(MCPAddArgs(exePath, dbpath)...); err != nil {
		return fmt.Errorf("claude mcp add failed: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// UninstallMCP removes the user-scope MCP registration. Missing is not an error.
func UninstallMCP() {
	_, _ = runClaude("mcp", "remove", "-s", "user", MCPName)
}
