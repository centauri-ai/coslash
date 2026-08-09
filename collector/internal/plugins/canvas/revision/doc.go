// Package revision owns isolated Git revisions and immutable revision identity.
//
// Two rules govern this package, and both were arrived at by trying to break
// the original design rather than by reasoning about it:
//
//  1. A disposable run root is a CLONE, not a linked worktree. A linked
//     worktree shares config, hooks/, refs/, and objects/ with the user's
//     repository, so an agent inside one can set core.hooksPath (code execution
//     in the user's next commit), set credential.helper (exfiltrate a token),
//     or force-remove the user's checkout. "We never write to the user's
//     worktree" is the wrong invariant; owning a separate object store is the
//     right one. Atlas additionally supports attaching in place to a dedicated
//     long-lived work branch, and such a root is never deleted.
//
//  2. Config inside a run root is agent-writable, so this package hardens its
//     own git calls on every invocation. Without core.hooksPath and
//     core.attributesFile, `git add -A` runs agent-authored hooks and clean
//     filters, and evidence capture becomes an execution vector.
//
// Nothing here encodes DaGama or Atlas scheduling policy. The package captures
// and anchors revisions; deciding when to capture one belongs to a controller.
package revision
