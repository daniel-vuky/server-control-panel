//go:build !dev

package drive

import (
	"testing"

	"github.com/daniel-vuky/server-control-panel/internal/config"
)

// A release build is Google, even without Google values: the fake Drive must
// never reach a release binary.
func TestReleaseClientIsGoogle(t *testing.T) {
	for _, cfg := range []config.Config{{}, {GoogleClientID: "id", GoogleClientSecret: "secret"}} {
		if _, ok := NewClient(cfg, t.TempDir()).(*Google); !ok {
			t.Fatalf("NewClient(%+v) is %T, want *Google", cfg, NewClient(cfg, t.TempDir()))
		}
	}
}
