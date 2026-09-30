package install

import (
	"strings"
	"testing"
)

func TestTaskName(t *testing.T) {
	if got := TaskName(`FGROUP\vincent`); got != "Timewarp-vincent" {
		t.Errorf("TaskName = %q", got)
	}
	if got := TaskName("solo"); got != "Timewarp-solo" {
		t.Errorf("TaskName = %q", got)
	}
}

func TestTaskXML(t *testing.T) {
	x := TaskXML(`FGROUP\vincent`, `C:\Users\vincent\timewarp\timewarp.exe`, `C:\Users\vincent\OneDrive - Flow Engineering\Timewarp Db`)
	for _, want := range []string{
		`<UserId>FGROUP\vincent</UserId>`,
		`<Command>C:\Users\vincent\timewarp\timewarp.exe</Command>`,
		`<Arguments>-dbpath "C:\Users\vincent\OneDrive - Flow Engineering\Timewarp Db"</Arguments>`,
		`<LogonType>InteractiveToken</LogonType>`,
		`<MultipleInstancesPolicy>IgnoreNew</MultipleInstancesPolicy>`,
	} {
		if !strings.Contains(x, want) {
			t.Errorf("task XML missing %s", want)
		}
	}
	if strings.Contains(x, "-silent") {
		t.Error("task must run with the tray icon")
	}
	if !strings.Contains(TaskXML("u", `C:\a&b\t.exe`, ""), `C:\a&amp;b\t.exe`) {
		t.Error("XML special characters must be escaped")
	}
}
