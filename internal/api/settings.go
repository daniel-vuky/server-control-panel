package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The settings limits (spec §5).
const (
	maxDomain      = 253
	maxDomainLabel = 63
	maxAlertCPU    = 100
	maxAlertRAMMB  = 1_048_576
)

// The settings field names, as the JSON and the 422 bodies name them.
const (
	fieldDisplayName = "displayName"
	fieldDomain      = "publicDomain"
	fieldTemplate    = "stopTemplate"
	fieldAlertCPU    = "alertCpuPercent"
	fieldAlertRAM    = "alertRamMb"
)

const (
	templateStop = "stop"
	templateDown = "down"

	msgSelfTemplate = "The panel's own project always stops with stop."
)

// projectSettings is POST /api/projects/{id}/settings: it replaces the five
// settings of a tracked project (a Not found one too) and answers the updated
// Project. It needs no project lock: it writes only the panel's own record,
// and a command already running keeps the template it started with. A save
// that changes something is recorded as project.settings, listing only the
// changed fields, and is pushed to every open tab through Hub.Refresh.
func (s *server) projectSettings(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	}
	var req SettingsRequest
	typeErr, ok := readSettings(w, r, &req)
	if !ok {
		return
	}
	cur, err := s.Store.TrackedProject(r.Context(), id)
	switch {
	case errors.Is(err, store.ErrNotFound):
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	next, fe := validSettings(req, typeErr)
	if fe == nil && cur.Self && next.StopTemplate != cur.StopTemplate {
		fe = &FieldError{Error: msgSelfTemplate, Field: fieldTemplate, Code: "not_allowed"}
	}
	if fe != nil {
		writeJSON(w, http.StatusUnprocessableEntity, fe)
		return
	}

	// The write outlives the request, so a client that goes away does not
	// leave a saved change without its activity row.
	ctx := context.WithoutCancel(r.Context())
	before, after, err := s.Store.UpdateSettings(ctx, id, next)
	switch {
	case errors.Is(err, store.ErrNotFound): // untracked meanwhile
		writeError(w, http.StatusNotFound, msgNotTracked)
		return
	case err != nil:
		s.internalError(w, r, err)
		return
	}
	if changes := settingsChanges(before, after); changes != "" {
		s.record(r, store.Activity{Action: store.ActProjectSettings, ProjectID: id, Target: after.DisplayName}, store.ResultOK, changes)
		s.Hub.Refresh()
	}
	writeJSON(w, http.StatusOK, toProjects(s.readViews(r.Context(), []store.Tracked{after}))[0])
}

// readSettings decodes the body like readJSON, except that a number that does
// not fit an alert threshold (a fraction, or too large for an int) is not a
// 400: its field is returned, to be answered 422 out_of_range in form order.
func readSettings(w http.ResponseWriter, r *http.Request, req *SettingsRequest) (typeErr string, ok bool) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxBody))
	err := dec.Decode(req)
	var ute *json.UnmarshalTypeError
	if errors.As(err, &ute) && strings.HasPrefix(ute.Value, "number") && (ute.Field == fieldAlertCPU || ute.Field == fieldAlertRAM) {
		typeErr, err = ute.Field, nil
	}
	if err == nil {
		if _, terr := dec.Token(); !errors.Is(terr, io.EOF) {
			err = errors.New("trailing data")
		}
	}
	if err != nil {
		writeError(w, http.StatusBadRequest, "The request body is not valid JSON.")
		return "", false
	}
	return typeErr, true
}

// validSettings checks req field by field in form order and returns the
// settings to store, or the first field error. typeErr names an alert field
// whose number did not decode.
func validSettings(req SettingsRequest, typeErr string) (store.Settings, *FieldError) {
	name := strings.TrimSpace(req.DisplayName)
	switch n := utf8.RuneCountInString(name); {
	case n == 0:
		return store.Settings{}, &FieldError{Error: "Enter a name.", Field: fieldDisplayName, Code: "required"}
	case n > maxDisplayName:
		return store.Settings{}, &FieldError{
			Error: fmt.Sprintf("The name must be at most %d characters.", maxDisplayName), Field: fieldDisplayName, Code: "too_long",
		}
	case strings.ContainsFunc(name, unicode.IsControl):
		return store.Settings{}, &FieldError{Error: "The name cannot contain control characters.", Field: fieldDisplayName, Code: "invalid"}
	}
	domain := strings.ToLower(strings.TrimSpace(req.PublicDomain))
	if domain != "" && !validDomain(domain) {
		return store.Settings{}, &FieldError{
			Error: "Enter a domain such as app.example.com, with no https://, port or path.", Field: fieldDomain, Code: "invalid",
		}
	}
	if req.StopTemplate != templateStop && req.StopTemplate != templateDown {
		return store.Settings{}, &FieldError{Error: "Choose stop or down.", Field: fieldTemplate, Code: "invalid"}
	}
	if typeErr == fieldAlertCPU || !inRange(req.AlertCPUPercent, maxAlertCPU) {
		return store.Settings{}, &FieldError{
			Error: fmt.Sprintf("Enter a whole number from 1 to %d, or leave it empty.", maxAlertCPU), Field: fieldAlertCPU, Code: "out_of_range",
		}
	}
	if typeErr == fieldAlertRAM || !inRange(req.AlertRAMMB, maxAlertRAMMB) {
		return store.Settings{}, &FieldError{
			Error: fmt.Sprintf("Enter a whole number from 1 to %d, or leave it empty.", maxAlertRAMMB), Field: fieldAlertRAM, Code: "out_of_range",
		}
	}
	return store.Settings{
		DisplayName: name, PublicDomain: domain, StopTemplate: req.StopTemplate,
		AlertCPUPercent: req.AlertCPUPercent, AlertRAMMB: req.AlertRAMMB,
	}, nil
}

// inRange says whether p is nil (the server default) or 1 to maxV.
func inRange(p *int, maxV int) bool {
	return p == nil || (*p >= 1 && *p <= maxV)
}

// validDomain says whether d (lowercased) is a host name of at least two
// labels, each 1 to 63 of a-z, 0-9 and "-" not starting or ending with "-",
// and at most 253 characters in all. A scheme, port or path cannot pass.
func validDomain(d string) bool {
	if len(d) > maxDomain {
		return false
	}
	labels := strings.Split(d, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if l == "" || len(l) > maxDomainLabel || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
		for _, c := range l {
			if (c < 'a' || c > 'z') && (c < '0' || c > '9') && c != '-' {
				return false
			}
		}
	}
	return true
}

// settingsChanges is the activity reason of a save: each changed field as
// `field: old → new`, joined with "; ", in form order. Names and domains are
// quoted; a null threshold reads "default". It is "" when nothing changed.
func settingsChanges(before, after store.Tracked) string {
	var parts []string
	add := func(field, old, next string) {
		if old != next {
			parts = append(parts, field+": "+old+" → "+next)
		}
	}
	add(fieldDisplayName, strconv.Quote(before.DisplayName), strconv.Quote(after.DisplayName))
	add(fieldDomain, strconv.Quote(before.PublicDomain), strconv.Quote(after.PublicDomain))
	add(fieldTemplate, before.StopTemplate, after.StopTemplate)
	add(fieldAlertCPU, threshold(before.AlertCPUPercent), threshold(after.AlertCPUPercent))
	add(fieldAlertRAM, threshold(before.AlertRAMMB), threshold(after.AlertRAMMB))
	return strings.Join(parts, "; ")
}

// threshold is an alert threshold for the activity reason.
func threshold(p *int) string {
	if p == nil {
		return "default"
	}
	return strconv.Itoa(*p)
}
