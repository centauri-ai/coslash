//go:build linux

// Package remoteinstall performs the one-time helper activation on the remote
// Linux host.  It intentionally uses descriptor-relative operations throughout:
// a hostile process may rename a directory after it has been opened, but cannot
// redirect operations performed through that descriptor.
package remoteinstall

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"regexp"

	"golang.org/x/sys/unix"
)

const (
	fileName = "coslash-helper"
	tmpName  = fileName + ".new"
)

var (
	ErrNoExec = errors.New("helper install directory is not executable")
	version   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,127}$`)
)

// Install copies this running, already authenticated helper into its versioned
// location below home. The source is /proc/self/exe rather than its launch path,
// so replacing the staging name after exec cannot change the copied bytes.
func Install(home, release, expectedSHA256 string) error {
	source, err := os.Open("/proc/self/exe")
	if err != nil {
		return fmt.Errorf("open running helper: %w", err)
	}
	defer source.Close()
	return install(source, home, release, expectedSHA256, nil)
}

func install(source *os.File, home, release, expectedSHA256 string, beforeWrite func() error) error {
	if !version.MatchString(release) {
		return errors.New("invalid helper version")
	}
	if len(expectedSHA256) != sha256.Size*2 {
		return errors.New("invalid helper digest")
	}
	if _, err := hex.DecodeString(expectedSHA256); err != nil {
		return errors.New("invalid helper digest")
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, source); err != nil {
		return fmt.Errorf("hash running helper: %w", err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expectedSHA256 {
		return errors.New("running helper digest does not match staged artifact")
	}
	homeFD, err := unix.Open(home, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if err != nil {
		return fmt.Errorf("open helper home: %w", err)
	}
	defer unix.Close(homeFD)
	if err := secureDirectory(homeFD, uint32(os.Geteuid())); err != nil {
		return fmt.Errorf("verify helper home: %w", err)
	}

	directoryFD := homeFD
	for _, component := range []string{".coslash", "helpers", release} {
		next, err := openOrCreateDirectory(directoryFD, component, uint32(os.Geteuid()))
		if directoryFD != homeFD {
			unix.Close(directoryFD)
		}
		if err != nil {
			return err
		}
		directoryFD = next
	}
	defer unix.Close(directoryFD)

	var statfs unix.Statfs_t
	if err := unix.Fstatfs(directoryFD, &statfs); err != nil {
		return fmt.Errorf("inspect helper filesystem: %w", err)
	}
	if statfs.Flags&unix.ST_NOEXEC != 0 {
		return ErrNoExec
	}
	if beforeWrite != nil {
		if err := beforeWrite(); err != nil {
			return err
		}
	}
	if err := removeTemporary(directoryFD, uint32(os.Geteuid())); err != nil {
		return err
	}
	temporaryFD, err := unix.Openat(directoryFD, tmpName, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0o700)
	if err != nil {
		return fmt.Errorf("create helper temporary: %w", err)
	}
	temporary := os.NewFile(uintptr(temporaryFD), tmpName)
	defer temporary.Close()

	if _, err := source.Seek(0, io.SeekStart); err != nil {
		return fmt.Errorf("rewind running helper: %w", err)
	}
	if _, err := io.Copy(temporary, source); err != nil {
		return fmt.Errorf("write helper temporary: %w", err)
	}
	if err := unix.Fchmod(temporaryFD, 0o700); err != nil {
		return fmt.Errorf("secure helper temporary: %w", err)
	}
	if err := unix.Fsync(temporaryFD); err != nil {
		return fmt.Errorf("sync helper temporary: %w", err)
	}
	if err := secureRegular(temporaryFD, uint32(os.Geteuid())); err != nil {
		return fmt.Errorf("verify helper temporary: %w", err)
	}
	if err := validateExistingDestination(directoryFD, uint32(os.Geteuid())); err != nil {
		return err
	}
	if err := unix.Renameat(directoryFD, tmpName, directoryFD, fileName); err != nil {
		return fmt.Errorf("activate helper atomically: %w", err)
	}
	// A successful rename is the commit point. The digest check above binds the
	// copied bytes to the authenticated artifact that launched this installer.
	return nil
}

func openOrCreateDirectory(parent int, name string, uid uint32) (int, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	if errors.Is(err, unix.ENOENT) {
		if err := unix.Mkdirat(parent, name, 0o700); err != nil && !errors.Is(err, unix.EEXIST) {
			return -1, fmt.Errorf("create helper directory: %w", err)
		}
		fd, err = unix.Openat(parent, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
	}
	if err != nil {
		return -1, fmt.Errorf("open helper directory: %w", err)
	}
	if err := secureDirectory(fd, uid); err != nil {
		unix.Close(fd)
		return -1, fmt.Errorf("verify helper directory: %w", err)
	}
	return fd, nil
}

func removeTemporary(directoryFD int, uid uint32) error {
	var stat unix.Stat_t
	err := unix.Fstatat(directoryFD, tmpName, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect helper temporary: %w", err)
	}
	if !isRegular(stat) || stat.Uid != uid || stat.Mode&0o022 != 0 {
		return errors.New("unsafe helper temporary")
	}
	if err := unix.Unlinkat(directoryFD, tmpName, 0); err != nil {
		return fmt.Errorf("remove stale helper temporary: %w", err)
	}
	return nil
}

func validateExistingDestination(directoryFD int, uid uint32) error {
	var stat unix.Stat_t
	err := unix.Fstatat(directoryFD, fileName, &stat, unix.AT_SYMLINK_NOFOLLOW)
	if errors.Is(err, unix.ENOENT) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect existing helper: %w", err)
	}
	if !isRegular(stat) || stat.Uid != uid || stat.Mode&0o022 != 0 {
		return errors.New("unsafe existing helper")
	}
	return nil
}

func secureDirectory(fd int, uid uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if stat.Mode&unix.S_IFMT != unix.S_IFDIR || stat.Uid != uid || stat.Mode&0o022 != 0 {
		return errors.New("unsafe helper directory")
	}
	return nil
}

func secureRegular(fd int, uid uint32) error {
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return err
	}
	if !isRegular(stat) || stat.Uid != uid || stat.Mode&0o022 != 0 {
		return errors.New("unsafe helper file")
	}
	return nil
}

func isRegular(stat unix.Stat_t) bool { return stat.Mode&unix.S_IFMT == unix.S_IFREG }
