package remote

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"

	"github.com/centauri-ai/coslash/collector/internal/remoteinstall"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/pkg/sftp"
)

// SSHLifecycleRemote is the production helper lifecycle adapter. It uses the
// same bounded system-SSH and SFTP transport as remote collection.
type SSHLifecycleRemote struct {
	Alias   string
	Options OpenOptions
}

func NewSSHLifecycleRemote(alias string, options OpenOptions) (*SSHLifecycleRemote, error) {
	if !aliasPattern.MatchString(alias) {
		return nil, ErrInvalidAlias
	}
	return &SSHLifecycleRemote{Alias: alias, Options: options}, nil
}

func (remote *SSHLifecycleRemote) ProbePlatform(ctx context.Context) (Platform, error) {
	limits := remote.Options.Limits.withDefaults()
	args, err := HelperPlatformArgs(remote.Alias, int(limits.ConnectTimeout.Seconds()))
	if err != nil {
		return Platform{}, err
	}
	if remote.Options.command == nil {
		if err := ensureControlMaster(ctx, remote.Alias, remote.Options); err != nil {
			return Platform{}, err
		}
	}
	bin := remote.Options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	command := remote.Options.command
	if command == nil {
		command = exec.CommandContext
	}
	runCtx, cancel := context.WithTimeout(ctx, limits.ConnectTimeout)
	defer cancel()
	cmd := command(runCtx, bin, args...)
	stdout := &boundedCommandOutput{limit: 128, cancel: cancel}
	stderr := &boundedCommandOutput{limit: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if stdout.overflow {
			return Platform{}, fmt.Errorf("%w: platform probe output too long", ErrUnsupportedHelperPlatform)
		}
		if stderr.overflow {
			return Platform{}, ErrStderrLimit
		}
		if runCtx.Err() != nil {
			return Platform{}, runCtx.Err()
		}
		return Platform{}, wrapSSHError(fmt.Errorf("probe helper platform: %w", err), stderr.String())
	}
	if stdout.overflow {
		return Platform{}, fmt.Errorf("%w: platform probe output too long", ErrUnsupportedHelperPlatform)
	}
	return ParsePlatformProbe(stdout.String())
}

func (remote *SSHLifecycleRemote) Inspect(ctx context.Context, requested string) (RemoteFile, error) {
	var result RemoteFile
	err := remote.withSession(ctx, func(session *Session) error {
		absolute, version, err := resolveLifecyclePath(session.source.Home(), requested)
		if err != nil {
			return err
		}
		if err := validateLifecycleDirectories(session.client, session.source.Home(), version, 0, false); err != nil {
			return err
		}
		result, err = inspectLifecycleFile(session.client, absolute, requested)
		return err
	})
	return result, err
}

func (remote *SSHLifecycleRemote) Install(ctx context.Context, request InstallRequest) (RemoteFile, error) {
	if err := verifyLocalArtifact(request.Artifact, request.Bytes); err != nil {
		return RemoteFile{}, err
	}
	wantDestination, err := helperPath(request.Artifact.Version)
	if err != nil || request.Destination != wantDestination || request.Temporary != wantDestination+".new" {
		return RemoteFile{}, ErrUnknownHelperPath
	}
	var installed RemoteFile
	err = remote.withSession(ctx, func(session *Session) error {
		_, version, err := resolveLifecyclePath(session.source.Home(), request.Destination)
		if err != nil {
			return err
		}
		home := session.source.Home()
		if err := validateLifecycleHome(session.client, home, request.OwnerUID); err != nil {
			return err
		}
		// The staging path is directly below the already-validated home directory.
		// Activation itself happens in the staged, authenticated static helper using
		// descriptor-relative openat/renameat calls; SFTP has no such primitive.
		staging := path.Join(home, "."+HelperFileName+"-"+version+".stage")
		if err := removeStaleLifecycleTemporary(session.client, staging, request.OwnerUID); err != nil {
			return err
		}
		file, err := session.client.OpenFile(staging, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return fmt.Errorf("create helper staging file: %w", err)
		}
		writeErr := writeLifecycleArtifact(file, request.Bytes)
		closeErr := file.Close()
		if err := errors.Join(writeErr, closeErr); err != nil {
			_ = session.client.Remove(staging)
			return fmt.Errorf("write helper staging file: %w", err)
		}
		stagingInfo, err := inspectLifecycleFile(session.client, staging, staging)
		if err != nil {
			_ = session.client.Remove(staging)
			return err
		}
		if err := verifyRemoteFile(stagingInfo, staging, request.Artifact, request.OwnerUID); err != nil {
			_ = session.client.Remove(staging)
			return err
		}
		installErr := remote.runStagedInstaller(ctx, staging, home, version, request.Artifact.SHA256)
		_ = session.client.Remove(staging)
		if errors.Is(installErr, remoteinstall.ErrNoExec) {
			return ErrHelperNoExec
		}
		if installErr != nil {
			return installErr
		}
		installed = RemoteFile{Path: request.Destination, Size: request.Artifact.Size,
			SHA256: request.Artifact.SHA256, Mode: 0o700, UID: request.OwnerUID, Regular: true}
		return nil
	})
	return installed, err
}

