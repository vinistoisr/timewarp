package tray

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unsafe"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBoxW = user32.NewProc("MessageBoxW")
)

const (
	mbOK             = 0x00000000
	mbYesNo          = 0x00000004
	mbIconQuestion   = 0x00000020
	mbIconInfo       = 0x00000040
	idYes            = 6
)

func messageBox(title, text string, flags uintptr) int {
	titlePtr, _ := syscall.UTF16PtrFromString(title)
	textPtr, _ := syscall.UTF16PtrFromString(text)
	ret, _, _ := procMessageBoxW.Call(0, uintptr(unsafe.Pointer(textPtr)), uintptr(unsafe.Pointer(titlePtr)), flags)
	return int(ret)
}

// WelcomeText is the first-run notice: exactly what Timewarp records and where it goes.
const WelcomeText = "Timewarp helps you fill in your timesheet.\r\n\r\n" +
	"While you work, it saves:\r\n" +
	"• the title of the window you are using, like \"26-104 Report.docx - Word\"\r\n" +
	"• any project numbers in those titles\r\n" +
	"• the names of your Claude Code sessions and the project numbers in them\r\n\r\n" +
	"It does not save keystrokes, screenshots or what is inside your files.\r\n\r\n" +
	"Everything stays in one file on your computer, in a folder you pick next. " +
	"Timewarp does not send it to anyone. If you pick a OneDrive folder, " +
	"OneDrive backs it up like any other file you keep there.\r\n\r\n" +
	"When you run /timesheet, Claude reads this file to suggest your hours.\r\n\r\n" +
	"To pause or quit, right-click the purple clock icon near the Windows clock."

// AutoStartText asks whether to register the per-user startup task.
const AutoStartText = "Start Timewarp each time you sign in to Windows?\r\n\r\n" +
	"No administrator rights are needed. To remove it later, run: timewarp.exe -uninstall"

// HasExistingDB checks if any timewarp-*.db files exist in the given directory.
func HasExistingDB(dir string) bool {
	matches, err := filepath.Glob(filepath.Join(dir, "timewarp-*.db"))
	if err != nil {
		return false
	}
	return len(matches) > 0
}

// hiddenCmd returns a Cmd with the console window hidden.
func hiddenCmd(name string, args ...string) *exec.Cmd {
	cmd := exec.Command(name, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return cmd
}

// RunOnboarding shows a welcome dialog and folder picker if this is a first run.
// Returns the chosen DB path, or empty string if the user cancelled.
func RunOnboarding() string {
	messageBox("Welcome to Timewarp", WelcomeText, mbOK|mbIconInfo)

	cmd := hiddenCmd("powershell", "-NoProfile", "-Command",
		`Add-Type -AssemblyName System.Windows.Forms; `+
			`$d = New-Object System.Windows.Forms.FolderBrowserDialog; `+
			`$d.Description = 'Select or create a folder for Timewarp data'; `+
			`$d.ShowNewFolderButton = $true; `+
			`if ($d.ShowDialog() -eq 'OK') { $d.SelectedPath }`)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// OfferScheduledTask asks the user if they want Timewarp to start
// automatically and, if so, runs "timewarp.exe -install" as the current user.
func OfferScheduledTask(exePath, dbpath string) bool {
	ret := messageBox("Start Automatically?", AutoStartText, mbYesNo|mbIconQuestion)
	if ret != idYes {
		return false
	}
	out, err := hiddenCmd(exePath, "-install", "-dbpath", dbpath).CombinedOutput()
	if err != nil {
		messageBox("Timewarp", fmt.Sprintf("Failed to set up automatic start:\n%v\n%s", err, out), mbOK|mbIconInfo)
		return false
	}
	return true
}

// claudeConfigPath returns the path to Claude Desktop's config file, or empty if not found.
func claudeConfigPath() string {
	appdata := os.Getenv("APPDATA")
	if appdata == "" {
		return ""
	}
	p := filepath.Join(appdata, "Claude", "claude_desktop_config.json")
	if _, err := os.Stat(p); err != nil {
		return ""
	}
	return p
}

// IsClaudeConnected checks if the Claude Desktop config already has a timewarp MCP entry.
func IsClaudeConnected() bool {
	p := claudeConfigPath()
	if p == "" {
		return false
	}
	data, err := os.ReadFile(p)
	if err != nil {
		return false
	}
	var config map[string]interface{}
	if err := json.Unmarshal(data, &config); err != nil {
		return false
	}
	servers, ok := config["mcpServers"].(map[string]interface{})
	if !ok {
		return false
	}
	_, exists := servers["timewarp"]
	return exists
}

// ConnectClaude adds the timewarp MCP server entry to Claude Desktop's config.
// Returns a user-facing message describing the result.
func ConnectClaude(exePath, dbpath string) string {
	p := claudeConfigPath()
	if p == "" {
		return "Could not find Claude Desktop config.\n\nExpected location:\n%APPDATA%\\Claude\\claude_desktop_config.json\n\nMake sure Claude Desktop is installed."
	}

	data, err := os.ReadFile(p)
	if err != nil {
		return fmt.Sprintf("Could not read config file: %v", err)
	}

	var config map[string]json.RawMessage
	if err := json.Unmarshal(data, &config); err != nil {
		return fmt.Sprintf("Could not parse config file: %v\n\nYou may need to fix the JSON manually.", err)
	}

	// Parse or create mcpServers
	servers := map[string]json.RawMessage{}
	if raw, ok := config["mcpServers"]; ok {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return fmt.Sprintf("Could not parse mcpServers: %v\n\nYou may need to fix the JSON manually.", err)
		}
	}

	// Build the timewarp entry
	entry := map[string]interface{}{
		"command": exePath,
		"args":    []string{"-mcp", "-dbpath", dbpath},
	}
	entryJSON, _ := json.Marshal(entry)

	// Check if already connected with matching config
	if existing, exists := servers["timewarp"]; exists {
		var old map[string]interface{}
		if json.Unmarshal(existing, &old) == nil {
			oldCmd, _ := old["command"].(string)
			oldArgs, _ := json.Marshal(old["args"])
			newArgs, _ := json.Marshal(entry["args"])
			if oldCmd == exePath && string(oldArgs) == string(newArgs) {
				return "Timewarp is already connected to Claude Desktop."
			}
		}
	}

	_, wasExisting := servers["timewarp"]
	servers["timewarp"] = json.RawMessage(entryJSON)

	serversJSON, _ := json.Marshal(servers)
	config["mcpServers"] = json.RawMessage(serversJSON)

	out, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return fmt.Sprintf("Could not serialize config: %v", err)
	}

	if err := os.WriteFile(p, out, 0644); err != nil {
		return fmt.Sprintf("Could not write config file: %v", err)
	}

	if wasExisting {
		return "Updated! Restart Claude Desktop to apply the new Timewarp config."
	}
	return "Connected! Restart Claude Desktop to activate the Timewarp MCP server."
}
