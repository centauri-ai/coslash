//go:build embedded_helpers

package remote

import (
	"context"
	"errors"
	"testing"
)

func TestEmbeddedHelperReleaseProvider(t *testing.T) {
	provider, available := newProductionHelperReleaseProvider()
	if !available {
		t.Fatal("tagged build did not enable the embedded helper provider")
	}
	document, err := provider.LoadMetadata(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := (TrustStore{}).Verify(document); !errors.Is(err, ErrHelperMetadata) {
		t.Fatalf("untrusted embedded document error = %v", err)
	}
	metadata, err := (TrustStore{AllowEmbedded: true}).Verify(document)
	if err != nil {
		t.Fatal(err)
	}
	if len(metadata.Artifacts) != 2 {
		t.Fatalf("embedded artifacts = %d, want 2", len(metadata.Artifacts))
	}
	seen := map[string]bool{}
	for _, artifact := range metadata.Artifacts {
		content, err := provider.LoadArtifact(context.Background(), artifact)
		if err != nil {
			t.Fatal(err)
		}
		if err := verifyLocalArtifact(artifact, content); err != nil {
			t.Fatalf("verify embedded linux/%s helper: %v", artifact.Arch, err)
		}
		seen[artifact.Arch] = true
	}
	if !seen["amd64"] || !seen["arm64"] {
		t.Fatalf("embedded architectures = %v", seen)
	}
}
