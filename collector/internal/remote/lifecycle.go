package remote

// This file deliberately keeps helper lifecycle operations behind a small
// remote boundary.  An SSH/SFTP implementation must perform that boundary's
// filesystem operations relative to a trusted directory descriptor with
// no-follow semantics.  A sequence of remote shell `test`, `mv`, and `rm`
// commands is not an acceptable implementation: it leaves a check/use race.

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
)

const (
	// HelperInstallBase is the only remote directory lifecycle code may touch.
	// It is user-owned and versioned, so a failed update cannot replace a known
	// working helper.  A noexec mount is a deliberate SFTP fallback, not a
	// reason to try a privileged or alternate installation location.
	HelperInstallBase            = "~/.coslash/helpers"
	HelperFileName               = "coslash-helper"
	MaxHelperArtifactBytes int64 = 128 << 20
)

var (
	ErrUnsupportedHelperPlatform = errors.New("unsupported helper platform")
	ErrHelperConsentRequired     = errors.New("helper install or upgrade requires consent")
	ErrHelperMetadata            = errors.New("helper release metadata is not trusted")
	ErrHelperArtifact            = errors.New("helper artifact is invalid")
	ErrHelperVerification        = errors.New("helper verification failed")
	ErrHelperInstallation        = errors.New("helper installation failed")
	ErrHelperNoExec              = errors.New("helper install directory is not executable")
	ErrHelperRevoked             = errors.New("helper artifact is revoked")
	ErrHelperUpgradeRequired     = errors.New("helper upgrade requires consent")
	ErrHelperRollback            = errors.New("helper activation was rolled back")
	ErrUnknownHelperPath         = errors.New("helper path is outside the approved install layout")
	ErrHelperMetadataRollback    = errors.New("helper release metadata is older than previously accepted metadata")
	ErrHelperMetadataExpired     = errors.New("helper release metadata has expired")
)

var helperVersionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)

// Platform is deliberately derived only from the bounded, fixed platform
// probe.  OS and architecture aliases are normalized before artifact lookup.
type Platform struct {
	OS   string
	Arch string
	UID  uint32
}

func normalizePlatform(osName, arch string) (Platform, error) {
	osName = strings.ToLower(strings.TrimSpace(osName))
	arch = strings.ToLower(strings.TrimSpace(arch))
	if osName != "linux" {
		return Platform{}, fmt.Errorf("%w: %s", ErrUnsupportedHelperPlatform, osName)
	}
	switch arch {
	case "x86_64", "amd64":
		arch = "amd64"
	case "aarch64", "arm64":
		arch = "arm64"
	default:
		return Platform{}, fmt.Errorf("%w: linux/%s", ErrUnsupportedHelperPlatform, arch)
	}
	return Platform{OS: osName, Arch: arch}, nil
}

// ParsePlatformProbe accepts the three lines produced by the fixed remote
// command `uname -s; uname -m; id -u`.  Keeping this parser narrow prevents a
// login banner or arbitrary command output from selecting an artifact.
func ParsePlatformProbe(output string) (Platform, error) {
	if len(output) > 128 {
		return Platform{}, fmt.Errorf("%w: platform probe output too long", ErrUnsupportedHelperPlatform)
	}
	lines := strings.Fields(output)
	if len(lines) != 3 {
		return Platform{}, fmt.Errorf("%w: malformed platform probe", ErrUnsupportedHelperPlatform)
	}
	platform, err := normalizePlatform(lines[0], lines[1])
	if err != nil {
		return Platform{}, err
	}
	var uid uint64
	for _, character := range lines[2] {
		if character < '0' || character > '9' {
			return Platform{}, fmt.Errorf("%w: malformed uid", ErrUnsupportedHelperPlatform)
		}
		uid = uid*10 + uint64(character-'0')
		if uid > uint64(^uint32(0)) {
			return Platform{}, fmt.Errorf("%w: uid out of range", ErrUnsupportedHelperPlatform)
		}
	}
	platform.UID = uint32(uid)
	return platform, nil
}

