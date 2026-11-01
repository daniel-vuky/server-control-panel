package store

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestAlertSettingsDefaultsAndNoopSave(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	a, err := s.AlertSettings(ctx)
	want := AlertSettings{CPUPercent: 90, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 5}
	if err != nil || a != want {
		t.Fatalf("AlertSettings = %+v, %v; want %+v", a, err, want)
	}

	before, changed, err := s.SetAlertThresholds(ctx, want)
	if err != nil || changed || before != want {
		t.Fatalf("same save = %+v changed=%v err=%v", before, changed, err)
	}
	// TunnelSeenConnected is not a threshold: a save never touches it, and
	// carrying it in the argument is not a change.
	if err := s.SetTunnelSeenConnected(ctx, true); err != nil {
		t.Fatal(err)
	}
	same := want
	same.TunnelSeenConnected = false
	if _, changed, err := s.SetAlertThresholds(ctx, same); err != nil || changed {
		t.Fatalf("save with a different TunnelSeenConnected: changed=%v err=%v", changed, err)
	}
	if a, _ := s.AlertSettings(ctx); !a.TunnelSeenConnected {
		t.Fatalf("a save reset TunnelSeenConnected: %+v", a)
	}

	next := AlertSettings{CPUPercent: 80, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 5}
	before, changed, err = s.SetAlertThresholds(ctx, next)
	if err != nil || !changed || before.CPUPercent != 90 || !before.TunnelSeenConnected {
		t.Fatalf("CPU 80 = before %+v changed=%v err=%v", before, changed, err)
	}
	got, _ := s.AlertSettings(ctx)
	if got.CPUPercent != 80 || got.RAMPercent != 90 || got.DiskPercent != 85 || got.SustainedMinutes != 5 || !got.TunnelSeenConnected {
		t.Fatalf("after save = %+v", got)
	}
	if err := s.SetTunnelSeenConnected(ctx, false); err != nil {
		t.Fatal(err)
	}
	if got, _ := s.AlertSettings(ctx); got.TunnelSeenConnected || got.CPUPercent != 80 {
		t.Fatalf("after clearing seen = %+v", got)
	}
	// The table's CHECKs hold the ranges.
	if _, _, err := s.SetAlertThresholds(ctx, AlertSettings{CPUPercent: 0, RAMPercent: 90, DiskPercent: 85, SustainedMinutes: 5}); err == nil {
		t.Fatal("CPU 0 was stored")
	}
}

func TestAlertChannelRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	if m, err := s.AlertChannels(ctx); err != nil || len(m) != 0 {
		t.Fatalf("fresh AlertChannels = %+v, %v", m, err)
	}

	tg := AlertChannel{Kind: "telegram", Enabled: true, SealedToken: "v1:abc", ChatID: "-1001234"}
	before, existed, changed, err := s.SetAlertChannel(ctx, tg)
	if err != nil || existed || !changed || before.Kind != "telegram" || before.Enabled {
		t.Fatalf("first telegram save = %+v existed=%v changed=%v err=%v", before, existed, changed, err)
	}
	em := AlertChannel{Kind: "email", Recipients: []string{"a@x.io", "b@x.io"}}
	if _, existed, changed, err := s.SetAlertChannel(ctx, em); err != nil || existed || !changed {
		t.Fatalf("email save existed=%v changed=%v err=%v", existed, changed, err)
	}
	m, err := s.AlertChannels(ctx)
	if err != nil || len(m) != 2 {
		t.Fatalf("AlertChannels = %+v, %v", m, err)
	}
	if g := m["telegram"]; !g.Enabled || g.SealedToken != "v1:abc" || g.ChatID != "-1001234" || len(g.Recipients) != 0 || g.UpdatedAt.IsZero() {
		t.Fatalf("telegram = %+v", g)
	}
	if g := m["email"]; g.Enabled || !sameStrings(g.Recipients, []string{"a@x.io", "b@x.io"}) || g.SealedToken != "" || g.ChatID != "" {
		t.Fatalf("email = %+v", g)
	}

	stamp := m["telegram"].UpdatedAt
	before, existed, changed, err = s.SetAlertChannel(ctx, tg)
	if err != nil || !existed || changed || before.SealedToken != "v1:abc" {
		t.Fatalf("identical save = %+v existed=%v changed=%v err=%v", before, existed, changed, err)
	}
	if m, _ := s.AlertChannels(ctx); !m["telegram"].UpdatedAt.Equal(stamp) {
		t.Fatal("an identical save wrote the row")
	}

	tg.Enabled = false
	before, existed, changed, err = s.SetAlertChannel(ctx, tg)
	if err != nil || !existed || !changed || !before.Enabled {
		t.Fatalf("disable = %+v existed=%v changed=%v err=%v", before, existed, changed, err)
	}
	if m, _ := s.AlertChannels(ctx); m["telegram"].Enabled || m["telegram"].SealedToken != "v1:abc" {
		t.Fatalf("after disable = %+v", m["telegram"])
	}
	if _, _, _, err := s.SetAlertChannel(ctx, AlertChannel{Kind: "sms"}); err == nil {
		t.Fatal("kind sms was stored")
	}
}

