package atlas

import (
	"slices"
	"strings"
)

// Role system prompts for Atlas Plan / Build / Review, plus the Plan main
// refine turn. One copy per role lives on the board, so edits apply to every
// future run of that board.
//
// Placeholders are substituted at assemble time by the controller:
//
//	{{OUTPUT_PATH}}       relative path for the primary required artifact
//	{{OUTPUT_JSON_PATH}}  relative path for Review's typed JSON
//	{{OUTPUT_NAME}}       basename of the primary artifact

// SystemPromptRole names one editable role prompt.
type SystemPromptRole string

const (
	RolePlan       SystemPromptRole = "plan"
	RoleBuild      SystemPromptRole = "build"
	RoleReview     SystemPromptRole = "review"
	RolePlanRefine SystemPromptRole = "planRefine"
)

// SystemPromptRoles is the closed set of editable role prompts.
var SystemPromptRoles = []SystemPromptRole{RolePlan, RoleBuild, RoleReview, RolePlanRefine}

// SystemPromptPlaceholders are the substitutions the controller performs.
var SystemPromptPlaceholders = []string{"{{OUTPUT_PATH}}", "{{OUTPUT_JSON_PATH}}", "{{OUTPUT_NAME}}"}

// SystemPrompts is one board's copy of every role prompt.
type SystemPrompts struct {
	Plan       string `json:"plan"`
	Build      string `json:"build"`
	Review     string `json:"review"`
	PlanRefine string `json:"planRefine"`
}

// Defaults are delivered as the seat's first user turn — the launcher pipes the
// assembled prompt into `claude -p` / `codex exec` on stdin — so the harness's
// own system prompt is already in place. Each states the turn's task
// imperatively rather than asserting an identity the harness has already fixed,
// and is ordered task → inputs → output → boundaries → done condition so the
// deliverable and the failure condition stay adjacent.
const (
	DefaultPlanPrompt = "Your task this turn is to produce an implementation plan. Read PROBLEM.md at the listed path and any source file it names; SOURCE.md is reference-only for text sources. Write a non-empty Markdown plan to `{{OUTPUT_PATH}}`, creating parent directories if needed. Do not modify project files or anything under `.fleetlog/` other than that output path. A validator outside this conversation checks for that file when the turn ends — if it is missing the turn fails (`missing_output`), regardless of what you report."

	DefaultBuildPrompt = "Your task this turn is to implement a plan. Read PLAN.md at the listed path, then implement it by editing project files in this worktree. Write a short non-empty Markdown summary of what you changed to `{{OUTPUT_PATH}}`, creating parent directories if needed. Do not modify anything under `.fleetlog/` other than that output path. Two conditions fail the turn and are both checked externally: that output file missing (`missing_output`), and a turn that changes no project files."

	DefaultReviewPrompt = "Your task this turn is to review a changeset against the problem and plan it came from. Read the listed PROBLEM.md, PLAN.md, CHANGESET.patch, and verification.json, then write your reasoning as prose to `{{OUTPUT_PATH}}` and your verdict as typed JSON to `{{OUTPUT_JSON_PATH}}` — the contract for that JSON is below. Judge only what those artifacts show; do not modify project files. Both files must exist when the turn ends or it fails, and any blocking finding or non-approved verdict becomes changes_requested."

	DefaultPlanRefinePrompt = "Your task this turn is to refine several draft plans into one final plan. Read every draft at the listed paths, including your own earlier draft, and resolve where they disagree — choose the stronger approach rather than concatenating. Write the result as one coherent non-empty Markdown plan to `{{OUTPUT_PATH}}`. Do not modify project files or anything under `.fleetlog/` other than that output path. A validator outside this conversation checks for that file when the turn ends — if it is missing the turn fails (`missing_output`)."
)

// DefaultSystemPrompts returns the current role defaults.
func DefaultSystemPrompts() SystemPrompts {
	return SystemPrompts{
		Plan:       DefaultPlanPrompt,
		Build:      DefaultBuildPrompt,
		Review:     DefaultReviewPrompt,
		PlanRefine: DefaultPlanRefinePrompt,
	}
}

