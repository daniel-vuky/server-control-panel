package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

func settingsURL(id int64) string { return actionURL(id, "settings") }

// alphaSettings is alpha's settings as addProjects stores them.
func alphaSettings() SettingsRequest {
	return SettingsRequest{DisplayName: "Alpha", PublicDomain: "", StopTemplate: "stop"}
}

func intp(v int) *int { return &v }

// trackedByID reads one tracked project from the store.
func trackedByID(t *testing.T, d Deps, id int64) store.Tracked {
	t.Helper()
	p, err := d.Store.TrackedProject(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSettingsValidation(t *testing.T) {
	label := func(n int) string { return strings.Repeat("a", n) }
	d253 := label(63) + "." + label(63) + "." + label(63) + "." + label(61)
	d254 := label(63) + "." + label(63) + "." + label(63) + "." + label(62)
	if len(d253) != 253 || len(d254) != 254 {
		t.Fatalf("domain lengths %d %d", len(d253), len(d254))
	}

	type want struct{ field, code string }
	for _, tc := range []struct {
		name string
		edit func(*SettingsRequest)
		raw  string // when set, the body instead of the edited request
		want want
	}{
		{name: "empty name", edit: func(r *SettingsRequest) { r.DisplayName = "   " }, want: want{"displayName", "required"}},
		{name: "81 chars", edit: func(r *SettingsRequest) { r.DisplayName = strings.Repeat("é", 81) }, want: want{"displayName", "too_long"}},
		{name: "control char", edit: func(r *SettingsRequest) { r.DisplayName = "Al\x01pha" }, want: want{"displayName", "invalid"}},
		{name: "newline", edit: func(r *SettingsRequest) { r.DisplayName = "Al\npha" }, want: want{"displayName", "invalid"}},
		{name: "scheme", edit: func(r *SettingsRequest) { r.PublicDomain = "https://x.com" }, want: want{"publicDomain", "invalid"}},
		{name: "one label", edit: func(r *SettingsRequest) { r.PublicDomain = "x" }, want: want{"publicDomain", "invalid"}},
		{name: "leading dash", edit: func(r *SettingsRequest) { r.PublicDomain = "-a.com" }, want: want{"publicDomain", "invalid"}},
		{name: "trailing dash", edit: func(r *SettingsRequest) { r.PublicDomain = "a-.com" }, want: want{"publicDomain", "invalid"}},
		{name: "empty label", edit: func(r *SettingsRequest) { r.PublicDomain = "a..com" }, want: want{"publicDomain", "invalid"}},
		{name: "trailing dot", edit: func(r *SettingsRequest) { r.PublicDomain = "a.com." }, want: want{"publicDomain", "invalid"}},
		{name: "port", edit: func(r *SettingsRequest) { r.PublicDomain = "a.com:8080" }, want: want{"publicDomain", "invalid"}},
		{name: "path", edit: func(r *SettingsRequest) { r.PublicDomain = "a.com/x" }, want: want{"publicDomain", "invalid"}},
		{name: "64-char label", edit: func(r *SettingsRequest) { r.PublicDomain = label(64) + ".com" }, want: want{"publicDomain", "invalid"}},
		{name: "254 chars", edit: func(r *SettingsRequest) { r.PublicDomain = d254 }, want: want{"publicDomain", "invalid"}},
		{name: "template halt", edit: func(r *SettingsRequest) { r.StopTemplate = "halt" }, want: want{"stopTemplate", "invalid"}},
		{name: "template empty", edit: func(r *SettingsRequest) { r.StopTemplate = "" }, want: want{"stopTemplate", "invalid"}},
		{name: "CPU 0", edit: func(r *SettingsRequest) { r.AlertCPUPercent = intp(0) }, want: want{"alertCpuPercent", "out_of_range"}},
		{name: "CPU 101", edit: func(r *SettingsRequest) { r.AlertCPUPercent = intp(101) }, want: want{"alertCpuPercent", "out_of_range"}},
		{name: "RAM 0", edit: func(r *SettingsRequest) { r.AlertRAMMB = intp(0) }, want: want{"alertRamMb", "out_of_range"}},
		{name: "RAM 1048577", edit: func(r *SettingsRequest) { r.AlertRAMMB = intp(1048577) }, want: want{"alertRamMb", "out_of_range"}},
		{
			name: "CPU fraction",
			raw:  `{"displayName":"Alpha","publicDomain":"","stopTemplate":"stop","alertCpuPercent":50.5,"alertRamMb":null}`,
			want: want{"alertCpuPercent", "out_of_range"},
		},
		{
			name: "RAM huge",
			raw:  `{"displayName":"Alpha","publicDomain":"","stopTemplate":"stop","alertCpuPercent":null,"alertRamMb":1e30}`,
			want: want{"alertRamMb", "out_of_range"},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := newTestDeps(t)
			h := NewRouter(d)
			addProjects(t, d)
			alpha, _ := trackedIDs(t, d)
			var body any = tc.raw
			if tc.raw == "" {
				req := alphaSettings()
				tc.edit(&req)
				body = req
			}

			rec := doJSON(h, http.MethodPost, settingsURL(alpha), body)
			if rec.Code != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d %s, want 422", rec.Code, rec.Body.String())
			}
			got := decode[FieldError](t, rec)
			if got.Field != tc.want.field || got.Code != tc.want.code || got.Error == "" {
				t.Errorf("body = %+v, want %s %s with a message", got, tc.want.field, tc.want.code)
			}
			if p := trackedByID(t, d, alpha); p.DisplayName != "Alpha" || p.PublicDomain != "" || p.StopTemplate != "stop" ||
				p.AlertCPUPercent != nil || p.AlertRAMMB != nil {
				t.Errorf("stored = %+v, want it unchanged", p)
			}
			if rows := activity(t, d); len(rows) != 0 {
				t.Errorf("activity = %+v, want none", rows)
			}
		})
	}

	t.Run("valid edges", func(t *testing.T) {
		d := newTestDeps(t)
		h := NewRouter(d)
		addProjects(t, d)
		alpha, _ := trackedIDs(t, d)
		for _, req := range []SettingsRequest{
			{DisplayName: strings.Repeat("é", 80), PublicDomain: d253, StopTemplate: "down", AlertCPUPercent: intp(1), AlertRAMMB: intp(1)},
			{DisplayName: " Alpha ", PublicDomain: " Shop-1.Example.COM ", StopTemplate: "stop", AlertCPUPercent: intp(100), AlertRAMMB: intp(1048576)},
		} {
			rec := doJSON(h, http.MethodPost, settingsURL(alpha), req)
			if rec.Code != http.StatusOK {
				t.Fatalf("%+v = %d %s, want 200", req, rec.Code, rec.Body.String())
			}
		}
		p := trackedByID(t, d, alpha)
		if p.DisplayName != "Alpha" || p.PublicDomain != "shop-1.example.com" || *p.AlertCPUPercent != 100 || *p.AlertRAMMB != 1048576 {
			t.Errorf("stored = %+v, want the trimmed name and the lowercased domain", p)
		}
	})
}

