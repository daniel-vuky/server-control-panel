//go:build unix

package runner

import (
	"errors"
	"os"
	"os/exec"
	"syscall"
)

// setProcessGroup puts the child in its own process group and makes the
// timeout kill the whole group, so that a grandchild (docker compose runs as a
// plugin child of docker) cannot outlive the deadline and keep the pipes open.
func setProcessGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if errors.Is(err, syscall.ESRCH) {
			// The group already ended, as when the command exits at the deadline.
			return os.ErrProcessDone
		}
		return err
	}
}
