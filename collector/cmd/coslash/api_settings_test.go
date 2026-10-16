package main

import (
	"testing"

	"github.com/centauri-ai/coslash/collector/internal/settings"
)

func TestOpenCodeModelOptionsPreferUserDefault(t *testing.T) {
	for _, models := range [][]string{nil, {settings.OpenCodeSynthesisModel, "opencode/another-free", "--invalid"}} {
		options := openCodeModels(models)
		if len(options) == 0 || options[0].ID != settings.OpenCodeDefaultModel || !options[0].Default {
			t.Fatalf("OpenCode default is not preferred: %+v", options)
		}
		for _, option := range options[1:] {
			if option.Default || !settings.ValidSynthesisModel(option.ID) {
				t.Fatalf("unexpected model option: %+v", option)
			}
		}
		if len(models) > 0 && len(options) != 3 {
			t.Fatalf("explicit valid model choices were lost: %+v", options)
		}
	}
}