func (remote *SSHLifecycleRemote) runStagedInstaller(ctx context.Context, staging, home, version, sha256 string) error {
	limits := remote.Options.Limits.withDefaults()
	if remote.Options.command == nil {
		if err := ensureControlMaster(ctx, remote.Alias, remote.Options); err != nil {
			return err
		}
	}
	bin := remote.Options.SSHBin
	if bin == "" {
		bin = "ssh"
	}
	command := remote.Options.command
	if command == nil {
		command = exec.CommandContext
	}
	runCtx, cancel := context.WithTimeout(ctx, limits.Deadline)
	defer cancel()
	args := []string{
		"-T", "-o", "BatchMode=yes", "-o", "ConnectTimeout=" + strconv.Itoa(int(limits.ConnectTimeout.Seconds())),
		"-o", "ControlMaster=auto", "-o", "ControlPath=" + controlSocketPath(), "-o", "ControlPersist=" + defaultControlPersist,
		remote.Alias, shellQuote(staging) + " install " + shellQuote(home) + " " + shellQuote(version) + " " + shellQuote(sha256),
	}
	cmd := command(runCtx, bin, args...)
	stdout := &boundedCommandOutput{limit: 128, cancel: cancel}
	stderr := &boundedCommandOutput{limit: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Run(); err != nil {
		if stderr.overflow {
			return ErrStderrLimit
		}
		if runCtx.Err() != nil {
			return runCtx.Err()
		}
		if strings.Contains(stderr.String(), remoteinstall.ErrNoExec.Error()) {
			return remoteinstall.ErrNoExec
		}
		return wrapSSHError(fmt.Errorf("run secure helper installer: %w", err), stderr.String())
	}
	if stdout.overflow || stderr.overflow {
		return ErrStderrLimit
	}
	return nil
}

func (remote *SSHLifecycleRemote) RemoveExact(ctx context.Context, requested string) error {
	return remote.withSession(ctx, func(session *Session) error {
		absolute, version, err := resolveLifecyclePath(session.source.Home(), requested)
		if err != nil {
			return err
		}
		if err := validateLifecycleDirectories(session.client, session.source.Home(), version, 0, false); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil
			}
			return err
		}
		info, err := session.client.Lstat(absolute)
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		if err != nil {
			return err
		}
		ownerUID, err := lifecycleHomeUID(session.client, session.source.Home())
		if err != nil {
			return err
		}
		if err := validateLifecycleEntry(info, ownerUID, false); err != nil {
			return err
		}
		if err := session.client.Remove(absolute); err != nil {
			return fmt.Errorf("remove exact helper: %w", err)
		}
		if err := session.client.RemoveDirectory(path.Dir(absolute)); err != nil && !strings.Contains(strings.ToLower(err.Error()), "not empty") {
			return fmt.Errorf("remove empty helper version directory: %w", err)
		}
		return nil
	})
}

func (remote *SSHLifecycleRemote) Capabilities(ctx context.Context, helperPath string) (remoteprotocol.Capabilities, error) {
	capabilities, _, err := HelperCapabilities(ctx, remote.Alias, helperPath, remote.Options)
	return capabilities, err
}

func (remote *SSHLifecycleRemote) withSession(ctx context.Context, operation func(*Session) error) error {
	options := remote.Options
	options.lifecycleOnly = true
	session, err := OpenSession(ctx, remote.Alias, options)
	if err != nil {
		return err
	}
	operationErr := operation(session)
	closeErr := session.Close()
	return errors.Join(operationErr, closeErr)
}

func resolveLifecyclePath(home, requested string) (string, string, error) {
	prefix := HelperInstallBase + "/"
	if !strings.HasPrefix(requested, prefix) || strings.HasSuffix(requested, ".new") {
		return "", "", ErrUnknownHelperPath
	}
	remaining := strings.TrimPrefix(requested, prefix)
	version, fileName, ok := strings.Cut(remaining, "/")
	if !ok || fileName != HelperFileName || !helperVersionPattern.MatchString(version) {
		return "", "", ErrUnknownHelperPath
	}
	return path.Join(home, ".local/lib/coslash/helpers", version, HelperFileName), version, nil
}

