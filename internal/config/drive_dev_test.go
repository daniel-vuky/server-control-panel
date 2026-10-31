//go:build dev

package config

import "testing"

func TestDriveReason(t *testing.T) {
	key := testKey()
	cases := []struct {
		name string
		cfg  Config
		want string
	}{
		{"nothing", Config{}, "PANEL_SECRET_KEY is not set"},
		{"no url", Config{SecretKey: key}, "PANEL_PUBLIC_URL is not set"},
		{"fake drive needs no google", Config{SecretKey: key, PublicURL: "http://localhost:5173"}, ""},
		{"fake drive needs no google secret", Config{SecretKey: key, PublicURL: "http://localhost:5173", GoogleClientID: "i"}, ""},
	}
	for _, tc := range cases {
		if got := tc.cfg.DriveReason(); got != tc.want {
			t.Errorf("%s: DriveReason() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDevBackupMinFree(t *testing.T) {
	cfg, err := envLoad(map[string]string{"PANEL_DEV_BACKUP_MIN_FREE": "123456"})
	if err != nil || cfg.DevBackupMinFree != 123456 {
		t.Errorf("DevBackupMinFree = %d, err = %v; want 123456, nil", cfg.DevBackupMinFree, err)
	}
	cfg, err = envLoad(nil)
	if err != nil || cfg.DevBackupMinFree != 0 {
		t.Errorf("unset: DevBackupMinFree = %d, err = %v; want 0, nil", cfg.DevBackupMinFree, err)
	}
	for _, bad := range []string{"junk", "-1", "1.5"} {
		if _, err := envLoad(map[string]string{"PANEL_DEV_BACKUP_MIN_FREE": bad}); err == nil {
			t.Errorf("PANEL_DEV_BACKUP_MIN_FREE=%q: want an error", bad)
		}
	}
}
