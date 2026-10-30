package control

import (
	"log/slog"

	"github.com/daniel-vuky/server-control-panel/internal/scan"
	"github.com/daniel-vuky/server-control-panel/internal/store"
)

// New returns a Controller and the Hub that observes it, wired both ways: the
// Hub overlays the Controller's in-flight statuses, and the Controller reuses
// the Hub's recent container list and tells it about every lock change.
func New(st *store.Store, cmd scan.Commander, log *slog.Logger) (*Controller, *Hub) {
	ctl := NewController(st, cmd, log)
	h := newHub(st, cmd, ctl, log)
	ctl.setObserver(h)
	return ctl, h
}