// Artifact is one executable, never an archive.  Its digest therefore covers
// exactly the file that is uploaded and later executed.
type Artifact struct {
	Version  string                      `json:"version"`
	OS       string                      `json:"os"`
	Arch     string                      `json:"arch"`
	Size     int64                       `json:"size"`
	SHA256   string                      `json:"sha256"`
	Protocol remoteprotocol.VersionRange `json:"protocol"`
	Schema   remoteprotocol.VersionRange `json:"schema"`
	// Current identifies the one artifact selected for a platform. Older
	// non-revoked entries remain only for the supported deprecated window.
	Current bool `json:"current,omitempty"`
	Revoked bool `json:"revoked,omitempty"`
}

type ReleaseMetadata struct {
	Sequence      uint64     `json:"sequence"`
	ExpiresAtUnix int64      `json:"expires_at_unix"`
	Artifacts     []Artifact `json:"artifacts"`
}

// SignedReleaseMetadata is the release document fetched by the app.  The
// release pipeline signs canonicalMetadataPayload(metadata), not transport
// formatting, so whitespace or object-key ordering cannot affect verification.
type SignedReleaseMetadata struct {
	KeyID     string          `json:"key_id"`
	Metadata  ReleaseMetadata `json:"metadata"`
	Signature string          `json:"signature"`
	// embedded is set only by the compile-time asset provider. It cannot be
	// supplied by decoded metadata and makes the containing Coslash binary the
	// authentication boundary instead of a second runtime signature service.
	embedded bool
}

// TrustStore is compiled into the Mac application.  Shipping a replacement
// app can add a key during rotation; a signed document can revoke artifacts,
// while a compiled revoked key is never accepted even if a stale document uses
// it.
type TrustStore struct {
	Keys            map[string]ed25519.PublicKey
	RevokedKeys     map[string]bool
	MinimumSequence uint64
	Now             func() time.Time
	Sequences       MetadataSequenceStore
	AllowEmbedded   bool
}

// MetadataSequenceStore durably remembers the newest authenticated release
// sequence. Accept must allow the same sequence again and reject lower values.
// This closes the replay hole that signatures alone cannot address.
type MetadataSequenceStore interface {
	Accept(uint64) error
}

// MemoryMetadataSequenceStore is useful for one-process callers and tests.
// Production callers should use FileMetadataSequenceStore.
type MemoryMetadataSequenceStore struct {
	mu      sync.Mutex
	highest uint64
}

func (store *MemoryMetadataSequenceStore) Accept(sequence uint64) error {
	store.mu.Lock()
	defer store.mu.Unlock()
	if sequence < store.highest {
		return ErrHelperMetadataRollback
	}
	store.highest = sequence
	return nil
}

func (trust TrustStore) Verify(document SignedReleaseMetadata) (ReleaseMetadata, error) {
	if document.embedded {
		if !trust.AllowEmbedded || document.KeyID != "" || document.Signature != "" {
			return ReleaseMetadata{}, ErrHelperMetadata
		}
		if err := document.Metadata.Validate(); err != nil {
			return ReleaseMetadata{}, fmt.Errorf("%w: %v", ErrHelperMetadata, err)
		}
		return document.Metadata, nil
	}
	if !validMetadataID(document.KeyID) || trust.RevokedKeys[document.KeyID] {
		return ReleaseMetadata{}, ErrHelperMetadata
	}
	key := trust.Keys[document.KeyID]
	if len(key) != ed25519.PublicKeySize {
		return ReleaseMetadata{}, ErrHelperMetadata
	}
	signature, err := base64.StdEncoding.DecodeString(document.Signature)
	if err != nil || len(signature) != ed25519.SignatureSize ||
		!ed25519.Verify(key, canonicalMetadataPayload(document.Metadata), signature) {
		return ReleaseMetadata{}, ErrHelperMetadata
	}
	if err := document.Metadata.Validate(); err != nil {
		return ReleaseMetadata{}, fmt.Errorf("%w: %v", ErrHelperMetadata, err)
	}
	now := time.Now()
	if trust.Now != nil {
		now = trust.Now()
	}
	if document.Metadata.ExpiresAtUnix <= now.Unix() {
		return ReleaseMetadata{}, fmt.Errorf("%w: %w", ErrHelperMetadata, ErrHelperMetadataExpired)
	}
	if document.Metadata.Sequence < trust.MinimumSequence {
		return ReleaseMetadata{}, fmt.Errorf("%w: %w", ErrHelperMetadata, ErrHelperMetadataRollback)
	}
	if trust.Sequences == nil {
		return ReleaseMetadata{}, fmt.Errorf("%w: metadata sequence store is required", ErrHelperMetadata)
	}
	if err := trust.Sequences.Accept(document.Metadata.Sequence); err != nil {
		return ReleaseMetadata{}, fmt.Errorf("%w: %w", ErrHelperMetadata, err)
	}
	return document.Metadata, nil
}

