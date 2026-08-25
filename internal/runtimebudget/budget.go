// Package runtimebudget defines the shared wall-clock limits for one verified
// restart transaction. Keeping the limits here prevents the continuation lease
// from drifting below the work that the Helper may still be safely cleaning up.
package runtimebudget

import "time"

const (
	RestartStartupDelay      = 2 * time.Second
	RestartWorkerTimeout     = 4 * time.Minute
	EngineRollbackTimeout    = 20 * time.Second
	AdapterCleanupTimeout    = 45 * time.Second
	TerminalStateWriteBudget = 15 * time.Second
	RestartRunningLease      = RestartStartupDelay + RestartWorkerTimeout + EngineRollbackTimeout + AdapterCleanupTimeout + TerminalStateWriteBudget
)
