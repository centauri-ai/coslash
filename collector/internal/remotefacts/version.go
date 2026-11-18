package remotefacts

import (
	"github.com/centauri-ai/coslash/collector/internal/vendors"
	"github.com/centauri-ai/coslash/collector/internal/vendors/claude"
	"github.com/centauri-ai/coslash/collector/internal/vendors/codex"
)

// ParserVersion is the parser version that remote facts of vendor carry. It
// joins the shared parser version with the vendor's own, so a bump of either
// one reparses cached remote families. The helper and the Mac both stamp and
// compare this value.
func ParserVersion(vendor string) string {
	switch vendor {
	case vendors.AgentClaude:
		return vendors.ParserVersion + "+" + claude.ParserVersion
	case vendors.AgentCodex:
		return vendors.ParserVersion + "+" + codex.ParserVersion
	}
	return vendors.ParserVersion
}
