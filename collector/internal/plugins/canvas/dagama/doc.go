// Package dagama owns the durable DaGama domain model, policy, and stores.
//
// The split this package is built around: events.jsonl IS the run, and run.json
// is a materialized view rebuildable from it at any time. Reduce is a pure total
// function from events to state, so deleting the view and replaying yields a
// byte-identical document — nothing derived is ever written into the log, and no
// clock is read during materialization.
//
// A board is untrusted input. It can be hand-edited, committed, shared, or
// arrive in a pull request, so Normalize repairs at the boundary, AssertPolicy
// refuses what repair could not legitimately fix, and the in-memory model stays
// strict with no escape hatch.
//
// Nothing here drives the pipeline. There are no HTTP handlers, no subprocesses,
// and no UI: the controller, runner, prompts, repair routing, reconciliation,
// takeover, and cancel belong to Task 12.
package dagama
