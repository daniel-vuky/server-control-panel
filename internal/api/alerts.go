package api

import (
	"context"
	"errors"
	"net/http"

	"github.com/daniel-vuky/server-control-panel/internal/alerts"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// The alert settings' activity targets and 409 codes.
const (
	targetTelegram   = "Telegram"
	targetEmail      = "Email"
	targetThresholds = "Alert thresholds"

	codeNoSecretKey        = "no_secret_key"
	codeAlertNotConfigured = "not_configured"

	msgNoSecretKey      = "Set PANEL_SECRET_KEY in panel.env first."
	msgChannelNotSetUp  = "Set up this channel first."
	msgUnknownChannel   = "There is no such alert channel."
	fieldThresholdsCPU  = "cpuPercent"
	fieldThresholdsRAM  = "ramPercent"
	fieldThresholdsDisk = "diskPercent"
	fieldSustained      = "sustainedMinutes"
)

// channelTargets maps a {channel} path segment to its activity target; any
// other segment is a 404.
var channelTargets = map[string]string{
	store.ChannelTelegram: targetTelegram,
	store.ChannelEmail:    targetEmail,
}

// alertsConfig maps the service's view to the API's: hints only, never the
// bot token.
func alertsConfig(v alerts.View) AlertsConfig {
	return AlertsConfig{
		Telegram: alertChannel(v.Telegram),
		Email:    alertChannel(v.Email),
		Thresholds: AlertThresholds{
			CPUPercent: v.Thresholds.CPUPercent, RAMPercent: v.Thresholds.RAMPercent,
			DiskPercent: v.Thresholds.DiskPercent, SustainedMinutes: v.SustainedMinutes,
		},
		CanSeal: v.CanSeal,
	}
}

func alertChannel(c alerts.ChannelView) AlertChannel {
	recipients := c.Recipients
	if recipients == nil {
		recipients = []string{}
	}
	return AlertChannel{
		Available: c.Available, Configured: c.Configured, Enabled: c.Enabled,
		TokenHint: c.TokenHint, ChatID: c.ChatID, Recipients: recipients, Reason: c.Reason,
	}
}

// alertSettings is GET /api/alerts: the channels (hints only) and the
// thresholds.
func (s *server) alertSettings(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, alertsConfig(s.Alerts.View()))
}

// alertError answers err, when there is one, and returns true: a field
// error is 422, ErrNoSecretKey and ErrNotConfigured are 409 AlertRefused,
// anything else 500. target names the channel the request is about.
func (s *server) alertError(w http.ResponseWriter, r *http.Request, target string, err error) bool {
	var fe *alerts.FieldError
	switch {
	case err == nil:
		return false
	case errors.As(err, &fe):
		writeFieldError(w, fe.Field, fe.Code, fe.Message)
	case errors.Is(err, alerts.ErrNoSecretKey):
		writeJSON(w, http.StatusConflict, AlertRefused{Error: codeNoSecretKey, Message: msgNoSecretKey})
	case errors.Is(err, alerts.ErrNotConfigured):
		writeJSON(w, http.StatusConflict, s.notConfigured(target))
	default:
		s.internalError(w, r, err)
	}
	return true
}

// alertSaved answers a save: the error, or 200 with the settings after a
// change (changes not empty) is recorded under action and target. The reason
// never holds the bot token: the service words it.
func (s *server) alertSaved(w http.ResponseWriter, r *http.Request, action, target, changes string, err error) {
	if s.alertError(w, r, target, err) {
		return
	}
	if changes != "" {
		s.record(r, store.Activity{Action: action, Target: target}, store.ResultOK, changes)
	}
	writeJSON(w, http.StatusOK, alertsConfig(s.Alerts.View()))
}

// notConfigured is the 409 body for a channel that is not set up: email on a
// server without SMTP names what is missing.
func (s *server) notConfigured(target string) AlertRefused {
	if target == targetEmail {
		if reason := s.Alerts.View().Email.Reason; reason != "" {
			return AlertRefused{Error: codeAlertNotConfigured, Message: reason}
		}
	}
	return AlertRefused{Error: codeAlertNotConfigured, Message: msgChannelNotSetUp}
}

