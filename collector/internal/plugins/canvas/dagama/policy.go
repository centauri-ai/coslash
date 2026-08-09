package dagama

import (
	"fmt"
	"path"
	"path/filepath"
	"slices"
	"strings"
)

// Normalize repairs a board once, at the boundary.
//
// Repairing is the right behaviour for someone editing a board in the UI, so a
// document that arrives with a missing or drifted value is pulled back to a
// legal one here. AssertPolicy then refuses whatever normalization could not
// legitimately repair: a value that is still invalid after this did not come
// from the UI, and silently downgrading it would hide that.
//
// Normalize never invents identity. A board with no ID stays without one and
// fails validation, because guessing an ID would let two different boards
// collide in the store.
func Normalize(board *Board) {
	if board.SchemaVersion == 0 {
		board.SchemaVersion = BoardSchemaVersion
	}
	board.Name = strings.TrimSpace(board.Name)
	board.ProjectPath = strings.TrimSpace(board.ProjectPath)
	// Steering is clamped rather than refused: it is prose an operator typed,
	// and truncating it loses the tail, where refusing would lose the board.
	board.Instructions = clampSteering(board.Instructions, MaxInstructionsChars)
	board.Components.Plan.Prompt = clampSteering(board.Components.Plan.Prompt, MaxPromptChars)
	board.Components.Build.Prompt = clampSteering(board.Components.Build.Prompt, MaxPromptChars)
	board.Components.Review.Prompt = clampSteering(board.Components.Review.Prompt, MaxPromptChars)

	normalizeSeat(&board.Components.Plan.Seat)
	normalizeSeat(&board.Components.Build.Seat)
	normalizeSeat(&board.Components.Review.Seat)

	board.Components.Verify.Checks = normalizeChecks(board.Components.Verify.Checks)
	board.Components.Publish.Publish.Base = strings.TrimSpace(board.Components.Publish.Publish.Base)
}

// clampSteering bounds operator prose to a rune count, never a byte slice: a
// byte cut would split a multi-byte character and put invalid UTF-8 into the
// document and then into an agent's prompt.
func clampSteering(value string, limit int) string {
	value = strings.TrimSpace(value)
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return string(runes[:limit])
}

func normalizeSeat(seat *Seat) {
	if seat.Vendor != VendorClaude && seat.Vendor != VendorCodex {
		seat.Vendor = VendorClaude
	}
	models := ModelsFor(seat.Vendor)
	if !slices.Contains(models, seat.Model) {
		seat.Model = models[0]
	}
	efforts := EffortsFor(seat.Vendor, seat.Model)
	if !slices.Contains(efforts, seat.Effort) {
		// The middle of the range, not the first: `low` on a repair round is the
		// difference between a fix and another failed instance.
		seat.Effort = "medium"
		if !slices.Contains(efforts, seat.Effort) {
			seat.Effort = efforts[0]
		}
	}
	permissions := PermissionsFor(seat.Vendor)
	if !slices.Contains(permissions, seat.Permission) {
		// The tightest legal value, never the loosest: an unreadable permission
		// must not silently widen what an unattended agent may do.
		seat.Permission = permissions[0]
	}
}

// normalizeChecks drops entries the UI could not have produced and trims the
// list to the bound. It does not repair an argv, because a partially repaired
// command is a different command.
func normalizeChecks(checks []Check) []Check {
	if len(checks) == 0 {
		return nil
	}
	kept := make([]Check, 0, len(checks))
	seen := make(map[string]bool, len(checks))
	for _, check := range checks {
		check.Name = strings.TrimSpace(check.Name)
		if check.Name == "" || len(check.Argv) == 0 || seen[check.Name] {
			continue
		}
		seen[check.Name] = true
		kept = append(kept, check)
		if len(kept) == MaxChecks {
			break
		}
	}
	if len(kept) == 0 {
		return nil
	}
	return kept
}

// AssertPolicy refuses a board's executable content.
//
// Two escalation paths need no shell metacharacter at all:
//
//	{"permission": "bypassPermissions"}       reaches --permission-mode directly
//	{"argv": ["sh", "-c", "curl … | sh"]}     makes "argv, not a shell string" moot
//
// This gate therefore rejects rather than repairs. It is called before a board
// is written and again before a run reads one; the second call is the one that
// matters, since a file can change on disk between the two.
func AssertPolicy(board *Board) error {
	if board == nil {
		return policyError("board", "the board is missing")
	}
	if board.SchemaVersion == 0 || board.SchemaVersion > BoardSchemaVersion {
		return newError(CodeSchemaVersion, "the board schema version is not supported")
	}
	if !ValidBoardID(board.ID) {
		return &Error{Code: CodeInvalidBoardID, Message: "the board identifier is not valid", Field: "id"}
	}
	if !ValidProjectID(board.ProjectID) {
		return &Error{
			Code: CodeInvalidProjectID, Message: "the project identifier is not valid", Field: "projectId",
		}
	}
	if board.Name == "" || len(board.Name) > 200 {
		return policyError("name", "the board name must be between 1 and 200 characters")
	}
	if err := assertProjectPath(board.ProjectPath); err != nil {
		return err
	}

	for _, entry := range []struct {
		id   ComponentID
		seat Seat
	}{
		{ComponentPlan, board.Components.Plan.Seat},
		{ComponentBuild, board.Components.Build.Seat},
		{ComponentReview, board.Components.Review.Seat},
	} {
		if err := assertSeat(entry.id, entry.seat); err != nil {
			return err
		}
	}
	if err := assertChecks(board.Components.Verify.Checks); err != nil {
		return err
	}
	return assertPublish(board.Components.Publish.Publish)
}

