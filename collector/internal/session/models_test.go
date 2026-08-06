package session

import "testing"

// A transcript can report a model id dated after the last table refresh. Every
// model in the table must then still resolve through its base id, in either
// vendor's date format. Naming no model here keeps the check free of upkeep.
func TestDatedModelIDsResolveToTheBaseModel(t *testing.T) {
	for model := range models {
		for _, dated := range []string{model + "-20991231", model + "-2099-12-31"} {
			if _, ok := modelInfoFor(dated); !ok {
				t.Errorf("%s: no price for the base model %s", dated, model)
			}
		}
	}
	if _, ok := modelInfoFor("mystery-model-2099-12-31"); ok {
		t.Error("mystery-model-2099-12-31: want no price")
	}
}
