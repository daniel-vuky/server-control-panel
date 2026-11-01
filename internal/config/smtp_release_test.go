//go:build !dev

package config

import "testing"

func TestDevRealAlertsIgnoredInRelease(t *testing.T) {
	cfg, err := envLoad(map[string]string{"PANEL_DEV_REAL_ALERTS": "1"})
	if err != nil || cfg.DevRealAlerts {
		t.Errorf("DevRealAlerts = %v, err = %v; want false, nil", cfg.DevRealAlerts, err)
	}
}
