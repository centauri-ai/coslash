package remote

// Windows does not support flushing an open directory with FlushFileBuffers.
// The temporary file itself is flushed before the same-directory atomic rename,
// so there is no supported directory-handle durability operation left to run.
func syncMetadataDirectory(string) error {
	return nil
}