func assertSeat(id ComponentID, seat Seat) error {
	field := "components." + string(id) + ".seat"
	if seat.Vendor != VendorClaude && seat.Vendor != VendorCodex {
		return policyError(field+".vendor", "the seat vendor is not recognized")
	}
	if !slices.Contains(ModelsFor(seat.Vendor), seat.Model) {
		return policyError(field+".model", "the model is not allowed for this vendor")
	}
	if !slices.Contains(EffortsFor(seat.Vendor, seat.Model), seat.Effort) {
		return policyError(field+".effort", "the effort is not allowed for this model")
	}
	// The escalation that carries no metacharacter: a permission string is
	// passed straight to --permission-mode or --sandbox, so quoting changes
	// nothing and only an allowlist helps.
	if !slices.Contains(PermissionsFor(seat.Vendor), seat.Permission) {
		return policyError(field+".permission", "the permission is not allowed for this vendor")
	}
	return nil
}

func assertChecks(checks []Check) error {
	if len(checks) > MaxChecks {
		return policyError("components.verify.checks",
			fmt.Sprintf("at most %d checks are allowed", MaxChecks))
	}
	seen := make(map[string]bool, len(checks))
	for index, check := range checks {
		field := fmt.Sprintf("components.verify.checks[%d]", index)
		if !ValidCheckName(check.Name) {
			return policyError(field+".name", "the check name is not valid")
		}
		if seen[check.Name] {
			// Two checks with one name would overwrite each other's log.
			return policyError(field+".name", "the check name is repeated")
		}
		seen[check.Name] = true
		if len(check.Argv) == 0 {
			return policyError(field+".argv", "argv must be a non-empty list")
		}
		if len(check.Argv) > MaxArgvTokens {
			return policyError(field+".argv",
				fmt.Sprintf("argv may hold at most %d tokens", MaxArgvTokens))
		}
		if !ValidCheckCommand(check.Argv[0]) {
			return policyError(field+".argv[0]", "the command is not an allowed check command")
		}
		for tokenIndex, token := range check.Argv {
			if !ValidArgvToken(token) {
				return policyError(fmt.Sprintf("%s.argv[%d]", field, tokenIndex),
					"the argv token is empty, oversized, or contains a control character")
			}
		}
	}
	return nil
}

// assertProjectPath bounds the one board field that names a filesystem
// location. It is never joined to a scoped root by this package, but a run
// preflight will hand it to Git, so a relative path or a traversal must not
// survive validation.
func assertProjectPath(projectPath string) error {
	if projectPath == "" {
		return policyError("projectPath", "the project path is required")
	}
	if !filepath.IsAbs(projectPath) {
		return policyError("projectPath", "the project path must be absolute")
	}
	if strings.ContainsAny(projectPath, "\x00\n\r") {
		return policyError("projectPath", "the project path contains control characters")
	}
	if projectPath != filepath.Clean(projectPath) {
		return policyError("projectPath", "the project path is not canonical")
	}
	return nil
}

// ArtifactBlobPrefix is the only location a DaGama artifact reference may point
// to. A record naming anything else is either corrupt or a cross-canvas
// reference — an Atlas run's blob reached through a relative path — and either
// way this run must not attest it.
const ArtifactBlobPrefix = ".fleetlog/run/artifacts/blobs/"

// AssertArtifactReference refuses an artifact record that does not describe this
// run's own promoted blob.
func AssertArtifactReference(artifact ArtifactRecord) error {
	if artifact.ArtifactID == "" || artifact.Name == "" || artifact.Kind == "" {
		return policyError("artifact", "the artifact record is incomplete")
	}
	if !ValidSha256(artifact.Sha256) {
		return policyError("artifact.sha256", "the artifact digest is not a SHA-256 value")
	}
	if artifact.Bytes <= 0 {
		return policyError("artifact.bytes", "the artifact size must be positive")
	}
	cleaned := path.Clean(artifact.Path)
	if cleaned != artifact.Path || !strings.HasPrefix(cleaned, ArtifactBlobPrefix) {
		return policyError("artifact.path", "the artifact path is not this run's promoted blob")
	}
	if !ValidComponentID(artifact.Producer.ComponentID) {
		return policyError("artifact.producer.componentId", "the artifact producer is not a pipeline component")
	}
	return nil
}

func assertPublish(publish PublishConfig) error {
	if publish.Base != "" && !ValidBaseBranch(publish.Base) {
		return policyError("components.publish.publish.base", "the base branch is not valid")
	}
	return nil
}
