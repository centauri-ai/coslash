// Package hardening validates the assembled Canvas plugin rather than any one
// of its parts.
//
// Every other Canvas package tests its own behavior. What none of them can test
// is what happens when they are mounted together behind the real coSlash guard,
// which is where the boundaries that matter actually live: a route group is
// only as authenticated as the guard in front of it, and a store is only as
// scoped as the handler that resolves paths into it.
//
// The suite is deliberately adversarial. It sends the requests a hostile page
// in the user's browser would send — no token, a stolen origin, a traversal in
// a path segment, a body past the limit, a symlink out of the scope — and
// asserts the refusal, not the happy path. It also counts what the process
// holds before and after repeated lifecycles, because a leak is invisible in a
// single-run test and fatal in a long-lived collector.
//
// Product fixes do not belong here. A failure found by this package is routed
// to the package that owns the behavior; this one only proves it.
package hardening
