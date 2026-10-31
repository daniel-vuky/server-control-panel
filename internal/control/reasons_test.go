package control_test

import (
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/control"
	"github.com/daniel-vuky/server-control-panel/internal/project"
)

// The view's backup reasons are the manager's refusal codes, so the UI words
// a view's reason and a refusal the same way.
func TestBackupReasonsMatchRefusalCodes(t *testing.T) {
	for reason, code := range map[string]string{
		control.ReasonNotFound:          backup.CodeNotFound,
		project.ReasonNotSupported:      backup.CodeNotSupported,
		project.ReasonNoDBName:          backup.CodeNoDBName,
		project.ReasonDockerDown:        backup.CodeDockerDown,
		project.ReasonDBNotRunning:      backup.CodeDBNotRunning,
		control.ReasonDriveNotConnected: backup.CodeDriveNotConnected,
		control.ReasonDriveExpired:      backup.CodeDriveExpired,
		control.ReasonFolderMissing:     backup.CodeFolderMissing,
	} {
		if reason != code {
			t.Errorf("reason %q, refusal code %q", reason, code)
		}
	}
}
