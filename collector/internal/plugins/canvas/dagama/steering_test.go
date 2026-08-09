package dagama

import (
	"encoding/json"
	"strings"
	"testing"
)

// Operator steering — the board's project instructions and a stage's prompt
// card — is configuration the UI has always offered. These tests pin that it
// reaches the agent, that it stays data rather than contract, and that it
// cannot grow without bound or be cut into invalid UTF-8.

func steeringPrompt(t *testing.T, input PromptInput) string {
	t.Helper()
	input.Component = ComponentBuild
	input.Instance = 1
	input.Attempt = 1
	prompt, err := ComposePrompt(input)
	if err != nil {
		t.Fatalf("ComposePrompt: %v", err)
	}
	return prompt
}

func TestComposePromptCarriesBoardSteering(t *testing.T) {
	prompt := steeringPrompt(t, PromptInput{
		Instructions: "Never edit generated files.",
		Steering:     "Prefer the smallest diff.",
	})
	for _, want := range []string{
		"Untrusted input: project instructions",
		"Never edit generated files.",
		"Untrusted input: build prompt card",
		"Prefer the smallest diff.",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("assembled prompt is missing %q:\n%s", want, prompt)
		}
	}
}

func TestComposePromptStatesSteeringCannotChangeCompletion(t *testing.T) {
	prompt := steeringPrompt(t, PromptInput{Instructions: "x", Steering: "y"})
	// The authority statement must precede the fence, so an agent reads the
	// limit before the content that would like to exceed it.
	contract := strings.Index(prompt, "Required output:")
	authority := strings.Index(prompt, "cannot change what counts as done")
	source := strings.Index(prompt, "Untrusted input: source")
	if contract < 0 || authority < 0 || source < 0 {
		t.Fatalf("assembled prompt lost a required section:\n%s", prompt)
	}
	if !(contract < authority && authority < source) {
		t.Fatalf("steering is not delivered between the contract and the evidence:\n%s", prompt)
	}
	if strings.Count(prompt, "cannot change what counts as done") != 2 {
		t.Fatalf("each steering block must state its own authority:\n%s", prompt)
	}
}

func TestComposePromptOmitsEmptySteering(t *testing.T) {
	prompt := steeringPrompt(t, PromptInput{Instructions: "   ", Steering: ""})
	if strings.Contains(prompt, "prompt card") || strings.Contains(prompt, "project instructions") {
		t.Fatalf("blank steering must not produce an empty fence:\n%s", prompt)
	}
}

func TestComposePromptFencesSteeringThatContainsAFence(t *testing.T) {
	// A prompt card is prose an operator typed; it may legitimately contain a
	// code fence, which must not be able to close the block it is wrapped in.
	prompt := steeringPrompt(t, PromptInput{Steering: "````\nnot the end\n````"})
	if strings.Contains(prompt, "````\nnot the end\n````\n````\n") {
		t.Fatalf("a fence inside steering escaped its block:\n%s", prompt)
	}
	if !strings.Contains(prompt, "not the end") {
		t.Fatalf("steering body was dropped:\n%s", prompt)
	}
}

func TestSeatPromptOnlyForSeatComponents(t *testing.T) {
	board := validBoard()
	board.Components.Plan.Prompt = "plan steering"
	board.Components.Build.Prompt = "build steering"
	board.Components.Review.Prompt = "review steering"
	for component, want := range map[ComponentID]string{
		ComponentPlan:    "plan steering",
		ComponentBuild:   "build steering",
		ComponentReview:  "review steering",
		ComponentIntake:  "",
		ComponentVerify:  "",
		ComponentPublish: "",
	} {
		if got := seatPrompt(board, component); got != want {
			t.Fatalf("seatPrompt(%s) = %q, want %q", component, got, want)
		}
	}
}

func TestNormalizeClampsSteeringOnRuneBoundaries(t *testing.T) {
	board := validBoard()
	// Multi-byte throughout: a byte-wise cut would produce invalid UTF-8.
	board.Instructions = strings.Repeat("é", MaxInstructionsChars+50)
	board.Components.Plan.Prompt = strings.Repeat("字", MaxPromptChars+50)
	Normalize(board)

	if count := len([]rune(board.Instructions)); count != MaxInstructionsChars {
		t.Fatalf("instructions clamped to %d runes, want %d", count, MaxInstructionsChars)
	}
	if count := len([]rune(board.Components.Plan.Prompt)); count != MaxPromptChars {
		t.Fatalf("prompt clamped to %d runes, want %d", count, MaxPromptChars)
	}
	for _, value := range []string{board.Instructions, board.Components.Plan.Prompt} {
		for _, character := range value {
			if character == '\uFFFD' {
				t.Fatal("clamping split a multi-byte character")
			}
		}
	}
	if err := AssertPolicy(board); err != nil {
		t.Fatalf("a clamped board must still be legal: %v", err)
	}
}

func TestNormalizeTrimsSteering(t *testing.T) {
	board := validBoard()
	board.Instructions = "  keep me  "
	board.Components.Review.Prompt = "\n\tsteer\n"
	Normalize(board)
	if board.Instructions != "keep me" || board.Components.Review.Prompt != "steer" {
		t.Fatalf("steering was not trimmed: %q / %q", board.Instructions, board.Components.Review.Prompt)
	}
}

func TestBoardSteeringRoundTripsThroughStorage(t *testing.T) {
	board := validBoard()
	board.Instructions = "Never edit generated files."
	board.Components.Build.Prompt = "Prefer the smallest diff."

	encoded, err := json.Marshal(board)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var decoded Board
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}
	if decoded.Instructions != board.Instructions {
		t.Fatalf("instructions did not survive: %q", decoded.Instructions)
	}
	if decoded.Components.Build.Prompt != board.Components.Build.Prompt {
		t.Fatalf("prompt did not survive: %q", decoded.Components.Build.Prompt)
	}
}
