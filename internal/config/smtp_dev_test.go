//go:build dev

package config

import "testing"

func TestDevRealAlerts(t *testing.T) {
	cfg, err := envLoad(map[string]string{"PANEL_DEV_REAL_ALERTS": "1"})
	if err != nil || !cfg.DevRealAlerts {
		t.Errorf("DevRealAlerts = %v, err = %v; want true, nil", cfg.DevRealAlerts, err)
	}
	for _, v := range []string{"", "0", "true", "yes"} {
		cfg, err := envLoad(map[string]string{"PANEL_DEV_REAL_ALERTS": v})
		if err != nil || cfg.DevRealAlerts {
			t.Errorf("PANEL_DEV_REAL_ALERTS=%q: DevRealAlerts = %v, err = %v; want false, nil", v, cfg.DevRealAlerts, err)
		}
	}
}
