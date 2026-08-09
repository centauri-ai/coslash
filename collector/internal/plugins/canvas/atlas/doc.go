// Package atlas owns the Atlas model, store, controller, and HTTP API.
//
// This file set is the model half: the versioned board graph, its v1-to-v2
// migration boundary, the policy gate, the durable board store, the run model,
// its pure reducer, and the event-sourced run store. Controller, subprocess,
// HTTP, and frontend code live elsewhere and depend on these types.
//
// Two rules shape everything here. A board is untrusted input — it is a JSON
// file that can be hand-edited, committed, shared, or arrive in a pull request —
// so Normalize repairs a document into something coherent and AssertPolicy
// refuses one whose executable content is not allowed. And the disk, not the
// process, is the authority on a run: events.jsonl is the run, run.json is a
// materialized view, and Reduce is a pure total function between them.
package atlas
