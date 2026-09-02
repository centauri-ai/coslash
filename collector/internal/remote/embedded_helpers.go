//go:build embedded_helpers

package remote

import (
	"context"
	_ "embed"
	"errors"
	"math"
	"slices"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
)

// These files are generated immediately before an embedded release build and
// removed afterward. They are never checked into the repository.
//
//go:embed helperassets/coslash-helper_linux_amd64
var embeddedHelperAMD64 []byte

//go:embed helperassets/coslash-helper_linux_arm64
var embeddedHelperARM64 []byte

// Set by release builds so the remote versioned install path matches the
// containing Coslash release. A tagged build that forgets the ldflag fails
// closed instead of publishing an ambiguous development helper.
var embeddedHelperVersion = ""

type embeddedHelperReleaseProvider struct {
	document  SignedReleaseMetadata
	artifacts map[string][]byte
}

func newProductionHelperReleaseProvider() (HelperReleaseProvider, bool) {
	if !helperVersionPattern.MatchString(embeddedHelperVersion) ||
		len(embeddedHelperAMD64) == 0 || len(embeddedHelperARM64) == 0 {
		return unavailableReleaseProvider{}, false
	}
	artifacts := map[string][]byte{
		"linux/amd64": embeddedHelperAMD64,
		"linux/arm64": embeddedHelperARM64,
	}
	metadata := ReleaseMetadata{Sequence: 1, ExpiresAtUnix: math.MaxInt64}
	for _, arch := range []string{"amd64", "arm64"} {
		content := artifacts["linux/"+arch]
		metadata.Artifacts = append(metadata.Artifacts, Artifact{
			Version: embeddedHelperVersion, OS: "linux", Arch: arch,
			Size: int64(len(content)), SHA256: digest(content), Current: true,
			Protocol: remoteprotocol.VersionRange{Min: remoteprotocol.ProtocolVersion, Max: remoteprotocol.ProtocolVersion},
			Schema:   remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion},
		})
	}
	if err := metadata.Validate(); err != nil {
		return unavailableReleaseProvider{}, false
	}
	return &embeddedHelperReleaseProvider{
		document:  SignedReleaseMetadata{Metadata: metadata, embedded: true},
		artifacts: artifacts,
	}, true
}

func (provider *embeddedHelperReleaseProvider) LoadMetadata(context.Context) (SignedReleaseMetadata, error) {
	return provider.document, nil
}

func (provider *embeddedHelperReleaseProvider) LoadArtifact(_ context.Context, artifact Artifact) ([]byte, error) {
	content, ok := provider.artifacts[artifact.OS+"/"+artifact.Arch]
	if !ok || artifact.Version != embeddedHelperVersion ||
		int64(len(content)) != artifact.Size || digest(content) != artifact.SHA256 {
		return nil, errors.New("embedded helper artifact is unavailable")
	}
	return slices.Clone(content), nil
}