func TestSettingsSelfTemplateNotAllowed(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	_, beta := trackedIDs(t, d) // beta is the panel's own project

	req := SettingsRequest{DisplayName: "Beta", StopTemplate: "down"}
	rec := doJSON(h, http.MethodPost, settingsURL(beta), req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d %s, want 422", rec.Code, rec.Body.String())
	}
	if got := decode[FieldError](t, rec); got != (FieldError{Error: "The panel's own project always stops with stop.", Field: "stopTemplate", Code: "not_allowed"}) {
		t.Errorf("body = %+v", got)
	}
	if p := trackedByID(t, d, beta); p.StopTemplate != "stop" {
		t.Errorf("stored template = %q, want stop", p.StopTemplate)
	}

	// Its other settings can change.
	req = SettingsRequest{DisplayName: "Panel", StopTemplate: "stop", AlertRAMMB: intp(512)}
	if rec := doJSON(h, http.MethodPost, settingsURL(beta), req); rec.Code != http.StatusOK {
		t.Fatalf("self rename = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if p := trackedByID(t, d, beta); p.DisplayName != "Panel" || p.AlertRAMMB == nil || *p.AlertRAMMB != 512 {
		t.Errorf("stored = %+v", p)
	}
}

func TestSettingsRecordsChangedFieldsOnly(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })

	req := SettingsRequest{DisplayName: "Alpha API", StopTemplate: "down", AlertCPUPercent: intp(80)}
	rec := doJSON(h, http.MethodPost, settingsURL(alpha), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	got := decode[Project](t, rec)
	if got.ID != alpha || got.DisplayName != "Alpha API" || got.StopTemplate != "down" || got.PublicDomain != "" ||
		got.AlertCPUPercent == nil || *got.AlertCPUPercent != 80 || got.AlertRAMMB != nil ||
		got.Status != "running" || len(got.Containers) != 2 {
		t.Errorf("body = %+v, want the updated running project", got)
	}

	rows := activity(t, d)
	if len(rows) != 1 {
		t.Fatalf("activity = %+v, want one row", rows)
	}
	r := rows[0]
	if r.Action != store.ActProjectSettings || r.Result != store.ResultOK || r.Actor != "test@example.com" ||
		r.ActorKind != store.ActorPerson || r.ProjectID != alpha || r.Target != "Alpha API" {
		t.Errorf("row = %+v", r)
	}
	if want := `displayName: "Alpha" → "Alpha API"; stopTemplate: stop → down; alertCpuPercent: default → 80`; r.Reason != want {
		t.Errorf("reason = %q, want %q", r.Reason, want)
	}

	req = SettingsRequest{DisplayName: "Alpha API", PublicDomain: "alpha.example.com", StopTemplate: "down", AlertRAMMB: intp(2048)}
	if rec := doJSON(h, http.MethodPost, settingsURL(alpha), req); rec.Code != http.StatusOK {
		t.Fatalf("second save = %d %s", rec.Code, rec.Body.String())
	}
	rows = activity(t, d)
	if want := `publicDomain: "" → "alpha.example.com"; alertCpuPercent: 80 → default; alertRamMb: default → 2048`; len(rows) != 2 || rows[0].Reason != want {
		t.Errorf("rows = %+v, want a second row with reason %q", rows, want)
	}

	// The settings route is not an action: no lifecycle command ran.
	f := dockerOf(d)
	f.mu.Lock()
	runs := len(f.lifecycleRuns)
	f.mu.Unlock()
	if runs != 0 {
		t.Errorf("lifecycle runs = %d, want none", runs)
	}
}