func canonicalMetadataPayload(metadata ReleaseMetadata) []byte {
	artifacts := slices.Clone(metadata.Artifacts)
	sort.Slice(artifacts, func(i, j int) bool {
		return artifactIdentity(artifacts[i]) < artifactIdentity(artifacts[j])
	})
	payload, _ := json.Marshal(ReleaseMetadata{
		Sequence:      metadata.Sequence,
		ExpiresAtUnix: metadata.ExpiresAtUnix,
		Artifacts:     artifacts,
	})
	return payload
}

func (metadata ReleaseMetadata) Validate() error {
	if metadata.Sequence == 0 || metadata.ExpiresAtUnix <= 0 {
		return errors.New("invalid metadata sequence or expiry")
	}
	if len(metadata.Artifacts) == 0 || len(metadata.Artifacts) > 32 {
		return errors.New("invalid artifact count")
	}
	seen := map[string]bool{}
	platforms := map[string]bool{}
	platformCount := map[string]int{}
	current := map[string]int{}
	for _, artifact := range metadata.Artifacts {
		if err := artifact.Validate(); err != nil {
			return err
		}
		identity := artifactIdentity(artifact)
		if seen[identity] {
			return errors.New("duplicate artifact")
		}
		seen[identity] = true
		platform := artifact.OS + "/" + artifact.Arch
		platforms[platform] = true
		platformCount[platform]++
		if platformCount[platform] > 2 {
			return fmt.Errorf("%s exceeds the current-plus-previous support window", platform)
		}
		if artifact.Current {
			current[platform]++
		}
	}
	for platform := range platforms {
		count := current[platform]
		if count != 1 {
			return fmt.Errorf("%s has %d current artifacts", platform, count)
		}
	}
	if len(current) == 0 {
		return errors.New("metadata has no current artifact")
	}
	return nil
}

// HelperPlatformArgs builds the fixed, bounded platform probe. Unlike helper
// execution, it contains no remote path or caller-provided command text.
func HelperPlatformArgs(alias string, connectTimeoutSeconds int) ([]string, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	if connectTimeoutSeconds <= 0 {
		connectTimeoutSeconds = int(DefaultConnectTimeout.Seconds())
	}
	return []string{
		"-T",
		"-o", "BatchMode=yes",
		"-o", "ConnectTimeout=" + strconv.Itoa(connectTimeoutSeconds),
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=" + controlSocketPath(),
		"-o", "ControlPersist=" + defaultControlPersist,
		alias,
		"uname -s; uname -m; id -u",
	}, nil
}

