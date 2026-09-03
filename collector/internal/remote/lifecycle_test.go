package remote

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
)

func TestLifecycleReusesVerifiedCurrentHelper(t *testing.T) {
	remote, artifact, content := lifecycleFixture(t)
	path, _ := helperPath(artifact.Version)
	remote.files[path] = remoteFile(path, artifact, remote.platform.UID)
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{})
	if result.State != LifecycleReady || !result.CanExecute || remote.installs != 0 || remote.capabilitiesCalls != 1 {
		t.Fatalf("result = %#v, installs = %d, capabilities = %d", result, remote.installs, remote.capabilitiesCalls)
	}
}

func TestLifecycleDeprecatedHelperIsReusedUntilUpgradeConsent(t *testing.T) {
	remote, current, content := lifecycleFixture(t)
	older := current
	older.Version = "v0"
	older.Current = false
	remote.document.Metadata.Artifacts = []Artifact{current, older}
	remote.document = signDocument(t, remote.document.Metadata)
	path, _ := helperPath(older.Version)
	remote.files[path] = remoteFile(path, older, remote.platform.UID)
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{})
	if result.State != LifecycleDeprecated || !result.CanExecute || !errors.Is(result.Reason, ErrHelperUpgradeRequired) || remote.installs != 0 {
		t.Fatalf("result = %#v, installs = %d", result, remote.installs)
	}
	result = lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Upgrade: true})
	currentPath, _ := helperPath(current.Version)
	if result.State != LifecycleReady || remote.files[path].Path != "" || remote.files[currentPath].Path == "" {
		t.Fatalf("upgrade result = %#v, files = %#v", result, remote.files)
	}
}

func TestLifecycleInstallsOnlyWithConsentAndVerifiesActivation(t *testing.T) {
	remote, artifact, content := lifecycleFixture(t)
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{})
	if result.State != LifecycleUpgradeRequired || !errors.Is(result.Reason, ErrHelperConsentRequired) || remote.installs != 0 {
		t.Fatalf("without consent = %#v, installs = %d", result, remote.installs)
	}
	result = lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Install: true})
	if result.State != LifecycleReady || !result.CanExecute || remote.installs != 1 {
		t.Fatalf("with consent = %#v, installs = %d", result, remote.installs)
	}
	if remote.lastRequest.Destination != "~/.coslash/helpers/v1/coslash-helper" || remote.lastRequest.Temporary != remote.lastRequest.Destination+".new" {
		t.Fatalf("install request = %#v", remote.lastRequest)
	}
	if _, err := os.Stat(filepath.Join(remote.root, artifact.Version, HelperFileName)); err != nil {
		t.Fatalf("activated helper missing from fake remote: %v", err)
	}
	_ = artifact
}

func TestLifecycleRejectsTamperedAndRevokedBeforeExecution(t *testing.T) {
	remote, artifact, content := lifecycleFixture(t)
	path, _ := helperPath(artifact.Version)
	bad := remoteFile(path, artifact, remote.platform.UID)
	bad.SHA256 = "00" + bad.SHA256[2:]
	remote.files[path] = bad
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{})
	if result.CanExecute || result.State != LifecycleUpgradeRequired || remote.capabilitiesCalls != 0 {
		t.Fatalf("tampered result = %#v, caps = %d", result, remote.capabilitiesCalls)
	}
	remote.document.Metadata.Artifacts[0].Revoked = true
	remote.document = signDocument(t, remote.document.Metadata)
	result = lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Upgrade: true})
	if result.State != LifecycleRevoked || result.CanExecute || remote.capabilitiesCalls != 0 {
		t.Fatalf("revoked result = %#v, caps = %d", result, remote.capabilitiesCalls)
	}
}

func TestLifecycleRevokedPreviousDoesNotBlockSafeCurrentInstall(t *testing.T) {
	remote, current, content := lifecycleFixture(t)
	older := current
	older.Version = "v0"
	older.Current = false
	older.Revoked = true
	remote.document.Metadata.Artifacts = []Artifact{current, older}
	remote.document = signDocument(t, remote.document.Metadata)
	oldPath, _ := helperPath(older.Version)
	remote.files[oldPath] = remoteFile(oldPath, older, remote.platform.UID)
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Install: true})
	if result.State != LifecycleReady || !result.CanExecute || remote.installs != 1 {
		t.Fatalf("result = %#v, installs = %d", result, remote.installs)
	}
}

