// Package install registers the startup task and the Claude Code hooks
// for the current user, without elevation.
package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"unicode/utf16"
)

const taskXML = `<?xml version="1.0" encoding="UTF-16"?>
<Task version="1.4" xmlns="http://schemas.microsoft.com/windows/2004/02/mit/task">
  <RegistrationInfo>
    <Description>Timewarp activity tracker. Starts at logon and restarts within 5 minutes if it stops.</Description>
  </RegistrationInfo>
  <Principals>
    <Principal id="Author">
      <UserId>%[1]s</UserId>
      <LogonType>InteractiveToken</LogonType>
      <RunLevel>LeastPrivilege</RunLevel>
    </Principal>
  </Principals>
  <Settings>
    <MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>
    <DisallowStartIfOnBatteries>false</DisallowStartIfOnBatteries>
    <StopIfGoingOnBatteries>false</StopIfGoingOnBatteries>
    <StartWhenAvailable>true</StartWhenAvailable>
    <ExecutionTimeLimit>PT0S</ExecutionTimeLimit>
    <RestartOnFailure>
      <Interval>PT1M</Interval>
      <Count>3</Count>
    </RestartOnFailure>
    <IdleSettings>
      <StopOnIdleEnd>false</StopOnIdleEnd>
      <RestartOnIdle>false</RestartOnIdle>
    </IdleSettings>
  </Settings>
  <Triggers>
    <LogonTrigger>
      <Repetition>
        <Interval>PT5M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <Enabled>true</Enabled>
      <UserId>%[1]s</UserId>
      <Delay>PT30S</Delay>
    </LogonTrigger>
    <CalendarTrigger>
      <Repetition>
        <Interval>PT5M</Interval>
        <StopAtDurationEnd>false</StopAtDurationEnd>
      </Repetition>
      <StartBoundary>2020-01-01T00:05:00</StartBoundary>
      <Enabled>true</Enabled>
      <ScheduleByDay>
        <DaysInterval>1</DaysInterval>
      </ScheduleByDay>
    </CalendarTrigger>
  </Triggers>
  <Actions Context="Author">
    <Exec>
      <Command>%[2]s</Command>%[3]s
    </Exec>
  </Actions>
</Task>
`

func xmlEscape(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// TaskName is the per-user scheduled task name, e.g. Timewarp-vincent.
func TaskName(user string) string {
	if i := strings.LastIndex(user, `\`); i >= 0 {
		user = user[i+1:]
	}
	return "Timewarp-" + user
}

// TaskXML builds the task definition for user.
func TaskXML(user, exePath, dbpath string) string {
	args := ""
	if dbpath != "" {
		args = "\n      <Arguments>-dbpath \"" + xmlEscape(dbpath) + "\"</Arguments>"
	}
	return fmt.Sprintf(taskXML, xmlEscape(user), xmlEscape(exePath), args)
}

// utf16leBytes encodes s as UTF-16LE with a BOM; schtasks rejects UTF-8 XML.
func utf16leBytes(s string) []byte {
	u := utf16.Encode([]rune(s))
	b := make([]byte, 0, 2+len(u)*2)
	b = append(b, 0xFF, 0xFE)
	for _, r := range u {
		b = append(b, byte(r), byte(r>>8))
	}
	return b
}

func schtasks(args ...string) (string, error) {
	cmd := exec.Command("schtasks", args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := cmd.CombinedOutput()
	return string(out), err
}

// InstallTask registers (or replaces) the user's startup task. No elevation.
func InstallTask(user, exePath, dbpath string) error {
	tmp := filepath.Join(os.TempDir(), "timewarp-task.xml")
	if err := os.WriteFile(tmp, utf16leBytes(TaskXML(user, exePath, dbpath)), 0o600); err != nil {
		return fmt.Errorf("could not write task definition: %w", err)
	}
	defer os.Remove(tmp)
	if out, err := schtasks("/create", "/tn", TaskName(user), "/xml", tmp, "/f"); err != nil {
		return fmt.Errorf("schtasks create failed: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// UninstallTask removes the user's startup task.
func UninstallTask(user string) error {
	if out, err := schtasks("/delete", "/tn", TaskName(user), "/f"); err != nil {
		return fmt.Errorf("schtasks delete failed: %v: %s", err, strings.TrimSpace(out))
	}
	return nil
}

// HasTask reports whether the user's startup task exists.
func HasTask(user string) bool {
	_, err := schtasks("/query", "/tn", TaskName(user))
	return err == nil
}

// LegacyTaskPresent reports whether the old shared "\Timewarp" task exists,
// including when it belongs to another account and cannot be read.
func LegacyTaskPresent() bool {
	out, err := schtasks("/query", "/tn", "Timewarp")
	return err == nil || strings.Contains(strings.ToLower(out), "access is denied")
}
