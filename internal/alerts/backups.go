package alerts

import (
	"github.com/daniel-vuky/server-control-panel/internal/backup"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// backupStepWords names a failed step in a scheduled backup's alert.
var backupStepWords = map[string]string{
	backup.StepDump:     "Dump",
	backup.StepCompress: "Compress",
	backup.StepUpload:   "Upload",
	backup.StepPrune:    "Prune",
}

// BackupEnded is the backup manager's OnEnded hook (decision 16): a scheduled
// backup that failed or was refused raises one alert, with no incident and no
// recovery (each run is its own event). A person's backup never alerts, and
// neither does an interrupted one (only a shutdown interrupts a backup).
//
// It runs while the manager may hold an enqueue's reservation, so it only
// builds the alert and queues it: it never blocks and never reads the store.
// A refusal whose row could not be stored (ID 0) still alerts.
func (n *Notifier) BackupEnded(b store.Backup) {
	if b.StartedBy != backup.ScheduleActor.Name {
		return
	}
	name := b.ProjectName
	if b.DBName != "" {
		name += " · " + b.DBName
	}
	title := "Scheduled backup failed: " + name
	var detail string
	switch b.State {
	case backup.StateFailed:
		if w := backupStepWords[b.FailedStep]; w != "" {
			title += " at " + w
		}
		detail = joinReason(b.Error, b.Hint)
	case backup.StateRefused:
		detail = joinReason("refused: "+b.Error, b.Hint)
	default:
		return
	}
	n.Raise(Alert{
		ProjectID: b.ProjectID,
		Action:    store.ActAlertBackup,
		Target:    b.ProjectName,
		Message:   Message{Title: title, Detail: detail},
	})
}
