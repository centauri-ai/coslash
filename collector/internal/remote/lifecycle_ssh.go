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
	"strings"
	"sync"

	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/pkg/sftp"
)

const linuxStatVFSNoExec = 0x8

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
	configureProcessGroup(cmd)
	stdout := &boundedCommandOutput{limit: 128, cancel: cancel}
	stderr := &boundedCommandOutput{limit: limits.MaxStderrBytes, cancel: cancel}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		return Platform{}, fmt.Errorf("start helper platform probe: %w", err)
	}
	waited := make(chan error, 1)
	go func() { waited <- cmd.Wait() }()
	var runErr error
	select {
	case runErr = <-waited:
	case <-runCtx.Done():
		terminateProcessGroup(cmd)
		runErr = <-waited
	}
	if runErr != nil {
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
		destination, version, err := resolveLifecyclePath(session.source.Home(), request.Destination)
		if err != nil {
			return err
		}
		temporary := destination + ".new"
		if err := validateLifecycleDirectories(session.client, session.source.Home(), version, request.OwnerUID, true); err != nil {
			return err
		}
		vfs, err := session.client.StatVFS(path.Dir(destination))
		if err != nil {
			return fmt.Errorf("inspect helper filesystem: %w", err)
		}
		if vfs.Flag&linuxStatVFSNoExec != 0 {
			return ErrHelperNoExec
		}
		if err := removeStaleLifecycleTemporary(session.client, temporary, request.OwnerUID); err != nil {
			return err
		}
		file, err := session.client.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL)
		if err != nil {
			return fmt.Errorf("create helper temporary: %w", err)
		}
		if err := writeAndCloseLifecycleArtifact(file, request.Bytes, temporary, session.client.Remove); err != nil {
			return err
		}
		temporaryInfo, err := inspectLifecycleFile(session.client, temporary, request.Temporary)
		if err != nil {
			_ = session.client.Remove(temporary)
			return err
		}
		if err := verifyRemoteFile(temporaryInfo, request.Temporary, request.Artifact, request.OwnerUID); err != nil {
			_ = session.client.Remove(temporary)
			return err
		}
		if existing, err := session.client.Lstat(destination); err == nil {
			if err := validateLifecycleEntry(existing, request.OwnerUID, false); err != nil {
				_ = session.client.Remove(temporary)
				return err
			}
		} else if !errors.Is(err, fs.ErrNotExist) {
			_ = session.client.Remove(temporary)
			return err
		}
		if err := activateLifecycleTemporary(session.client.PosixRename, session.client.Remove, temporary, destination); err != nil {
			return err
		}
		installed, err = inspectLifecycleFile(session.client, destination, request.Destination)
		return err
	})
	return installed, err
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
	return path.Join(home, ".coslash/helpers", version, HelperFileName), version, nil
}

func validateLifecycleDirectories(client *sftp.Client, home, version string, uid uint32, create bool) error {
	directories := []string{home}
	current := home
	// Keep the executable beneath a dedicated, mode-0700 tree. Common Linux
	// homes may have a group-writable ~/.local; rejecting that ancestor is safe,
	// but trying to chmod it would unexpectedly mutate unrelated user data.
	for _, component := range []string{".coslash", "helpers", version} {
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

type lifecycleArtifactFile interface {
	io.Writer
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

func writeLifecycleArtifact(file lifecycleArtifactFile, content []byte) error {
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

// writeAndCloseLifecycleArtifact is the failure-atomic temporary-file step of
// installation. The callback makes cleanup testable independently from an SSH
// server and ensures an interrupted write, sync, or close cannot leave a
// candidate executable behind.
func writeAndCloseLifecycleArtifact(
	file lifecycleArtifactFile,
	content []byte,
	temporary string,
	remove func(string) error,
) error {
	if err := errors.Join(writeLifecycleArtifact(file, content), file.Close()); err != nil {
		return fmt.Errorf("write helper temporary: %w", errors.Join(err, remove(temporary)))
	}
	return nil
}

// activateLifecycleTemporary keeps a failed atomic rename from leaving a
// verified-looking temporary executable behind for a later operation.
func activateLifecycleTemporary(
	rename func(string, string) error,
	remove func(string) error,
	temporary, destination string,
) error {
	if err := rename(temporary, destination); err != nil {
		return fmt.Errorf("activate helper atomically: %w", errors.Join(err, remove(temporary)))
	}
	return nil
}

type boundedCommandOutput struct {
	mu       sync.Mutex
	buffer   bytes.Buffer
	limit    int
	overflow bool
	cancel   context.CancelFunc
}

func (output *boundedCommandOutput) Write(data []byte) (int, error) {
	output.mu.Lock()
	defer output.mu.Unlock()
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

func (output *boundedCommandOutput) String() string {
	output.mu.Lock()
	defer output.mu.Unlock()
	return output.buffer.String()
}
