//go:build !dev

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
		{"no google", Config{SecretKey: key, PublicURL: "https://x"}, "GOOGLE_CLIENT_ID is not set"},
		{"no google secret", Config{SecretKey: key, PublicURL: "https://x", GoogleClientID: "i"}, "GOOGLE_CLIENT_SECRET is not set"},
		{"key first", Config{GoogleClientID: "i", GoogleClientSecret: "s"}, "PANEL_SECRET_KEY is not set"},
		{"ready", Config{SecretKey: key, PublicURL: "https://x", GoogleClientID: "i", GoogleClientSecret: "s"}, ""},
	}
	for _, tc := range cases {
		if got := tc.cfg.DriveReason(); got != tc.want {
			t.Errorf("%s: DriveReason() = %q, want %q", tc.name, got, tc.want)
		}
	}
}

func TestDevBackupMinFreeIgnoredInRelease(t *testing.T) {
	cfg, err := envLoad(map[string]string{"PANEL_DEV_BACKUP_MIN_FREE": "123"})
	if err != nil || cfg.DevBackupMinFree != 0 {
		t.Errorf("DevBackupMinFree = %d, err = %v; want 0, nil", cfg.DevBackupMinFree, err)
	}
	if _, err := envLoad(map[string]string{"PANEL_DEV_BACKUP_MIN_FREE": "junk"}); err != nil {
		t.Errorf("a release build must not read PANEL_DEV_BACKUP_MIN_FREE, got %v", err)
	}
}
