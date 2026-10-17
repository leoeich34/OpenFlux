package orchestrator

import (
	"time"

	"github.com/p1neappleXpress/OpenFlux/access"
)

// Reconcile stops every launched exit whose key is unknown, revoked, or
// expired, so a daemon restart does not leave orphaned containers holding
// call slots. It returns the key ids it stopped. An exit whose key is still
// valid is left running.
func Reconcile(store *access.Store, launcher ExitLauncher, now time.Time) ([]string, error) {
	handles, err := launcher.List()
	if err != nil {
		return nil, err
	}
	var stopped []string
	for _, h := range handles {
		rec, ok := store.Get(h.KeyID)
		if ok && rec.Valid(now) {
			continue
		}
		if err := launcher.Stop(h); err != nil {
			return stopped, err
		}
		stopped = append(stopped, h.KeyID)
	}
	return stopped, nil
}