// supersededSystemPrompts are defaults this product has outgrown, frozen
// verbatim.
//
// Boards persist a literal copy of every role prompt, so changing a default
// above does not reach boards that already exist. A saved prompt byte-equal to
// one of these was never customized and can be upgraded, while an edited prompt
// never matches and is left alone. Append an outgoing default here whenever a
// default changes; never edit one.
var supersededSystemPrompts = []string{
	"You are the Atlas Plan seat. Write a non-empty Markdown implementation plan to `{{OUTPUT_PATH}}` (create parent directories if needed). Open and review PROBLEM.md at the listed path (and any source file it names); SOURCE.md is reference-only for text sources. Do not modify `.fleetlog/` except that output path. Completing the turn without that file fails the seat (`missing_output`).",
	"You are the Atlas Build seat. Open and review PLAN.md at the listed path, implement it by editing project files in this worktree, then write a short non-empty Markdown summary to `{{OUTPUT_PATH}}` (create parent directories if needed). A turn that changes no project files fails. Do not modify `.fleetlog/` except that output path. Completing the turn without that file fails the seat (`missing_output`).",
	"You are the Atlas Review seat. Use Read on the listed PROBLEM.md, PLAN.md, CHANGESET.patch, and verification.json paths (no Bash exploration, lint, or tests), then write prose to `{{OUTPUT_PATH}}` and typed JSON to `{{OUTPUT_JSON_PATH}}` promptly. Do not modify project files. The JSON must include schemaVersion 1, verdict (`approved`|`changes_requested`), summary, and findings (`severity`, `summary`, string `detail` — use `\"\"` if empty). Completing the turn without both files fails; any blocking finding or non-approved verdict becomes changes_requested.",
	"You are the Atlas Plan main worker refining drafts into the final plan. Open and review every draft at the listed paths (including yours), resolve conflicts, and write one coherent non-empty Markdown plan to `{{OUTPUT_PATH}}`. Do not merely concatenate drafts. Do not modify `.fleetlog/` except that output path. Completing the turn without that file fails the seat (`missing_output`).",
}

// IsSupersededSystemPrompt reports whether a saved prompt is an untouched copy
// of a default this role has outgrown.
func IsSupersededSystemPrompt(saved string) bool {
	return slices.Contains(supersededSystemPrompts, strings.TrimSpace(saved))
}

// Get returns the prompt for one role.
func (p SystemPrompts) Get(role SystemPromptRole) string {
	switch role {
	case RoleBuild:
		return p.Build
	case RoleReview:
		return p.Review
	case RolePlanRefine:
		return p.PlanRefine
	default:
		return p.Plan
	}
}

func (p *SystemPrompts) set(role SystemPromptRole, value string) {
	switch role {
	case RoleBuild:
		p.Build = value
	case RoleReview:
		p.Review = value
	case RolePlanRefine:
		p.PlanRefine = value
	default:
		p.Plan = value
	}
}

// normalizeSystemPrompts repairs a saved copy: blanks fall back to the current
// default, untouched superseded defaults upgrade to it, and edits survive.
func normalizeSystemPrompts(prompts *SystemPrompts) {
	defaults := DefaultSystemPrompts()
	for _, role := range SystemPromptRoles {
		saved := clampText(prompts.Get(role), MaxSystemPromptBytes)
		if strings.TrimSpace(saved) == "" || IsSupersededSystemPrompt(saved) {
			saved = defaults.Get(role)
		}
		prompts.set(role, saved)
	}
}

// ApplySystemPromptPlaceholders substitutes the assemble-time placeholders.
// It is pure: an absent optional value substitutes as empty rather than leaving
// the placeholder visible in an agent prompt.
func ApplySystemPromptPlaceholders(template, outputPath, outputJSONPath, outputName string) string {
	if outputName == "" {
		outputName = pathBase(outputPath)
	}
	replacer := strings.NewReplacer(
		"{{OUTPUT_PATH}}", outputPath,
		"{{OUTPUT_JSON_PATH}}", outputJSONPath,
		"{{OUTPUT_NAME}}", outputName,
	)
	return replacer.Replace(template)
}

func pathBase(relativePath string) string {
	index := strings.LastIndexAny(relativePath, `/\`)
	if index < 0 || index == len(relativePath)-1 {
		return relativePath
	}
	return relativePath[index+1:]
}
