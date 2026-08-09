// Package runfs provides bounded filesystem access beneath a canonical root,
// durable atomic replacement, and append-only JSONL event logs. It refuses
// traversal and symlinks below the root and exposes no recursive deletion API.
package runfs
