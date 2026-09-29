package claude

import (
	"context"

	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// ParseFilesContext parses exactly the given local transcripts, which must
// be whole families, and applies the same per-vendor finalization as a full
// collection. Streamed discovery calls it per batch of families.
func ParseFilesContext(ctx context.Context, files []string) ([]*vendors.ParsedSession, error) {
	return parseFilesContext(ctx, files)
}
