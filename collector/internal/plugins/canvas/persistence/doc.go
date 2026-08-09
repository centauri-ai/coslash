// Package persistence owns revisioned server-backed Canvas state.
//
// Canvas workspace state used to live in browser localStorage keyed by a bare
// session ID. That state is now stored beneath the coSlash private home so it
// survives browser and collector restarts, is keyed by the full {agent,id}
// identity, and can be imported by the legacy migration task.
//
// The store treats workspace state as opaque JSON. The frozen
// contracts.WorkspaceDocument envelope owns revision, identity, and timestamp;
// the consuming Canvas package owns and validates the state schema. Bounds that
// require reading inside the state, such as pruning individual checkpoints or
// cache entries, therefore belong to that consuming package. This package
// enforces only envelope-level bounds: per-document size and total document
// count.
package persistence