func TestLifecycleNoExecAndRollbackStayOnSFTP(t *testing.T) {
	remote, _, content := lifecycleFixture(t)
	remote.installErr = ErrHelperNoExec
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Install: true})
	if result.State != LifecycleSFTP || !result.Fallback || !errors.Is(result.Reason, ErrHelperNoExec) {
		t.Fatalf("noexec result = %#v", result)
	}
	remote.installErr = ErrHelperRollback
	result = lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Install: true})
	if result.State != LifecycleSFTP || !errors.Is(result.Reason, ErrHelperRollback) {
		t.Fatalf("rollback result = %#v", result)
	}
}

func TestLifecycleInterruptedInstallFailsClosed(t *testing.T) {
	remote, artifact, content := lifecycleFixture(t)
	path, _ := helperPath(artifact.Version)
	remote.installErr = context.DeadlineExceeded
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Install: true})
	if result.CanExecute || !result.Fallback || !errors.Is(result.Reason, context.DeadlineExceeded) {
		t.Fatalf("interrupted install result = %#v", result)
	}
	if _, exists := remote.files[path]; exists {
		t.Fatalf("interrupted install activated helper: %#v", remote.files)
	}
}

func TestLifecyclePlatformAndMetadataAreAuthenticated(t *testing.T) {
	remote, _, content := lifecycleFixture(t)
	remote.platform.Arch = "riscv64"
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Install: true})
	if result.State != LifecycleUnsupported || remote.installs != 0 {
		t.Fatalf("unsupported result = %#v", result)
	}
	remote.platform.Arch = "amd64"
	remote.document.Signature = base64.StdEncoding.EncodeToString([]byte("not a signature"))
	result = lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Upgrade: true})
	if result.State != LifecycleVerificationError || remote.installs != 0 {
		t.Fatalf("metadata result = %#v", result)
	}
}

func TestLifecycleUninstallIsExactAndIdempotent(t *testing.T) {
	remote, artifact, _ := lifecycleFixture(t)
	path, _ := helperPath(artifact.Version)
	remote.files[path] = remoteFile(path, artifact, remote.platform.UID)
	lifecycle := lifecycleFor(remote)
	if err := lifecycle.Uninstall(context.Background(), remote.document, artifact.Version); err != nil {
		t.Fatal(err)
	}
	if remote.removed != path || remote.files[path].Path != "" {
		t.Fatalf("removed = %q, files = %#v", remote.removed, remote.files)
	}
	if _, err := os.Stat(filepath.Join(remote.root, artifact.Version, HelperFileName)); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("uninstall did not remove exact helper: %v", err)
	}
	if err := lifecycle.Uninstall(context.Background(), remote.document, artifact.Version); err != nil {
		t.Fatal(err)
	}
	if err := lifecycle.Uninstall(context.Background(), remote.document, "unknown"); !errors.Is(err, ErrUnknownHelperPath) {
		t.Fatalf("unknown uninstall error = %v", err)
	}
}

func TestLifecycleRollsBackToPreviousHelperAfterCapabilityFailure(t *testing.T) {
	remote, current, content := lifecycleFixture(t)
	older := current
	older.Version = "v0"
	older.Current = false
	remote.document.Metadata.Artifacts = []Artifact{current, older}
	remote.document = signDocument(t, remote.document.Metadata)
	oldPath, _ := helperPath(older.Version)
	currentPath, _ := helperPath(current.Version)
	remote.files[oldPath] = remoteFile(oldPath, older, remote.platform.UID)
	remote.capabilityErrors = map[string]error{currentPath: ErrHelperIncompatible}

	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Upgrade: true})
	if result.State != LifecycleDeprecated || !result.CanExecute || result.Path != oldPath || !errors.Is(result.Reason, ErrHelperRollback) {
		t.Fatalf("rollback result = %#v", result)
	}
	if _, exists := remote.files[currentPath]; exists {
		t.Fatalf("failed current helper was retained: %#v", remote.files[currentPath])
	}
}

