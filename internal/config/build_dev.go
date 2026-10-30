//go:build dev

package config

import (
	"fmt"
	"strconv"
	"strings"
)

// devBuild is true in a dev build. The fake Drive stands in for Google there, so
// empty Google values are allowed.
const devBuild = true

// devBackupMinFree reads PANEL_DEV_BACKUP_MIN_FREE (bytes), which a dev build uses
// to force the "not enough space" refusal. A release build never reads it.
func devBackupMinFree(getenv func(string) string) (int64, error) {
	v := strings.TrimSpace(getenv("PANEL_DEV_BACKUP_MIN_FREE"))
	if v == "" {
		return 0, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < 0 {
		return 0, fmt.Errorf("PANEL_DEV_BACKUP_MIN_FREE %q: must be a number of bytes", v)
	}
	return n, nil
}
