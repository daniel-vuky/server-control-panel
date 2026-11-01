//go:build !dev

package config

// devBuild is false in a release build: Google credentials are required for Drive.
const devBuild = false

// devBackupMinFree is always 0 in a release build; PANEL_DEV_BACKUP_MIN_FREE is
// never read.
func devBackupMinFree(func(string) string) (int64, error) { return 0, nil }

// devRealAlerts is always false in a release build; PANEL_DEV_REAL_ALERTS is
// never read.
func devRealAlerts(func(string) string) bool { return false }