func (artifact Artifact) Validate() error {
	if !helperVersionPattern.MatchString(artifact.Version) || artifact.Size <= 0 || artifact.Size > MaxHelperArtifactBytes ||
		len(artifact.SHA256) != sha256.Size*2 || !validSHA256(artifact.SHA256) {
		return errors.New("invalid artifact identity")
	}
	platform, err := normalizePlatform(artifact.OS, artifact.Arch)
	if err != nil || platform.OS != artifact.OS || platform.Arch != artifact.Arch {
		return errors.New("invalid artifact platform")
	}
	if !supports(artifact.Protocol, remoteprotocol.ProtocolVersion) ||
		!supports(artifact.Schema, remotefacts.SchemaVersion) {
		return errors.New("artifact does not support this protocol/schema")
	}
	return nil
}

func validSHA256(value string) bool {
	_, err := hex.DecodeString(value)
	return err == nil
}

func validMetadataID(value string) bool {
	return len(value) > 0 && len(value) <= 64 && helperVersionPattern.MatchString(value)
}

func artifactIdentity(artifact Artifact) string {
	return artifact.Version + "/" + artifact.OS + "/" + artifact.Arch
}

func supports(range_ remoteprotocol.VersionRange, version int) bool {
	return range_.Min > 0 && range_.Max >= range_.Min && range_.Min <= version && version <= range_.Max
}

func (metadata ReleaseMetadata) Select(platform Platform) (Artifact, error) {
	for _, artifact := range metadata.Artifacts {
		if artifact.OS == platform.OS && artifact.Arch == platform.Arch && artifact.Current {
			if artifact.Revoked {
				return Artifact{}, ErrHelperRevoked
			}
			return artifact, nil
		}
	}
	return Artifact{}, fmt.Errorf("%w: %s/%s", ErrUnsupportedHelperPlatform, platform.OS, platform.Arch)
}

func helperPath(version string) (string, error) {
	if !helperVersionPattern.MatchString(version) {
		return "", ErrUnknownHelperPath
	}
	return HelperInstallBase + "/" + version + "/" + HelperFileName, nil
}

func helperTemporaryPath(version string) (string, error) {
	path, err := helperPath(version)
	if err != nil {
		return "", err
	}
	return path + ".new", nil
}

// RemoteFile is evidence returned by a secure remote endpoint.  The endpoint
// obtains it through no-follow, descriptor-relative inspection, both before
// and after activation; callers must not substitute pathname checks.
type RemoteFile struct {
	Path    string
	Size    int64
	SHA256  string
	Mode    fs.FileMode
	UID     uint32
	Regular bool
}

// InstallRequest contains only app-derived paths and locally verified bytes.
// Temporary is an exact sibling of Destination; it is not caller supplied.
type InstallRequest struct {
	Artifact    Artifact
	Bytes       []byte
	Destination string
	Temporary   string
	OwnerUID    uint32
}

// LifecycleRemote is implemented by the SSH/transfer boundary.  Every path
// argument has already passed helperPath. Implementations MUST use no-follow,
// directory-relative operations rooted at HelperInstallBase; reject symlinked,
// foreign-owned, or group/world-writable components; upload with O_EXCL; and
// atomically rename Temporary to Destination only after verifying the exact
// bytes. RemoveExact must remove only Destination (and an empty exact version
// directory), never the base or unrelated versions.
type LifecycleRemote interface {
	ProbePlatform(context.Context) (Platform, error)
	Inspect(context.Context, string) (RemoteFile, error)
	Install(context.Context, InstallRequest) (RemoteFile, error)
	RemoveExact(context.Context, string) error
	Capabilities(context.Context, string) (remoteprotocol.Capabilities, error)
}

type Consent struct {
	Install bool
	Upgrade bool
}

// ArtifactLoader is invoked only after authenticated metadata, platform
// selection, inspection, and the relevant user consent establish that an
// install or upgrade is actually required.
type ArtifactLoader func(context.Context, Artifact) ([]byte, error)

type LifecycleState string

const (
	LifecycleSFTP              LifecycleState = "sftp"
	LifecycleReady             LifecycleState = "ready"
	LifecycleDeprecated        LifecycleState = "deprecated"
	LifecycleUpgradeRequired   LifecycleState = "upgrade_required"
	LifecycleUnsupported       LifecycleState = "unsupported"
	LifecycleRevoked           LifecycleState = "revoked"
	LifecycleVerificationError LifecycleState = "verification_failed"
)

