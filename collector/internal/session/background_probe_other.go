//go:build !darwin

package session

func backgroundFilesystemProbeAllowed(path string) bool { return path != "" }
