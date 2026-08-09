package dagama

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
// default. A board that carries a wrong value would therefore run, and run
// differently than it reads.

// Vendor is the agent CLI a seat drives.
type Vendor string

const (
	VendorClaude Vendor = "claude"
	VendorCodex  Vendor = "codex"
)

// ComponentID names one stage of the fixed DaGama pipeline.
type ComponentID string

const (
	ComponentIntake  ComponentID = "intake"
	ComponentPlan    ComponentID = "plan"
	ComponentBuild   ComponentID = "build"
	ComponentVerify  ComponentID = "verify"
	ComponentReview  ComponentID = "review"
	ComponentPublish ComponentID = "publish"
)

// ComponentIDs is both the membership rule and the pipeline order. The shape is
// the product, so it is stated once and read from here rather than restated.
var ComponentIDs = []ComponentID{
	ComponentIntake, ComponentPlan, ComponentBuild,
	ComponentVerify, ComponentReview, ComponentPublish,
}

// SeatComponentIDs are the components that run a model. The rest are
// deterministic: Intake renders a template, Verify runs argv, Publish drives
// git and gh.
var SeatComponentIDs = []ComponentID{ComponentPlan, ComponentBuild, ComponentReview}

// HasSeat reports whether a component runs a model.
func HasSeat(id ComponentID) bool { return slices.Contains(SeatComponentIDs, id) }

// ValidComponentID reports membership in the fixed pipeline.
func ValidComponentID(id ComponentID) bool { return slices.Contains(ComponentIDs, id) }

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
	// the one OS-enforced boundary DaGama has, and with the run root being a
	// clone the agent can still do everything legitimate inside it, so letting a
	// board switch it off would discard the only real containment for no
	// capability gained.
	CodexSandboxes = []string{"read-only", "workspace-write"}
)

// ModelsFor returns the models a vendor accepts.
func ModelsFor(vendor Vendor) []string {
	if vendor == VendorCodex {
		return CodexModels
	}
	return ClaudeModels
}

// EffortsFor returns the efforts a model accepts. `ultra` is offered only by the
// models that support it, so a board can never carry an effort the chosen model
// would silently ignore.
func EffortsFor(vendor Vendor, model string) []string {
	if vendor == VendorClaude {
		return ClaudeEfforts
	}
	if model == "gpt-5.6-sol" || model == "gpt-5.6-terra" {
		return CodexEfforts
	}
	return slices.DeleteFunc(slices.Clone(CodexEfforts), func(effort string) bool {
		return effort == "ultra"
	})
}

// PermissionsFor returns the permission or sandbox values a vendor accepts.
func PermissionsFor(vendor Vendor) []string {
	if vendor == VendorCodex {
		return CodexSandboxes
	}
	return ClaudePermissions
}

// Verify check bounds.
const (
	MaxChecks         = 12
	MaxArgvTokens     = 40
	MaxArgvTokenChars = 400
)

// CheckCommands are the programs a Verify check may run.
//
// Checks are exec'd as argv with no shell, so metacharacters are inert — but
// that protection is worthless if argv[0] is itself a shell. A board is a JSON
// file that can be committed, shared, or arrive in a pull request, so
// ["sh","-c","curl …|sh"] would make opening someone's board a remote code
// execution vector.
//
// Notably absent: sh, bash, zsh, env, xargs, eval, and npx — npx resolves and
// executes packages from the network, which is the same hole wearing a hat.
var CheckCommands = []string{
	"npm", "pnpm", "yarn", "bun", "node", "deno",
	"make", "just", "cargo", "go", "python3", "pytest", "ruff", "tsc",
	"vitest", "jest", "eslint", "prettier",
	"mvn", "gradle", "dotnet", "swift",
	"bundle", "rake", "rspec", "phpunit", "composer",
}

var (
	// checkNamePattern bounds a value that is only ever shown to a human and
	// used as a log filename stem, so it is safe in both roles.
	checkNamePattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9 ._-]{0,39}$`)
	// baseBranchPattern bounds a branch that also reaches argv and a pull
	// request query. A leading dash could be read as a flag and `..` is a
	// traversal.
	baseBranchPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._/-]{0,127}$`)
	// runIDPattern is the generated run identity: run-<yyyymmdd>t<hhmmss>-<8 hex>.
	runIDPattern = regexp.MustCompile(`^run-[0-9]{8}t[0-9]{6}-[0-9a-f]{8}$`)
	// boardIDPattern and projectIDPattern bound values that become path
	// components under the private workflow root.
	boardIDPattern   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	projectIDPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)
	// uuidPattern is the only shape Claude's --session-id accepts; anything else
	// exits 1 at argument validation, so validating here turns a confusing CLI
	// error into a clear one.
	uuidPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[1-5][0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	// sha256Pattern and objectIDPattern bound recorded digests.
	sha256Pattern   = regexp.MustCompile(`^[0-9a-f]{64}$`)
	objectIDPattern = regexp.MustCompile(`^[0-9a-f]{40}$`)
)

// ValidCheckName reports whether a check name is safe as a label and a filename stem.
func ValidCheckName(value string) bool { return checkNamePattern.MatchString(value) }

// ValidBaseBranch reports whether a branch is safe in argv and in a ref path.
func ValidBaseBranch(value string) bool {
	return baseBranchPattern.MatchString(value) && !strings.Contains(value, "..")
}

// ValidRunID reports whether a run identifier has the generated shape.
func ValidRunID(value string) bool { return runIDPattern.MatchString(value) }

// ValidBoardID reports whether a board identifier is a safe path component.
func ValidBoardID(value string) bool { return boardIDPattern.MatchString(value) }

// ValidProjectID reports whether a project identifier is a safe path component.
func ValidProjectID(value string) bool { return projectIDPattern.MatchString(value) }

// ValidSessionID reports whether a value is a UUID.
func ValidSessionID(value string) bool { return uuidPattern.MatchString(value) }

// ValidSha256 reports whether a value is a full lowercase SHA-256 digest.
func ValidSha256(value string) bool { return sha256Pattern.MatchString(value) }

// ValidObjectID reports whether a value is a full lowercase Git object name.
func ValidObjectID(value string) bool { return objectIDPattern.MatchString(value) }

// ValidArgvToken reports whether a token is safe to exec.
//
// Argv tokens are exec'd directly, so shell metacharacters are harmless.
// Control characters are not: they can truncate or confuse the exec boundary
// and the logs.
func ValidArgvToken(value string) bool {
	if value == "" || len(value) > MaxArgvTokenChars {
		return false
	}
	if strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

// ValidCheckCommand reports allowlist membership for argv[0].
func ValidCheckCommand(value string) bool { return slices.Contains(CheckCommands, value) }