// LifecycleResult is intentionally transport-neutral for T05. SFTP is always
// usable when Ready is false, and CanExecute is false for revoked/incompatible
// or unverified files, so callers never accidentally execute one of them.
type LifecycleResult struct {
	State      LifecycleState
	Path       string
	Artifact   Artifact
	CanExecute bool
	Fallback   bool
	Reused     bool
	Reason     error
}

type compatibleHelper struct {
	path     string
	artifact Artifact
}

type Lifecycle struct {
	Remote LifecycleRemote
	Trust  TrustStore
}

// VerifyExecution revalidates all executable-file invariants immediately
// before a cached helper target is used. Discovery establishes which signed
// artifact is allowed; this check ensures the pathname still names those exact
// bytes with the expected owner and mode.
func (lifecycle Lifecycle) VerifyExecution(ctx context.Context, path string, artifact Artifact) error {
	if lifecycle.Remote == nil {
		return ErrHelperVerification
	}
	platform, err := lifecycle.Remote.ProbePlatform(ctx)
	if err != nil {
		return err
	}
	normalized, err := normalizePlatform(platform.OS, platform.Arch)
	if err != nil || normalized.OS != artifact.OS || normalized.Arch != artifact.Arch {
		return ErrHelperVerification
	}
	file, err := lifecycle.Remote.Inspect(ctx, path)
	if err != nil {
		return err
	}
	return verifyRemoteFile(file, path, artifact, platform.UID)
}

// Setup authenticates metadata before platform lookup or any remote mutation.
// New hosts require installation consent; a recorded helper owner can consent
// to its replacement when the app updates.
// Setup is retained for focused lifecycle tests. Production callers must use
// SetupWithLoader so an arm64 host never downloads amd64 bytes (or vice versa)
// before its platform is known.
func (lifecycle Lifecycle) Setup(ctx context.Context, document SignedReleaseMetadata, artifactBytes []byte, consent Consent) LifecycleResult {
	return lifecycle.SetupWithLoader(ctx, document, consent, func(context.Context, Artifact) ([]byte, error) {
		return artifactBytes, nil
	})
}

