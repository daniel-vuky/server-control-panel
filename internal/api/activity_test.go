package api

import (
	"context"
	"net/http"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// zonedRFC3339 is an RFC 3339 time at the Asia/Ho_Chi_Minh offset.
var zonedRFC3339 = regexp.MustCompile(`^\d{4}-\d\d-\d\dT\d\d:\d\d:\d\d\+07:00$`)

// seedActivity stores, oldest first: a start of alpha, a backup of alpha, a
// sent alert on alpha (by the system, through Telegram and email), a change
// of the alert channel (no project), a stop of beta (untracked afterwards),
// a Back up all (no project) and a running restart of alpha. It returns the
// ids in that order, and alpha's and beta's project ids.
func seedActivity(t *testing.T, d Deps) (ids []int64, alpha, beta int64) {
	t.Helper()
	ctx := context.Background()
	alpha, beta = trackedIDs(t, d)
	person := func(action string, project int64, target string) store.Activity {
		return store.Activity{Actor: "ops@example.com", ActorKind: store.ActorPerson, Action: action, ProjectID: project, Target: target}
	}
	record := func(a store.Activity, result, reason string, channels []string) {
		t.Helper()
		id, err := d.Store.StartActivity(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		if err := d.Store.FinishActivityWith(ctx, id, result, reason, channels); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	record(person(store.ActProjectStart, alpha, "Alpha"), store.ResultOK, "", nil)
	record(store.Activity{Actor: "schedule", ActorKind: store.ActorSchedule, Action: store.ActBackupRun, ProjectID: alpha, Target: "Alpha"},
		store.ResultOK, "84.2 MB · 41s", nil)
	record(store.Activity{Actor: "System", ActorKind: store.ActorSystem, Action: store.ActAlertProject, ProjectID: alpha, Target: "Alpha"},
		store.ResultSent, "web restarted 5 times", []string{"telegram", "email"})
	record(person(store.ActAlertsChannel, 0, "Telegram"), store.ResultOK, "on → off", nil)
	record(person(store.ActProjectStop, beta, "Beta"), store.ResultOK, "", nil)
	record(person(store.ActBackupAll, 0, "All projects"), store.ResultOK, "2 succeeded, 0 failed", nil)
	running, err := d.Store.StartActivity(ctx, person(store.ActProjectRestart, alpha, "Alpha"))
	if err != nil {
		t.Fatal(err)
	}
	ids = append(ids, running)
	if _, err := d.Store.UntrackProject(ctx, beta); err != nil {
		t.Fatal(err)
	}
	return ids, alpha, beta
}

func entryIDs(es []ActivityEntry) []int64 {
	out := make([]int64, 0, len(es))
	for _, e := range es {
		out = append(out, e.ID)
	}
	return out
}

func getActivity(t *testing.T, h http.Handler, query string) ActivityPage {
	t.Helper()
	rec := do(h, http.MethodGet, "/api/activity"+query)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /api/activity%s = %d %s", query, rec.Code, rec.Body.String())
	}
	return decode[ActivityPage](t, rec)
}

func TestActivityQuery(t *testing.T) {
	d := newTestDeps(t)
	d.Zone = mustZone(t, "Asia/Ho_Chi_Minh")
	addProjects(t, d)
	h := NewRouter(d)
	ids, alphaID, betaID := seedActivity(t, d)
	start, backupRun, alert, channel, stop, all, restart := ids[0], ids[1], ids[2], ids[3], ids[4], ids[5], ids[6]

	// Everything, newest first.
	page := getActivity(t, h, "")
	if !slices.Equal(entryIDs(page.Entries), []int64{restart, all, stop, channel, alert, backupRun, start}) || page.More {
		t.Fatalf("all = %v more %v", entryIDs(page.Entries), page.More)
	}
	byID := map[int64]ActivityEntry{}
	for _, e := range page.Entries {
		byID[e.ID] = e
		if !zonedRFC3339.MatchString(e.At) {
			t.Errorf("entry %d at = %q, want RFC 3339 at +07:00", e.ID, e.At)
		}
		if e.Channels == nil {
			t.Errorf("entry %d channels = nil, want a list", e.ID)
		}
	}
	// The kinds: alert.* is an alert, backup.run and backup.all are backups,
	// alerts.channel and the rest are actions.
	for id, kind := range map[int64]string{start: "action", backupRun: "backup", alert: "alert", channel: "action", stop: "action", all: "backup", restart: "action"} {
		if byID[id].Kind != kind {
			t.Errorf("entry %d (%s) kind = %q, want %q", id, byID[id].Action, byID[id].Kind, kind)
		}
	}
	a := byID[alert]
	if a.Actor != "System" || a.ActorKind != "system" || a.Action != "alert.project" || a.Result != "sent" ||
		a.Reason != "web restarted 5 times" || strings.Join(a.Channels, ",") != "telegram,email" ||
		a.ProjectID == nil || *a.ProjectID != alphaID || !a.ProjectTracked || a.Target != "Alpha" {
		t.Errorf("alert = %+v", a)
	}
	if a.FinishedAt == nil || !zonedRFC3339.MatchString(*a.FinishedAt) {
		t.Errorf("alert finishedAt = %v", a.FinishedAt)
	}
	if r := byID[restart]; r.FinishedAt != nil || r.Result != "running" {
		t.Errorf("running restart = %+v, want no finishedAt", r)
	}
	if c := byID[channel]; c.ProjectID != nil || c.ProjectTracked {
		t.Errorf("channel change = %+v, want projectId null", c)
	}
	// Beta was untracked: its id stays, it no longer links.
	if s := byID[stop]; s.ProjectID == nil || *s.ProjectID != betaID || s.ProjectTracked {
		t.Errorf("stop of beta = %+v, want projectId %d, not tracked", s, betaID)
	}
	body := do(h, http.MethodGet, "/api/activity").Body.String()
	if !strings.Contains(body, `"finishedAt":null`) || !strings.Contains(body, `"projectId":null`) || !strings.Contains(body, `"channels":[]`) {
		t.Errorf("body = %s, want null finishedAt and projectId and empty channels", body)
	}

	// The kind and project filters.
	for _, tc := range []struct {
		query string
		want  []int64
	}{
		{"?kind=alert", []int64{alert}},
		{"?kind=backup", []int64{all, backupRun}},
		{"?kind=action", []int64{restart, stop, channel, start}},
		{"?project=" + strconv.FormatInt(alphaID, 10), []int64{restart, alert, backupRun, start}},
		{"?kind=action&project=" + strconv.FormatInt(alphaID, 10), []int64{restart, start}},
		{"?project=" + strconv.FormatInt(betaID, 10), []int64{stop}},
		{"?project=9999", []int64{}},
	} {
		if got := getActivity(t, h, tc.query); !slices.Equal(entryIDs(got.Entries), tc.want) || got.More {
			t.Errorf("%s = %v more %v, want %v", tc.query, entryIDs(got.Entries), got.More, tc.want)
		}
	}
	if body := do(h, http.MethodGet, "/api/activity?project=9999").Body.String(); !strings.Contains(body, `"entries":[]`) {
		t.Errorf("empty page = %s, want entries []", body)
	}

	// Paging: limit, then before the last id shown.
	first := getActivity(t, h, "?limit=3")
	if !slices.Equal(entryIDs(first.Entries), []int64{restart, all, stop}) || !first.More {
		t.Fatalf("first page = %v more %v", entryIDs(first.Entries), first.More)
	}
	second := getActivity(t, h, "?limit=3&before="+strconv.FormatInt(stop, 10))
	if !slices.Equal(entryIDs(second.Entries), []int64{channel, alert, backupRun}) || !second.More {
		t.Errorf("second page = %v more %v", entryIDs(second.Entries), second.More)
	}
	last := getActivity(t, h, "?limit=3&before="+strconv.FormatInt(backupRun, 10))
	if !slices.Equal(entryIDs(last.Entries), []int64{start}) || last.More {
		t.Errorf("last page = %v more %v", entryIDs(last.Entries), last.More)
	}
}

func TestActivityDefaultLimit(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	for i := 0; i < 120; i++ {
		if err := d.Store.RecordActivity(context.Background(),
			store.Activity{Actor: "ops@example.com", ActorKind: store.ActorPerson, Action: store.ActScanStart, Target: "ws"},
			store.ResultOK, ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := getActivity(t, h, ""); len(got.Entries) != 50 || !got.More {
		t.Errorf("default page = %d entries, more %v, want 50 and more", len(got.Entries), got.More)
	}
	if got := getActivity(t, h, "?limit=100"); len(got.Entries) != 100 || !got.More {
		t.Errorf("limit 100 = %d entries, more %v", len(got.Entries), got.More)
	}
}

func TestActivityBadQuery400(t *testing.T) {
	h := NewRouter(newTestDeps(t))
	for query, field := range map[string]string{
		"?kind=alerts":    "kind",
		"?kind=ALERT":     "kind",
		"?project=0":      "project",
		"?project=-3":     "project",
		"?project=abc":    "project",
		"?before=0":       "before",
		"?before=x":       "before",
		"?limit=0":        "limit",
		"?limit=101":      "limit",
		"?limit=2.5":      "limit",
		"?limit=-1":       "limit",
		"?kind=":          "",
		"?project=":       "",
		"?before=&limit=": "",
	} {
		rec := do(h, http.MethodGet, "/api/activity"+query)
		if field == "" {
			if rec.Code != http.StatusOK {
				t.Errorf("GET /api/activity%s = %d %s, want 200", query, rec.Code, rec.Body.String())
			}
			continue
		}
		if rec.Code != http.StatusBadRequest {
			t.Errorf("GET /api/activity%s = %d %s, want 400", query, rec.Code, rec.Body.String())
			continue
		}
		if got := decode[QueryError](t, rec); got.Field != field || got.Error == "" {
			t.Errorf("GET /api/activity%s = %+v, want field %s", query, got, field)
		}
	}
}
