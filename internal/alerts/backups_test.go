package alerts

import (
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

func TestBackupEndedAlertsScheduledOnly(t *testing.T) {
	tg := &recSender{}
	r := newRig(t, Frame{PublicURL: "https://panel.x.io"}, Target{Name: ChannelTelegram, Sender: tg})
	r.start(t)

	base := store.Backup{ID: 7, ProjectID: 3, ProjectName: "crm", DBName: "crm_db", StartedBy: "schedule"}
	failed := base
	failed.State, failed.FailedStep = "failed", "dump"
	failed.Error, failed.Hint = "Access denied for user 'crm'@'%'", "Check MYSQL_PASSWORD in .env"
	refused := base
	refused.ID, refused.State, refused.Error = 0, "refused", "Google Drive is not connected." // its insert failed: still an alert
	noHint := base
	noHint.State, noHint.FailedStep, noHint.Error = "failed", "upload", "Could not reach Google Drive."
	interrupted := base
	interrupted.State = "interrupted"
	ok := base
	ok.State = "ok"
	person := failed
	person.StartedBy = "admin@company.vn"
	personRefused := refused
	personRefused.StartedBy = "admin@company.vn"
	for _, b := range []store.Backup{failed, interrupted, ok, person, refused, personRefused, noHint} {
		r.n.BackupEnded(b)
	}
	r.n.Raise(oneOff("sentinel"))
	rows := r.waitRows(t, "three alerts and the sentinel", finished(4))
	if rows[3].Target != "crm" || rows[3].Reason != "sentinel detail" {
		t.Fatalf("an unexpected alert: rows = %+v", rows)
	}

	want := []struct{ title, detail string }{
		{"Scheduled backup failed: crm · crm_db at Dump", "Access denied for user 'crm'@'%' · Check MYSQL_PASSWORD in .env"},
		{"Scheduled backup failed: crm · crm_db", "refused: Google Drive is not connected."},
		{"Scheduled backup failed: crm · crm_db at Upload", "Could not reach Google Drive."},
	}
	got := tg.delivered()
	for i, w := range want {
		m := got[i]
		if m.Title != w.title || m.Detail != w.detail || m.Recovery {
			t.Fatalf("message %d = %q / %q, want %q / %q", i, m.Title, m.Detail, w.title, w.detail)
		}
		if m.Link != "https://panel.x.io/projects/3" {
			t.Fatalf("message %d link = %q", i, m.Link)
		}
		row := rows[i]
		if row.Action != store.ActAlertBackup || row.ProjectID != 3 || row.Target != "crm" || row.Reason != w.detail {
			t.Fatalf("row %d = %+v", i, row)
		}
	}
	if text := got[0].Title + " · " + got[0].Detail; text != "Scheduled backup failed: crm · crm_db at Dump · Access denied for user 'crm'@'%' · Check MYSQL_PASSWORD in .env" {
		t.Fatalf("message = %q", text)
	}
	if keys := r.n.OpenKeys(); len(keys) != 0 {
		t.Fatalf("open keys = %q: a backup alert opens no incident", keys)
	}
}