func (lifecycle Lifecycle) SetupWithLoader(ctx context.Context, document SignedReleaseMetadata, consent Consent, load ArtifactLoader) LifecycleResult {
	metadata, err := lifecycle.Trust.Verify(document)
	if err != nil {
		return lifecycleFailure(LifecycleVerificationError, err)
	}
	if lifecycle.Remote == nil {
		return lifecycleFailure(LifecycleVerificationError, ErrHelperInstallation)
	}
	platform, err := lifecycle.Remote.ProbePlatform(ctx)
	if err != nil {
		if errors.Is(err, ErrUnsupportedHelperPlatform) {
			return lifecycleFailure(LifecycleUnsupported, err)
		}
		return lifecycleFailure(LifecycleSFTP, err)
	}
	normalized, err := normalizePlatform(platform.OS, platform.Arch)
	if err != nil {
		return lifecycleFailure(LifecycleUnsupported, err)
	}
	normalized.UID = platform.UID
	platform = normalized
	artifact, err := metadata.Select(platform)
	if err != nil {
		state := LifecycleUnsupported
		if errors.Is(err, ErrHelperRevoked) {
			state = LifecycleRevoked
		}
		return lifecycleFailure(state, err)
	}
	target, _ := helperPath(artifact.Version)
	current, currentErr := lifecycle.Remote.Inspect(ctx, target)
	if currentErr == nil {
		if err := verifyRemoteFile(current, target, artifact, platform.UID); err == nil {
			result := lifecycle.capabilityResult(ctx, target, artifact, LifecycleReady)
			result.Reused = result.CanExecute
			if result.CanExecute || !errors.Is(result.Reason, ErrHelperIncompatible) {
				return result
			}
			currentErr = ErrHelperIncompatible
		} else {
			currentErr = ErrHelperVerification
		}
	} else if !errors.Is(currentErr, fs.ErrNotExist) {
		return lifecycleFailure(LifecycleSFTP, currentErr)
	}

	// A non-current known artifact may remain executable only if it appears in
	// this signed manifest, is non-revoked, and verifies exactly.  T05 can show
	// this as a visible upgrade prompt while continuing useful collection.
	var previous *compatibleHelper
	for _, older := range metadata.Artifacts {
		if older.OS != platform.OS || older.Arch != platform.Arch || older.Version == artifact.Version {
			continue
		}
		path, _ := helperPath(older.Version)
		file, err := lifecycle.Remote.Inspect(ctx, path)
		if err != nil {
			continue
		}
		if older.Revoked {
			// A revoked previous helper is never executed, but it must not block
			// installation of the authenticated non-revoked current artifact.
			continue
		}
		if verifyRemoteFile(file, path, older, platform.UID) == nil {
			candidate := lifecycle.capabilityResult(ctx, path, older, LifecycleDeprecated)
			if !candidate.CanExecute {
				if !errors.Is(candidate.Reason, ErrHelperIncompatible) {
					return candidate
				}
				continue
			}
			previous = &compatibleHelper{path: path, artifact: older}
			if consent.Upgrade {
				break
			}
			candidate.Reason = ErrHelperUpgradeRequired
			return candidate
		}
	}

	// A missing target is an initial installation. A target that exists but
	// failed verification is a repair/upgrade and needs fresh upgrade consent.
	initialInstall := errors.Is(currentErr, fs.ErrNotExist) && previous == nil
	if (initialInstall && !consent.Install) || (!initialInstall && !consent.Upgrade) {
		return lifecycleFailure(LifecycleUpgradeRequired, ErrHelperConsentRequired)
	}
	if load == nil {
		return lifecycleFailure(LifecycleVerificationError, ErrHelperArtifact)
	}
	artifactBytes, err := load(ctx, artifact)
	if err != nil {
		return lifecycleFailure(LifecycleVerificationError, fmt.Errorf("%w: download helper artifact", ErrHelperArtifact))
	}
	if err := verifyLocalArtifact(artifact, artifactBytes); err != nil {
		return lifecycleFailure(LifecycleVerificationError, err)
	}
	temporary, _ := helperTemporaryPath(artifact.Version)
	installed, err := lifecycle.Remote.Install(ctx, InstallRequest{
		Artifact: artifact, Bytes: slices.Clone(artifactBytes), Destination: target,
		Temporary: temporary, OwnerUID: platform.UID,
	})
	if err != nil {
		if previous != nil {
			return rollbackResult(*previous, err)
		}
		return lifecycleFailure(LifecycleSFTP, classifyLifecycleInstallError(err))
	}
	if err := verifyRemoteFile(installed, target, artifact, platform.UID); err != nil {
		_ = lifecycle.Remote.RemoveExact(ctx, target)
		if previous != nil {
			return rollbackResult(*previous, err)
		}
		return lifecycleFailure(LifecycleVerificationError, err)
	}
	result := lifecycle.capabilityResult(ctx, target, artifact, LifecycleReady)
	if result.CanExecute {
		return result
	}
	_ = lifecycle.Remote.RemoveExact(ctx, target)
	if previous != nil {
		return rollbackResult(*previous, result.Reason)
	}
	return result
}

func (lifecycle Lifecycle) capabilityResult(ctx context.Context, path string, artifact Artifact, state LifecycleState) LifecycleResult {
	capabilities, err := lifecycle.Remote.Capabilities(ctx, path)
	if err != nil {
		if errors.Is(err, ErrHelperIncompatible) {
			return lifecycleFailure(LifecycleUpgradeRequired, ErrHelperIncompatible)
		}
		return lifecycleFailure(LifecycleSFTP, err)
	}
	if capabilities.Validate() != nil || !capabilities.Compatible() ||
		capabilities.OS != artifact.OS || capabilities.Arch != artifact.Arch ||
		!supports(artifact.Protocol, remoteprotocol.ProtocolVersion) || !supports(artifact.Schema, remotefacts.SchemaVersion) {
		return lifecycleFailure(LifecycleUpgradeRequired, ErrHelperIncompatible)
	}
	return LifecycleResult{State: state, Path: path, Artifact: artifact, CanExecute: true}
}