func TestTrustStoreRejectsExpiredAndReplayedMetadata(t *testing.T) {
	remote, _, _ := lifecycleFixture(t)
	guard := &MemoryMetadataSequenceStore{}
	trust := lifecycleFor(remote).Trust
	trust.Sequences = guard
	newer := remote.document.Metadata
	newer.Sequence = 2
	newerDocument := signDocument(t, newer)
	if _, err := trust.Verify(newerDocument); err != nil {
		t.Fatal(err)
	}
	if _, err := trust.Verify(remote.document); !errors.Is(err, ErrHelperMetadataRollback) {
		t.Fatalf("replay error = %v", err)
	}
	expired := newer
	expired.Sequence = 3
	expired.ExpiresAtUnix = 1
	if _, err := trust.Verify(signDocument(t, expired)); !errors.Is(err, ErrHelperMetadataExpired) {
		t.Fatalf("expiry error = %v", err)
	}
}

func TestTrustStoreRejectsMutatedSignedMetadataFields(t *testing.T) {
	remote, _, _ := lifecycleFixture(t)
	for name, mutate := range map[string]func(*ReleaseMetadata){
		"sequence": func(metadata *ReleaseMetadata) { metadata.Sequence++ },
		"expiry":   func(metadata *ReleaseMetadata) { metadata.ExpiresAtUnix += 3600 },
	} {
		t.Run(name, func(t *testing.T) {
			document := remote.document
			mutate(&document.Metadata)
			if _, err := lifecycleFor(remote).Trust.Verify(document); !errors.Is(err, ErrHelperMetadata) {
				t.Fatalf("Verify mutated metadata error = %v, want ErrHelperMetadata", err)
			}
		})
	}
}

func TestLifecycleDoesNotMisclassifyTransportFailureAsUnsupported(t *testing.T) {
	remote, _, content := lifecycleFixture(t)
	remote.probeErr = context.DeadlineExceeded
	result := lifecycleFor(remote).Setup(context.Background(), remote.document, content, Consent{Install: true})
	if result.State != LifecycleSFTP || !errors.Is(result.Reason, context.DeadlineExceeded) {
		t.Fatalf("transport result = %#v", result)
	}
}

func TestParsePlatformProbeRejectsAnythingButFixedOutput(t *testing.T) {
	platform, err := ParsePlatformProbe("Linux\nx86_64\n501\n")
	if err != nil || platform.Arch != "amd64" || platform.UID != 501 {
		t.Fatalf("platform = %#v, error = %v", platform, err)
	}
	for _, output := range []string{"Linux\nx86_64\n", "Linux\nriscv64\n1", "Linux x86_64 1 trailing"} {
		if _, err := ParsePlatformProbe(output); err == nil {
			t.Fatalf("accepted probe %q", output)
		}
	}
}

func TestHelperPlatformArgsAreFixed(t *testing.T) {
	args, err := HelperPlatformArgs("host", 1)
	if err != nil || args[len(args)-1] != "uname -s; uname -m; id -u" {
		t.Fatalf("args = %#v, error = %v", args, err)
	}
	if _, err := HelperPlatformArgs("host; hostile", 1); !errors.Is(err, ErrInvalidAlias) {
		t.Fatalf("injection error = %v", err)
	}
}

func TestClassifyLifecycleError(t *testing.T) {
	for _, test := range []struct {
		err  error
		want Reason
	}{
		{ErrUnsupportedHelperPlatform, ReasonHelperUnsupported},
		{ErrHelperVerification, ReasonHelperVerification},
		{ErrHelperNoExec, ReasonHelperBlocked},
		{ErrHelperRevoked, ReasonHelperRevoked},
		{ErrHelperConsentRequired, ReasonHelperConsent},
		{ErrHelperRollback, ReasonHelperRollback},
		{ErrHelperInstallation, ReasonHelperInstallation},
	} {
		if got := classifyLifecycleError(test.err); got != test.want {
			t.Errorf("classifyLifecycleError(%v) = %q, want %q", test.err, got, test.want)
		}
	}
}

func lifecycleFixture(t *testing.T) (*fakeLifecycleRemote, Artifact, []byte) {
	t.Helper()
	arch := "amd64"
	content := syntheticELF(arch)
	artifact := Artifact{Version: "v1", OS: "linux", Arch: arch, Size: int64(len(content)), SHA256: digest(content), Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1}, Schema: remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion}, Current: true}
	capabilities := compatibleCapabilities()
	capabilities.Arch = arch
	remote := &fakeLifecycleRemote{root: t.TempDir(), platform: Platform{OS: "linux", Arch: arch, UID: 501}, files: map[string]RemoteFile{}, capabilities: capabilities}
	remote.document = signDocument(t, ReleaseMetadata{Sequence: 1, ExpiresAtUnix: time.Now().Add(time.Hour).Unix(), Artifacts: []Artifact{artifact}})
	return remote, artifact, content
}

