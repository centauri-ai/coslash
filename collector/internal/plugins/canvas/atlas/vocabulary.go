package atlas

import (
	"regexp"
	"slices"
	"strings"
)

// The verified launch vocabularies and the validators that gate them.
//
// Every value here is an allowlist, not a suggestion list, because neither CLI
// fails fast on a bad value: Claude accepts an unknown --model and only errors
// at the API call, and an unknown --effort is a warning that silently uses the
// default. A board carrying a wrong value would therefore run, and run
// differently than it reads.

// Vendor is the agent CLI a seat drives.
type Vendor string

const (
	VendorClaude Vendor = "claude"
	VendorCodex  Vendor = "codex"
)

// ComponentID names one stage of the run pipeline.
//
// The editable board stores membership and order as data (components + edges),
// but the run model still keys state by these six ids, because the executable
// chain remains Intake → Plan → Build → Verify → Review → Publish.
type ComponentID string

const (
	ComponentIntake  ComponentID = "intake"
	ComponentPlan    ComponentID = "plan"
	ComponentBuild   ComponentID = "build"
	ComponentVerify  ComponentID = "verify"
	ComponentReview  ComponentID = "review"
	ComponentPublish ComponentID = "publish"
)

// ComponentIDs is both the membership rule and the pipeline order for run
// state. It is stated once and read from here rather than restated.
var ComponentIDs = []ComponentID{
	ComponentIntake, ComponentPlan, ComponentBuild,
	ComponentVerify, ComponentReview, ComponentPublish,
}

// SeatComponentIDs are the components that run a model, and therefore the only
// legal values of a board seat's legacyRole. The rest are deterministic: Intake
// renders a template, Verify runs argv, Publish drives git and gh.
var SeatComponentIDs = []ComponentID{ComponentPlan, ComponentBuild, ComponentReview}

// HasSeat reports whether a component runs a model.
func HasSeat(id ComponentID) bool { return slices.Contains(SeatComponentIDs, id) }

// ValidComponentID reports membership in the run pipeline.
func ValidComponentID(id ComponentID) bool { return slices.Contains(ComponentIDs, id) }

// ValidLegacyRole reports whether a board seat may claim this run role.
func ValidLegacyRole(id ComponentID) bool { return HasSeat(id) }

// Launch vocabularies.
var (
	ClaudeModels  = []string{"opus", "sonnet", "haiku", "fable"}
	ClaudeEfforts = []string{"low", "medium", "high", "xhigh", "max"}
	// ClaudePermissions is a deliberate SUBSET of Claude's --permission-mode
	// choices. The CLI also accepts manual, plan, auto, and dontAsk; a board may
	// only select a value that can actually finish an unattended turn that must
	// write an artifact. `manual` prompts, so it hangs — the exact failure the
	// exit protocol exists to prevent, arriving as a wasted timeout. `plan`
	// cannot write files, so every component fails missing_output, including
	// Review, which must produce review.json. `auto` and `dontAsk` have
	// undocumented semantics and are not shipped on a guess.
	//
	// bypassPermissions is offered but is the loosest setting, and Claude has no
	// sandbox, so it grants a full shell as the developer.
	ClaudePermissions = []string{"acceptEdits", "bypassPermissions"}

	CodexModels  = []string{"gpt-5.6-sol", "gpt-5.6-terra", "gpt-5.6-luna"}
	CodexEfforts = []string{"low", "medium", "high", "xhigh", "max", "ultra"}
	// CodexSandboxes omits danger-full-access deliberately. workspace-write is
	// the one OS-enforced boundary Atlas has, and with git-clone or plain-folder
	// run roots the agent can still do everything legitimate inside it, so
	// letting a board switch it off would discard the only real containment for
	// no capability gained.
	CodexSandboxes = []string{"read-only", "workspace-write"}
)

// ModelsFor returns the models a vendor accepts.
func ModelsFor(vendor Vendor) []string {
	if vendor == VendorCodex {
		return CodexModels
	}
	return ClaudeModels
}

// EffortsFor returns the efforts a model accepts. `ultra` is offered only by
// the models that support it, so a board can never carry an effort the chosen
// model would silently ignore.
func EffortsFor(vendor Vendor, model string) []string {
	if vendor != VendorCodex {
		return ClaudeEfforts
	}
	if model == "gpt-5.6-sol" || model == "gpt-5.6-terra" {
		return CodexEfforts
	}
	filtered := make([]string, 0, len(CodexEfforts))
	for _, effort := range CodexEfforts {
		if effort != "ultra" {
			filtered = append(filtered, effort)
		}
	}
	return filtered
}

// PermissionsFor returns the permission or sandbox values a vendor accepts.
func PermissionsFor(vendor Vendor) []string {
	if vendor == VendorCodex {
		return CodexSandboxes
	}
	return ClaudePermissions
}

