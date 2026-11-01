package backup

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/project"
	"github.com/daniel-vuky/server-control-panel/internal/runner"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// CheckError is a database check that ran and failed: the database is not
// there, or the credentials do not reach it. Words is what the UI shows: the
// last line of the check's stderr, masked, then the hint when one matches.
type CheckError struct {
	Words string
}

func (e *CheckError) Error() string { return "backup: check: " + e.Words }

// CheckDatabase checks, before a database is added by hand, that d can be
// reached inside its container: the type's db.*.check action runs in the
// resolved compose service with the name and the variable names, masked by
// the project's secrets, through Run (15 s). It refuses first, running
// nothing, with a *Refusal: not_found for a missing project, not_supported
// (a service that is not in the project, a project with no compose file),
// docker_down and db_not_running ("Start <service> first"), each with the
// service once it is resolved. A failed check is a *CheckError; a check cut
// short by ctx returns ctx.Err(). Nothing is stored or recorded.
func (m *Manager) CheckDatabase(ctx context.Context, t store.Tracked, d project.Database) error {
	if t.ID <= 0 || t.MissingSince != nil {
		return &Refusal{Code: CodeNotFound, Words: wordsNotFound}
	}
	if r := checkLocation(d); r != nil {
		return r
	}
	p, r := m.plan(ctx, t, d)
	if r != nil {
		if r.Service == "" {
			r.Service = p.service
		}
		return r
	}
	// The check always names the database, even where a backup would use the
	// image's default one.
	p.values["dbname"] = d.Name
	p.values["uservar"], p.values["passvar"] = d.UserVar, d.PasswordVar
	_, override := p.values["override"]
	res, err := m.r.Run(ctx, runner.StackCompose, checkAction(d.Type, override), p.values, p.secrets)
	if err == nil {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return &CheckError{Words: m.checkWords(d, p, res, err)}
}

// checkWords words a failed check, masked again with the project's secrets.
// The exit code is the verdict; stderr only says why.
func (m *Manager) checkWords(d project.Database, p plan, res runner.Result, err error) string {
	var (
		exit    *runner.ExitError
		timeout *runner.TimeoutError
	)
	switch {
	case errors.As(err, &exit):
		masked := runner.Mask(string(res.Stderr), p.secrets)
		if strings.TrimSpace(masked) == "" {
			masked = runner.Mask(exit.Stderr, p.secrets)
		}
		line := ErrorLine(masked)
		if line == "" {
			line = "The check exited with status " + strconv.Itoa(exit.Code) + "."
		}
		hint := Hint(d.Type, p.image, d.Name, p.service, masked, d.Sources, d.UserVar, d.PasswordVar)
		if hint == "" {
			return line
		}
		if !strings.HasSuffix(line, ".") {
			line += "."
		}
		return line + " " + hint
	case errors.As(err, &timeout):
		return "The database did not answer within " + strconv.Itoa(int(timeout.After.Round(time.Second)/time.Second)) + " seconds."
	}
	m.log.Warn("backup: the database check could not run", "service", p.service, "err", runner.Mask(err.Error(), p.secrets))
	return cut(runner.Mask(err.Error(), p.secrets), maxErrorLine)
}

// checkAction is the check action of a database type.
func checkAction(dbType string, override bool) runner.Action {
	a := runner.DBPgCheck
	switch dbType {
	case "mysql":
		a = runner.DBMySQLCheck
	case "mongodb":
		a = runner.DBMongoCheck
	}
	if override {
		a += ".override"
	}
	return a
}