func syntheticELF(arch string) []byte {
	content := make([]byte, 64)
	copy(content, []byte{0x7f, 'E', 'L', 'F', 2, 1})
	binary.LittleEndian.PutUint16(content[16:18], 2)
	machine := uint16(62)
	if arch == "arm64" {
		machine = 183
	}
	binary.LittleEndian.PutUint16(content[18:20], machine)
	return content
}

var lifecyclePrivate ed25519.PrivateKey

func signDocument(t *testing.T, metadata ReleaseMetadata) SignedReleaseMetadata {
	t.Helper()
	if lifecyclePrivate == nil {
		_, private, err := ed25519.GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		lifecyclePrivate = private
	}
	return SignedReleaseMetadata{KeyID: "test-2026", Metadata: metadata, Signature: base64.StdEncoding.EncodeToString(ed25519.Sign(lifecyclePrivate, canonicalMetadataPayload(metadata)))}
}

func lifecycleFor(remote *fakeLifecycleRemote) Lifecycle {
	return Lifecycle{Remote: remote, Trust: TrustStore{
		Keys:      map[string]ed25519.PublicKey{"test-2026": lifecyclePrivate.Public().(ed25519.PublicKey)},
		Sequences: &MemoryMetadataSequenceStore{},
	}}
}

func compatibleCapabilities() remoteprotocol.Capabilities {
	return remoteprotocol.Capabilities{Protocol: remoteprotocol.VersionRange{Min: 1, Max: 1}, Schema: remoteprotocol.VersionRange{Min: remotefacts.SchemaVersion, Max: remotefacts.SchemaVersion}, ParserVersion: "parsers-1", OS: "linux", Arch: "amd64"}
}

func remoteFile(path string, artifact Artifact, uid uint32) RemoteFile {
	return RemoteFile{Path: path, Size: artifact.Size, SHA256: artifact.SHA256, Mode: 0o700, UID: uid, Regular: true}
}

type fakeLifecycleRemote struct {
	root              string
	platform          Platform
	files             map[string]RemoteFile
	document          SignedReleaseMetadata
	capabilities      remoteprotocol.Capabilities
	installErr        error
	installs          int
	capabilitiesCalls int
	probeErr          error
	capabilityErrors  map[string]error
	removeErr         error
	lastRequest       InstallRequest
	removed           string
}

func (remote *fakeLifecycleRemote) ProbePlatform(context.Context) (Platform, error) {
	return remote.platform, remote.probeErr
}
func (remote *fakeLifecycleRemote) Inspect(_ context.Context, path string) (RemoteFile, error) {
	file, ok := remote.files[path]
	if !ok {
		return RemoteFile{}, fs.ErrNotExist
	}
	return file, nil
}
func (remote *fakeLifecycleRemote) Install(_ context.Context, request InstallRequest) (RemoteFile, error) {
	remote.installs++
	remote.lastRequest = request
	if remote.installErr != nil {
		return RemoteFile{}, remote.installErr
	}
	directory := filepath.Join(remote.root, request.Artifact.Version)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return RemoteFile{}, err
	}
	temporary := filepath.Join(directory, HelperFileName+".new")
	destination := filepath.Join(directory, HelperFileName)
	if err := os.WriteFile(temporary, request.Bytes, 0o600); err != nil {
		return RemoteFile{}, err
	}
	if err := os.Chmod(temporary, 0o700); err != nil {
		return RemoteFile{}, err
	}
	if err := os.Rename(temporary, destination); err != nil {
		return RemoteFile{}, err
	}
	file := remoteFile(request.Destination, request.Artifact, request.OwnerUID)
	remote.files[request.Destination] = file
	return file, nil
}
func (remote *fakeLifecycleRemote) RemoveExact(_ context.Context, path string) error {
	remote.removed = path
	if remote.removeErr != nil {
		return remote.removeErr
	}
	for _, artifact := range remote.document.Metadata.Artifacts {
		known, _ := helperPath(artifact.Version)
		if known == path {
			if err := os.Remove(filepath.Join(remote.root, artifact.Version, HelperFileName)); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return err
			}
			break
		}
	}
	delete(remote.files, path)
	return nil
}
func (remote *fakeLifecycleRemote) Capabilities(_ context.Context, path string) (remoteprotocol.Capabilities, error) {
	remote.capabilitiesCalls++
	return remote.capabilities, remote.capabilityErrors[path]
}
