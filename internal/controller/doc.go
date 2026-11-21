// Package controller is the scheduler behind `ard serve`. It runs one
// goroutine per Loop, reloads configuration before every tick, applies the
// kill switch to loops that start new work, and drains in-flight work on
// shutdown. It knows nothing about what a loop does: scanners, triage,
// workers, reviewers, the merger and the Reaper each implement Loop in the
// issues that own them.
//
// Loops receive their long-lived dependencies (GitHub reader, effects, state,
// pool) through their own constructors; the controller passes only per-tick
// values in Env. The leader lock lives here (AcquireLeader) but is taken by
// the serve handler, which owns dependency wiring.
package controller
