package gitx

import (
	"os/exec"
	"runtime"
)

// ShellCommand runs a command line through the platform shell: /bin/sh on
// Unix, cmd.exe on Windows (PowerShell is not assumed).
func ShellCommand(line string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		return exec.Command("cmd", "/C", line)
	}
	return exec.Command("/bin/sh", "-c", line)
}
