//go:build unix

package runner

import (
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group and makes the
// timeout kill the whole group, so that a grandchild (docker compose runs as a
// plugin child of docker) cannot outlive the deadline and keep the pipes open.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
	}
}