func TestIncidentsOpenCloseCascade(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	wsID, shopID, _ := seedApps(t, s)

	first := time.Date(2026, 11, 1, 10, 0, 0, 0, time.UTC)
	projKey := fmt.Sprintf("project:%d:error", shopID)
	if err := s.OpenIncident(ctx, Incident{Key: "server:cpu", OpenedAt: first, ActivityID: 11}); err != nil {
		t.Fatal(err)
	}
	if err := s.OpenIncident(ctx, Incident{Key: projKey, ProjectID: shopID, OpenedAt: first, ActivityID: 12}); err != nil {
		t.Fatal(err)
	}
	ins, err := s.OpenIncidents(ctx)
	if err != nil || len(ins) != 2 {
		t.Fatalf("OpenIncidents = %+v, %v", ins, err)
	}
	byKey := map[string]Incident{}
	for _, in := range ins {
		byKey[in.Key] = in
	}
	if c := byKey["server:cpu"]; c.ProjectID != 0 || !c.OpenedAt.Equal(first) || c.ActivityID != 11 {
		t.Fatalf("server:cpu = %+v", c)
	}
	if p := byKey[projKey]; p.ProjectID != shopID || p.ActivityID != 12 {
		t.Fatalf("project incident = %+v", p)
	}

	// An existing key is kept as it was.
	if err := s.OpenIncident(ctx, Incident{Key: "server:cpu", OpenedAt: first.Add(time.Hour), ActivityID: 99}); err != nil {
		t.Fatal(err)
	}
	ins, _ = s.OpenIncidents(ctx)
	for _, in := range ins {
		if in.Key == "server:cpu" && (!in.OpenedAt.Equal(first) || in.ActivityID != 11) {
			t.Fatalf("reopened incident changed: %+v", in)
		}
	}

	if err := s.CloseIncident(ctx, "server:cpu"); err != nil {
		t.Fatal(err)
	}
	if err := s.CloseIncident(ctx, "server:cpu"); err != nil {
		t.Fatalf("closing a closed incident = %v, want nil", err)
	}
	if ins, _ := s.OpenIncidents(ctx); len(ins) != 1 || ins[0].Key != projKey {
		t.Fatalf("after close = %+v", ins)
	}

	// The project's rows go with the workspace.
	if _, err := s.RemoveWorkspace(ctx, wsID); err != nil {
		t.Fatal(err)
	}
	if ins, err := s.OpenIncidents(ctx); err != nil || len(ins) != 0 {
		t.Fatalf("after workspace removal = %+v, %v", ins, err)
	}
	// An incident for a project that does not exist is refused by the FK.
	if err := s.OpenIncident(ctx, Incident{Key: "project:999:error", ProjectID: 999, OpenedAt: first}); err == nil {
		t.Fatalf("incident of an unknown project: %v", err)
	}
}
