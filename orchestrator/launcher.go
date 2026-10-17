// Package orchestrator manages the exit nodes behind OpenFlux access keys:
// it launches an exit bound to each issued key's call, keeps a warm set of
// pre-started exits, and stops exits whose keys are gone. Launching sits
// behind ExitLauncher so the logic runs against a fake in tests and against
// Docker in production. See
// docs/superpowers/plans/2026-10-17-pool-orchestrator.md.
package orchestrator

// ExitSpec is everything an exit node needs to serve one key.
type ExitSpec struct {
	KeyID     string // the access key this exit enforces (--license-key-id)
	CallLink  string // the Telemost call it joins (--url)
	Secret    string // the per-key encryption secret
	LicenseWS string // the control hub it reports to (--license-server)
}

// ExitHandle identifies a launched exit.
type ExitHandle struct {
	ID    string // opaque container/process id
	KeyID string // the key the exit serves, for reconciliation
}

// ExitLauncher starts and stops exit nodes.
type ExitLauncher interface {
	Launch(ExitSpec) (ExitHandle, error)
	Stop(ExitHandle) error
	List() ([]ExitHandle, error)
}
