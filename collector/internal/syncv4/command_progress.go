package syncv4

import "github.com/centauri-ai/coslash/collector/internal/hubclient"

func (r *Runner) setRetryProgress(sessionID, stage string, done, total int64) error {
	for commandID, id := range r.retryCommands {
		if id != sessionID {
			continue
		}
		if err := r.Queue.SetCommandProgress(commandID, hubclient.V4CommandProgress{
			Stage: stage, BytesDone: done, BytesTotal: total,
		}); err != nil {
			return err
		}
	}
	return nil
}

func manifestBytes(manifest *hubclient.V4Manifest) int64 {
	if manifest == nil {
		return 0
	}
	var total int64
	for _, artifact := range manifest.Artifacts {
		total += artifact.Bytes
	}
	return total
}