func rollbackResult(previous compatibleHelper, cause error) LifecycleResult {
	return LifecycleResult{
		State: LifecycleDeprecated, Path: previous.path, Artifact: previous.artifact,
		CanExecute: true, Reason: fmt.Errorf("%w: %v", ErrHelperRollback, cause),
	}
}

func lifecycleFailure(state LifecycleState, reason error) LifecycleResult {
	return LifecycleResult{State: state, Fallback: true, Reason: reason}
}

func classifyLifecycleInstallError(err error) error {
	switch {
	case errors.Is(err, ErrHelperNoExec), errors.Is(err, ErrHelperRollback):
		return err
	default:
		return fmt.Errorf("%w: %w", ErrHelperInstallation, err)
	}
}

func verifyLocalArtifact(artifact Artifact, content []byte) error {
	if int64(len(content)) != artifact.Size || digest(content) != artifact.SHA256 {
		return ErrHelperArtifact
	}
	return verifyELF(content, artifact.Arch)
}

func verifyRemoteFile(file RemoteFile, path string, artifact Artifact, uid uint32) error {
	if file.Path != path || !file.Regular || file.Size != artifact.Size || file.SHA256 != artifact.SHA256 ||
		file.UID != uid || file.Mode.Perm() != 0o700 || file.Mode&(fs.ModeSymlink|fs.ModeSetuid|fs.ModeSetgid) != 0 {
		return ErrHelperVerification
	}
	return nil
}

func verifyELF(content []byte, arch string) error {
	// ELF's fixed little-endian header prefix is enough for the supported Go
	// binaries.  Parsing it directly also avoids accepting a script merely
	// because it is executable. Linux amd64 and arm64 releases are ELF64 LE.
	if len(content) < 20 || !bytes.Equal(content[:4], []byte{0x7f, 'E', 'L', 'F'}) ||
		content[4] != 2 || content[5] != 1 {
		return ErrHelperArtifact
	}
	type_ := binary.LittleEndian.Uint16(content[16:18])
	if type_ != 2 && type_ != 3 { // ET_EXEC or ET_DYN (PIE)
		return ErrHelperArtifact
	}
	want := uint16(62) // EM_X86_64
	if arch == "arm64" {
		want = 183 // EM_AARCH64
	}
	if binary.LittleEndian.Uint16(content[18:20]) != want {
		return ErrHelperArtifact
	}
	return nil
}

func digest(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:])
}

// Uninstall authenticates the release document and probes the platform before
// deriving an exact known artifact path. Revoked artifacts remain removable.
func (lifecycle Lifecycle) Uninstall(ctx context.Context, document SignedReleaseMetadata, version string) error {
	if lifecycle.Remote == nil {
		return ErrHelperInstallation
	}
	metadata, err := lifecycle.Trust.Verify(document)
	if err != nil {
		return err
	}
	platform, err := lifecycle.Remote.ProbePlatform(ctx)
	if err != nil {
		return err
	}
	normalized, err := normalizePlatform(platform.OS, platform.Arch)
	if err != nil {
		return err
	}
	known := false
	for _, artifact := range metadata.Artifacts {
		if artifact.Version == version && artifact.OS == normalized.OS && artifact.Arch == normalized.Arch {
			known = true
			break
		}
	}
	if !known {
		return ErrUnknownHelperPath
	}
	path, err := helperPath(version)
	if err != nil {
		return err
	}
	return lifecycle.Remote.RemoveExact(ctx, path)
}