func validateLifecycleDirectories(client *sftp.Client, home, version string, uid uint32, create bool) error {
	directories := []string{home}
	current := home
	for _, component := range []string{".local", "lib", "coslash", "helpers", version} {
		current = path.Join(current, component)
		directories = append(directories, current)
	}
	for index, directory := range directories {
		info, err := client.Lstat(directory)
		if errors.Is(err, fs.ErrNotExist) && create && index > 0 {
			if err := client.Mkdir(directory); err != nil {
				return fmt.Errorf("create helper directory: %w", err)
			}
			if err := client.Chmod(directory, 0o700); err != nil {
				return fmt.Errorf("secure helper directory: %w", err)
			}
			info, err = client.Lstat(directory)
		}
		if err != nil {
			return err
		}
		if index == 0 && uid == 0 {
			stat, ok := info.Sys().(*sftp.FileStat)
			if !ok {
				return ErrHelperVerification
			}
			uid = stat.UID
		}
		if err := validateLifecycleEntry(info, uid, true); err != nil {
			return fmt.Errorf("unsafe helper directory %s: %w", directory, err)
		}
	}
	return nil
}

func validateLifecycleHome(client *sftp.Client, home string, uid uint32) error {
	info, err := client.Lstat(home)
	if err != nil {
		return err
	}
	if err := validateLifecycleEntry(info, uid, true); err != nil {
		return fmt.Errorf("unsafe helper home %s: %w", home, err)
	}
	return nil
}

func lifecycleHomeUID(client *sftp.Client, home string) (uint32, error) {
	info, err := client.Lstat(home)
	if err != nil {
		return 0, err
	}
	stat, ok := info.Sys().(*sftp.FileStat)
	if !ok {
		return 0, ErrHelperVerification
	}
	return stat.UID, nil
}

func validateLifecycleEntry(info os.FileInfo, uid uint32, directory bool) error {
	if info.Mode()&fs.ModeSymlink != 0 || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) || info.Mode().Perm()&0o022 != 0 {
		return ErrHelperVerification
	}
	stat, ok := info.Sys().(*sftp.FileStat)
	if !ok || (uid != 0 && stat.UID != uid) {
		return ErrHelperVerification
	}
	return nil
}

func inspectLifecycleFile(client *sftp.Client, absolute, reported string) (RemoteFile, error) {
	info, err := client.Lstat(absolute)
	if err != nil {
		return RemoteFile{}, err
	}
	if err := validateLifecycleEntry(info, 0, false); err != nil {
		return RemoteFile{}, err
	}
	file, err := client.Open(absolute)
	if err != nil {
		return RemoteFile{}, err
	}
	hash := sha256.New()
	_, copyErr := io.CopyN(hash, file, info.Size()+1)
	if errors.Is(copyErr, io.EOF) {
		copyErr = nil
	}
	closeErr := file.Close()
	if err := errors.Join(copyErr, closeErr); err != nil {
		return RemoteFile{}, err
	}
	stat, ok := info.Sys().(*sftp.FileStat)
	if !ok {
		return RemoteFile{}, ErrHelperVerification
	}
	return RemoteFile{
		Path: reported, Size: info.Size(), SHA256: fmt.Sprintf("%x", hash.Sum(nil)),
		Mode: info.Mode(), UID: stat.UID, Regular: info.Mode().IsRegular(),
	}, nil
}

func removeStaleLifecycleTemporary(client *sftp.Client, temporary string, uid uint32) error {
	info, err := client.Lstat(temporary)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	if err := validateLifecycleEntry(info, uid, false); err != nil {
		return err
	}
	return client.Remove(temporary)
}

func writeLifecycleArtifact(file *sftp.File, content []byte) error {
	if _, err := io.Copy(file, bytes.NewReader(content)); err != nil {
		return err
	}
	if err := file.Chmod(0o700); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	return nil
}

type boundedCommandOutput struct {
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (output *boundedCommandOutput) Write(data []byte) (int, error) {
	if output.overflow {
		return len(data), nil
	}
	remaining := output.limit - output.buffer.Len()
	if len(data) > remaining {
		if remaining > 0 {
			_, _ = output.buffer.Write(data[:remaining])
		}
		output.overflow = true
		output.cancel()
		return len(data), nil
	}
	_, _ = output.buffer.Write(data)
	return len(data), nil
}

func (output *boundedCommandOutput) String() string { return output.buffer.String() }