// saveTelegram is POST /api/alerts/telegram {token, chatId}: an empty token
// keeps the stored one (422 on token when it cannot be opened), an empty
// chatId the stored chat. 409 no_secret_key without PANEL_SECRET_KEY, 422 on
// token or chatId. The save outlives the request, so a stored change keeps its
// activity row.
func (s *server) saveTelegram(w http.ResponseWriter, r *http.Request) {
	var req TelegramRequest
	if !readJSON(w, r, &req) {
		return
	}
	changes, err := s.Alerts.SaveTelegram(context.WithoutCancel(r.Context()), req.Token, req.ChatID)
	s.alertSaved(w, r, store.ActAlertsChannel, targetTelegram, changes, err)
}

// saveEmail is POST /api/alerts/email {recipients}: 1 to 10 addresses. 409
// not_configured on a server without SMTP, 422 on recipients.
func (s *server) saveEmail(w http.ResponseWriter, r *http.Request) {
	var req EmailRequest
	if !readJSON(w, r, &req) {
		return
	}
	changes, err := s.Alerts.SaveEmail(context.WithoutCancel(r.Context()), req.Recipients)
	s.alertSaved(w, r, store.ActAlertsChannel, targetEmail, changes, err)
}

// alertChannelTarget is the activity target of r's {channel}; it answers 404
// and returns false for anything but telegram or email.
func alertChannelTarget(w http.ResponseWriter, r *http.Request) (string, bool) {
	target, ok := channelTargets[r.PathValue("channel")]
	if !ok {
		writeError(w, http.StatusNotFound, msgUnknownChannel)
	}
	return target, ok
}

// setAlertEnabled is POST /api/alerts/{channel}/enabled {enabled}: turns a
// set-up channel on or off (409 not_configured otherwise).
func (s *server) setAlertEnabled(w http.ResponseWriter, r *http.Request) {
	target, ok := alertChannelTarget(w, r)
	if !ok {
		return
	}
	var req AlertEnabledRequest
	if !readJSON(w, r, &req) {
		return
	}
	changes, err := s.Alerts.SetEnabled(context.WithoutCancel(r.Context()), r.PathValue("channel"), req.Enabled)
	s.alertSaved(w, r, store.ActAlertsChannel, target, changes, err)
}

// testAlertChannel is POST /api/alerts/{channel}/test: sends a test message
// through a set-up channel, on or off, and answers {ok, message} (409
// not_configured when it is not set up). A test is not an alert: nothing is
// recorded.
func (s *server) testAlertChannel(w http.ResponseWriter, r *http.Request) {
	target, ok := alertChannelTarget(w, r)
	if !ok {
		return
	}
	res, err := s.Alerts.Test(r.Context(), r.PathValue("channel"))
	if s.alertError(w, r, target, err) {
		return
	}
	writeJSON(w, http.StatusOK, AlertTestResult{OK: res.OK, Message: res.Message})
}

// saveAlertThresholds is POST /api/alerts/thresholds {cpuPercent, ramPercent,
// diskPercent, sustainedMinutes}: whole numbers, percent 1 to 100 and minutes
// 1 to 60, else 422 on the first field in form order. GET /api/server reads
// the saved values from the next request on.
func (s *server) saveAlertThresholds(w http.ResponseWriter, r *http.Request) {
	var req AlertThresholds
	typeErr, ok := readJSONNumbers(w, r, &req, fieldThresholdsCPU, fieldThresholdsRAM, fieldThresholdsDisk, fieldSustained)
	if !ok {
		return
	}
	// A number that is not whole is out of range: 0 makes the service name
	// it, after any earlier field in error.
	switch typeErr {
	case fieldThresholdsCPU:
		req.CPUPercent = 0
	case fieldThresholdsRAM:
		req.RAMPercent = 0
	case fieldThresholdsDisk:
		req.DiskPercent = 0
	case fieldSustained:
		req.SustainedMinutes = 0
	}
	changes, err := s.Alerts.SetThresholds(context.WithoutCancel(r.Context()),
		req.CPUPercent, req.RAMPercent, req.DiskPercent, req.SustainedMinutes)
	s.alertSaved(w, r, store.ActAlertsThresholds, targetThresholds, changes, err)
}