// ValidVendor reports membership in the supported vendor set.
func ValidVendor(vendor Vendor) bool {
	return vendor == VendorClaude || vendor == VendorCodex
}

// Bounds on a board's executable content. A board is configuration, not
// content; a document past these limits is a sign of an injected payload rather
// than a graph someone drew.
const (
	MaxChecks            = 12
	MaxArgvTokens        = 40
	MaxArgvTokenLength   = 400
	MaxComponents        = 24
	MaxCommitteeWorkers  = 5
	MaxRequiredOutputs   = 8
	MaxPromptLength      = 8_000
	MaxInstructionsBytes = 8_000
	MaxSystemPromptBytes = 8_000
	MaxTitleLength       = 80
	MaxIDLength          = 128
)

// CheckCommands are the programs a Verify check may run.
//
// Checks are exec'd as argv with no shell, so metacharacters are inert — but
// that protection is worthless if argv[0] is itself a shell. A board is a JSON
// file that can be committed, shared, or arrive in a pull request, so
// ["sh","-c","curl …|sh"] would make opening someone's board a remote-code
// execution vector.
//
// Notably absent: sh, bash, zsh, env, xargs, eval, and npx — npx resolves and
// executes packages from the network, which is the same hole wearing a hat.
//
// This does not pretend to sandbox anything: `npm run typecheck` already runs
// whatever the repository's package.json says. The boundary it draws is
// narrower and still worth having — a board cannot introduce an executable the
// repository does not already invoke.
var CheckCommands = []string{
	"npm", "pnpm", "yarn", "bun", "node", "deno", "make", "just", "cargo", "go",
	"python3", "pytest", "ruff", "tsc", "vitest", "jest", "eslint", "prettier",
	"mvn", "gradle", "dotnet", "swift", "bundle", "rake", "rspec", "phpunit",
	"composer",
}

var (
	// A check name is only ever shown to a human and used as a log filename
	// stem, so it is restricted to characters that are safe in both roles.
	checkNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)
	// Git allows most bytes in a ref, but a base branch also reaches argv and a
	// pull-request query. A leading dash could be read as a flag.
	baseBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	// Required outputs are joined onto a run root, so they are single path
	// segments with no separator and no traversal.
	outputNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// Identifiers reach the filesystem as one path component.
	pathComponentPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
	// A run id is timestamp-prefixed and case-folded: macOS is case-insensitive,
	// and a run directory differing only by case would collide on disk while
	// looking distinct in the log.
	runIDPattern = regexp.MustCompile(`^run-[0-9]{8}t[0-9]{6}-[0-9a-f]{8}$`)
	// A UUID is the only shape Claude's --session-id accepts.
	sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	// Graph node and edge identifiers are used as map and DOM keys.
	graphIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$`)
)

// ValidCheckName reports whether a check name is safe to display and to use as
// a log filename stem.
func ValidCheckName(value string) bool { return checkNamePattern.MatchString(value) }

// ValidBaseBranch reports whether a branch name is safe to pass to git and to a
// pull-request query.
func ValidBaseBranch(value string) bool {
	return baseBranchPattern.MatchString(value) && !strings.Contains(value, "..")
}

// ValidRequiredOutput reports whether an artifact name is a safe single path
// segment beneath a run root.
func ValidRequiredOutput(value string) bool {
	return outputNamePattern.MatchString(value) && !strings.Contains(value, "..")
}

// ValidArgvToken reports whether a token is safe to exec directly. Shell
// metacharacters are harmless because there is no shell; control characters are
// not, because they can truncate or confuse the exec boundary and the logs.
func ValidArgvToken(value string) bool {
	if value == "" || value != strings.TrimSpace(value) || len(value) > MaxArgvTokenLength {
		return false
	}
	return strings.IndexFunc(value, func(r rune) bool { return r < 0x20 || r == 0x7f }) < 0
}

// ValidCheckCommand reports membership in the allowed program list.
func ValidCheckCommand(value string) bool { return slices.Contains(CheckCommands, value) }

// ValidProjectID reports whether a project identifier is a safe path component.
func ValidProjectID(value string) bool { return validPathComponent(value) }

// ValidBoardID reports whether a board identifier is a safe path component.
func ValidBoardID(value string) bool { return validPathComponent(value) }

// ValidRunID reports whether a run identifier matches the allocated shape.
func ValidRunID(value string) bool { return runIDPattern.MatchString(value) }

// ValidSessionID reports whether a value is a session identifier Claude accepts.
// Validating here turns a confusing CLI exit into a clear refusal.
func ValidSessionID(value string) bool { return sessionIDPattern.MatchString(value) }

// ValidGraphID reports whether a component, seat, or edge identifier is usable
// as a stable key.
func ValidGraphID(value string) bool { return graphIDPattern.MatchString(value) }

func validPathComponent(value string) bool {
	return pathComponentPattern.MatchString(value) && value != "." && value != ".."
}