func TestSettingsNoChangeNoActivity(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)

	req := alphaSettings()
	req.DisplayName = "  Alpha  "
	req.PublicDomain = "  "
	rec := doJSON(h, http.MethodPost, settingsURL(alpha), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := decode[Project](t, rec); got.DisplayName != "Alpha" {
		t.Errorf("body = %+v", got)
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("activity = %+v, want none", rows)
	}
}

func TestSettingsUnknown404(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	_, beta := trackedIDs(t, d)
	gamma := beta + 1 // stored right after beta, as ignored

	for _, id := range []string{"999", "0", "-1", "abc", strconv.FormatInt(gamma, 10)} {
		rec := doJSON(h, http.MethodPost, "/api/projects/"+id+"/settings", alphaSettings())
		if rec.Code != http.StatusNotFound {
			t.Errorf("%s = %d %s, want 404", id, rec.Code, rec.Body.String())
		}
	}
	if rows := activity(t, d); len(rows) != 0 {
		t.Errorf("activity = %+v, want none", rows)
	}
}

func TestSettingsNotFoundProjectEditable(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	markMissing(t, d)

	req := alphaSettings()
	req.DisplayName = "Old alpha"
	rec := doJSON(h, http.MethodPost, settingsURL(alpha), req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d %s, want 200", rec.Code, rec.Body.String())
	}
	if got := decode[Project](t, rec); got.DisplayName != "Old alpha" || got.Status != "notfound" || got.MissingSince == "" {
		t.Errorf("body = %+v, want the renamed Not found project", got)
	}
}

func TestSettingsNeedsJSON415(t *testing.T) {
	d := newTestDeps(t)
	h := NewRouter(d)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)

	b, _ := json.Marshal(SettingsRequest{DisplayName: "Changed", StopTemplate: "stop"})
	for _, ct := range []string{"", "text/plain"} {
		req := httptest.NewRequest(http.MethodPost, settingsURL(alpha), strings.NewReader(string(b)))
		if ct != "" {
			req.Header.Set("Content-Type", ct)
		}
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnsupportedMediaType {
			t.Errorf("Content-Type %q = %d, want 415", ct, rec.Code)
		}
	}
	if p := trackedByID(t, d, alpha); p.DisplayName != "Alpha" {
		t.Errorf("stored name = %q, want it unchanged", p.DisplayName)
	}
}

func TestSettingsRefreshesHub(t *testing.T) {
	d := newTestDeps(t)
	addProjects(t, d)
	alpha, _ := trackedIDs(t, d)
	dockerOf(d).set(func(f *fakeDocker) { f.containers = alphaUp() })
	h := NewRouter(d)
	srv := httptest.NewServer(h)
	defer srv.Close()

	r, closeFn := openSSE(t, srv.URL+"/api/events")
	defer closeFn()
	if s := nextState(t, r); s.Projects[0].DisplayName != "Alpha" {
		t.Fatalf("first state = %+v", s)
	}

	req := alphaSettings()
	req.DisplayName = "Alpha API"
	req.StopTemplate = "down"
	if rec := doJSON(h, http.MethodPost, settingsURL(alpha), req); rec.Code != http.StatusOK {
		t.Fatalf("save = %d %s", rec.Code, rec.Body.String())
	}
	start := time.Now()
	s := stateUntil(t, r, "the new name", func(s LiveState) bool { return s.Projects[0].DisplayName == "Alpha API" })
	if time.Since(start) > 4*time.Second {
		t.Errorf("the change took %v to arrive, want it without waiting for a tick", time.Since(start))
	}
	if s.Projects[0].StopTemplate != "down" {
		t.Errorf("state = %+v, want the down template", s.Projects[0])
	}
}
