package backup

import (
	"context"
	"strconv"
	"strings"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/drive"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// pruneWait bounds one Drive delete of the Prune step.
const pruneWait = 30 * time.Second

// wordsCandidatesUnread is Prune's failure when the stored copies cannot be
// read.
const wordsCandidatesUnread = "Could not read the stored copies. It is retried after the next backup."

// prune runs step 4, "Remove old copies", after a successful upload (spec
// decisions 9-12): the database's copies on Drive (its rows with a Drive file
// that is not pruned, whatever their state, the new one first) past the number
// to keep are deleted oldest first and stamped pruned. A delete answered
// folder_missing (the file is already gone) counts as removed.
//
// Prune never fails the backup, whose copy is safe on Drive: a Drive error
// marks the step failed with the words of decision 11 and stops; the copies
// left are picked up by the next successful backup. Each delete runs on a
// context the worker's cancellation does not end, bounded by pruneWait, so
// Close never turns an uploaded backup interrupted; between deletes Prune
// checks ctx and, once it is cancelled, stops the same way.
func (m *Manager) prune(ctx context.Context, j *job) {
	m.setStep(j, iPrune, stepRunning, "")
	keep := j.keep

	sctx, cancel := storeContext(ctx)
	rows, err := m.st.RetentionCandidates(sctx, j.b.ProjectID, j.b.DBKey)
	cancel()
	if err != nil {
		m.log.Error("backup: read the stored copies", "backup", j.b.ID, "err", err)
		m.setStep(j, iPrune, stepFailed, wordsCandidatesUnread)
		return
	}
	copies := newestFirst(j.b.ID, rows)
	if len(copies) <= keep {
		m.setStep(j, iPrune, stepDone, "Nothing to remove · "+strconv.Itoa(len(copies))+" of "+strconv.Itoa(keep)+" kept")
		return
	}
	old := copies[keep:]

	dctx := context.WithoutCancel(ctx)
	removed := 0
	var sess drive.Session
	for i := len(old) - 1; i >= 0; i-- {
		if ctx.Err() != nil {
			m.setStep(j, iPrune, stepFailed, notRemovedWords(i+1, wordsInterrupted))
			return
		}
		if sess == nil {
			gctx, gcancel := context.WithTimeout(dctx, pruneWait)
			sess, _, err = m.d.Session(gctx)
			gcancel()
			if err != nil {
				m.setStep(j, iPrune, stepFailed, notRemovedWords(i+1, m.driveWords(j, "remove old copies", err)))
				return
			}
		}
		if err := m.removeCopy(dctx, sess, old[i]); err != nil {
			m.setStep(j, iPrune, stepFailed, notRemovedWords(i+1, m.driveWords(j, "remove old copies", err)))
			return
		}
		removed++
	}
	m.setStep(j, iPrune, stepDone, "Removed "+oldCopies(removed)+" · keeping "+strconv.Itoa(keep))
}

// newestFirst is the database's copies newest first with the new backup (id)
// first: the stored row normally is, but a failed progress write must never
// make the new copy a candidate for removal.
func newestFirst(id int64, rows []store.Backup) []store.Backup {
	out := make([]store.Backup, 0, len(rows)+1)
	out = append(out, store.Backup{ID: id})
	for _, b := range rows {
		if b.ID != id {
			out = append(out, b)
		}
	}
	return out
}

// removeCopy deletes one copy's Drive file and stamps its row. Drive's
// folder_missing (the file is gone already) counts as removed. A failed stamp
// is only logged: the file is gone, and the next prune's delete answers
// folder_missing.
func (m *Manager) removeCopy(ctx context.Context, sess drive.Session, b store.Backup) error {
	dctx, cancel := context.WithTimeout(ctx, pruneWait)
	err := sess.Delete(dctx, b.DriveFileID)
	cancel()
	if err != nil && drive.CodeOf(err) != drive.CodeFolderMissing {
		return err
	}
	sctx, scancel := storeContext(ctx)
	defer scancel()
	if err := m.st.MarkPruned(sctx, b.ID, m.now()); err != nil {
		m.log.Error("backup: stamp a removed copy", "backup", b.ID, "err", err)
	}
	return nil
}

// notRemovedWords is a failed Prune's detail (decision 11): "Could not remove
// 2 old copies: <words>. It is retried after the next backup."
func notRemovedWords(n int, words string) string {
	words = strings.TrimSuffix(strings.TrimSpace(words), ".")
	return "Could not remove " + oldCopies(n) + ": " + words + ". It is retried after the next backup."
}

func oldCopies(n int) string {
	if n == 1 {
		return "1 old copy"
	}
	return strconv.Itoa(n) + " old copies"
}
