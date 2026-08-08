package runfs

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

const (
	defaultMaxReadBytes  int64       = 8 << 20
	defaultMaxWriteBytes int64       = 8 << 20
	defaultFileMode      os.FileMode = 0o600
	defaultDirMode       os.FileMode = 0o700
)

// ScopeOptions bounds I/O and fixes the modes used for newly-created files and
// directories. Zero values select private, conservative defaults.
type ScopeOptions struct {
	MaxReadBytes  int64
	MaxWriteBytes int64
	FileMode      os.FileMode
	DirMode       os.FileMode
}

// Scope confines filesystem access beneath one canonical directory. The root
// itself may be opened through a symlink (for example macOS /tmp); every path
// component below the canonical root is lstat-checked and symlinks are refused.
type Scope struct {
	root          *os.Root
	canonicalRoot string
	maxReadBytes  int64
	maxWriteBytes int64
	fileMode      os.FileMode
	dirMode       os.FileMode
}

// OpenScope opens an existing directory as a scoped filesystem.
func OpenScope(root string, options ScopeOptions) (*Scope, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: empty root", ErrInvalidPath)
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("canonicalize root: %w", err)
	}
	canonical, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, fmt.Errorf("canonicalize root: %w", err)
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		return nil, fmt.Errorf("inspect root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("%w: root is not a directory", ErrNotRegular)
	}
	opened, err := os.OpenRoot(canonical)
	if err != nil {
		return nil, fmt.Errorf("open root: %w", err)
	}

	maxRead := options.MaxReadBytes
	if maxRead == 0 {
		maxRead = defaultMaxReadBytes
	}
	maxWrite := options.MaxWriteBytes
	if maxWrite == 0 {
		maxWrite = defaultMaxWriteBytes
	}
	fileMode := options.FileMode
	if fileMode == 0 {
		fileMode = defaultFileMode
	}
	dirMode := options.DirMode
	if dirMode == 0 {
		dirMode = defaultDirMode
	}
	if maxRead < 1 || maxWrite < 1 || !validPerm(fileMode) || !validPerm(dirMode) {
		opened.Close()
		return nil, fmt.Errorf("invalid scope options")
	}

	return &Scope{
		root:          opened,
		canonicalRoot: canonical,
		maxReadBytes:  maxRead,
		maxWriteBytes: maxWrite,
		fileMode:      fileMode.Perm(),
		dirMode:       dirMode.Perm(),
	}, nil
}

func validPerm(mode os.FileMode) bool {
	return mode.Perm() != 0 && mode&^os.ModePerm == 0
}

// Close releases the operating-system handle for the scope.
func (s *Scope) Close() error { return s.root.Close() }

// Root returns the canonical root for diagnostics and explicit user-visible
// provenance. Callers must not construct unscoped filesystem operations from it.
func (s *Scope) Root() string { return s.canonicalRoot }

// Resolve validates a relative path and returns its canonical lexical location.
// It does not create the path and refuses every existing symlink below the root.
func (s *Scope) Resolve(name string) (string, error) {
	clean, err := cleanRelative(name)
	if err != nil {
		return "", err
	}
	if err := s.checkComponents(clean, true); err != nil {
		return "", err
	}
	return filepath.Join(s.canonicalRoot, filepath.FromSlash(clean)), nil
}

// MkdirAll creates a scoped directory tree with explicit private modes and
// fsyncs every newly-created directory entry.
func (s *Scope) MkdirAll(ctx context.Context, name string) error {
	clean, err := cleanRelative(name)
	if err != nil {
		return err
	}
	return s.ensureDirectory(ctx, clean)
}

// ReadFile reads a regular, non-symlink file with the scope's configured bound.
func (s *Scope) ReadFile(ctx context.Context, name string) ([]byte, error) {
	return s.readFile(ctx, name, s.maxReadBytes)
}

func (s *Scope) readFile(ctx context.Context, name string, limit int64) ([]byte, error) {
	if err := context.Cause(ctx); err != nil {
		return nil, err
	}
	clean, err := cleanRelative(name)
	if err != nil {
		return nil, err
	}
	if err := s.checkComponents(clean, false); err != nil {
		return nil, err
	}
	file, err := s.root.Open(clean)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("%w: scoped read target", ErrNotRegular)
	}
	pathInfo, err := s.root.Lstat(clean)
	if err != nil {
		return nil, err
	}
	if pathInfo.Mode()&os.ModeSymlink != 0 || !os.SameFile(info, pathInfo) {
		return nil, fmt.Errorf("%w: read target changed while opening", ErrSymlink)
	}
	if info.Size() > limit {
		return nil, ErrTooLarge
	}
	data, err := io.ReadAll(io.LimitReader(&contextReader{ctx: ctx, reader: file}, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, ErrTooLarge
	}
	return data, nil
}

// AtomicWrite durably replaces a regular file. The temporary file is created in
// the destination directory, chmodded, fsynced, renamed, and followed by a
// directory fsync. Existing symlinks and non-regular targets are refused.
func (s *Scope) AtomicWrite(ctx context.Context, name string, contents []byte) error {
	return s.atomicWrite(ctx, name, contents, nil)
}

type atomicWriteHook func(stage string) error

func (s *Scope) atomicWrite(ctx context.Context, name string, contents []byte, hook atomicWriteHook) error {
	if int64(len(contents)) > s.maxWriteBytes {
		return ErrTooLarge
	}
	if err := context.Cause(ctx); err != nil {
		return err
	}
	clean, err := cleanRelative(name)
	if err != nil {
		return err
	}
	parent := filepath.ToSlash(filepath.Dir(filepath.FromSlash(clean)))
	if parent == "." {
		parent = "."
	}
	if err := s.ensureDirectory(ctx, parent); err != nil {
		return err
	}
	if err := s.checkFinalFile(clean, true); err != nil {
		return err
	}

	temporary, err := tempName(parent, filepath.Base(filepath.FromSlash(clean)))
	if err != nil {
		return err
	}
	file, err := s.root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, s.fileMode)
	if err != nil {
		return err
	}
	temporaryExists := true
	defer func() {
		if temporaryExists {
			_ = s.root.Remove(temporary)
		}
	}()

	defer func() {
		if file != nil {
			_ = file.Close()
		}
	}()
	if err := writeAll(ctx, file, contents); err != nil {
		return err
	}
	if hook != nil {
		if err := hook("written"); err != nil {
			return err
		}
	}
	if err := file.Chmod(s.fileMode); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if hook != nil {
		if err := hook("file_synced"); err != nil {
			return err
		}
	}
	if err := file.Close(); err != nil {
		return err
	}
	file = nil
	if err := s.checkFinalFile(clean, true); err != nil {
		return err
	}
	if err := s.root.Rename(temporary, clean); err != nil {
		return err
	}
	temporaryExists = false
	if hook != nil {
		if err := hook("renamed"); err != nil {
			return err
		}
	}
	return s.syncDirectory(parent)
}

func (s *Scope) ensureDirectory(ctx context.Context, clean string) error {
	if clean == "." {
		return nil
	}
	parts := strings.Split(filepath.ToSlash(clean), "/")
	current := "."
	for _, part := range parts {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		next := part
		if current != "." {
			next = current + "/" + part
		}
		info, err := s.root.Lstat(next)
		switch {
		case err == nil:
			if info.Mode()&os.ModeSymlink != 0 {
				return fmt.Errorf("%w: directory component", ErrSymlink)
			}
			if !info.IsDir() {
				return fmt.Errorf("%w: directory component", ErrNotRegular)
			}
		case errors.Is(err, fs.ErrNotExist):
			if err := s.root.Mkdir(next, s.dirMode); err != nil {
				if !errors.Is(err, fs.ErrExist) {
					return err
				}
				// Another scoped operation may have created the same shared
				// parent after our lstat. Re-inspect it rather than turning an
				// idempotent MkdirAll into a spurious failure.
				info, inspectErr := s.root.Lstat(next)
				if inspectErr != nil {
					return inspectErr
				}
				if info.Mode()&os.ModeSymlink != 0 {
					return fmt.Errorf("%w: directory component", ErrSymlink)
				}
				if !info.IsDir() {
					return fmt.Errorf("%w: directory component", ErrNotRegular)
				}
				break
			}
			if err := s.syncDirectory(current); err != nil {
				return err
			}
		default:
			return err
		}
		current = next
	}
	return nil
}

func (s *Scope) checkComponents(clean string, allowMissingFinal bool) error {
	parts := strings.Split(filepath.ToSlash(clean), "/")
	current := "."
	for index, part := range parts {
		next := part
		if current != "." {
			next = current + "/" + part
		}
		info, err := s.root.Lstat(next)
		if errors.Is(err, fs.ErrNotExist) && allowMissingFinal && index == len(parts)-1 {
			return nil
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("%w: path component", ErrSymlink)
		}
		if index < len(parts)-1 && !info.IsDir() {
			return fmt.Errorf("%w: path component", ErrNotRegular)
		}
		current = next
	}
	return nil
}

func (s *Scope) checkFinalFile(clean string, allowMissing bool) error {
	if err := s.checkComponents(clean, allowMissing); err != nil {
		return err
	}
	info, err := s.root.Lstat(clean)
	if errors.Is(err, fs.ErrNotExist) && allowMissing {
		return nil
	}
	if err != nil {
		return err
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("%w: file", ErrSymlink)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("%w: file", ErrNotRegular)
	}
	return nil
}

func (s *Scope) syncDirectory(name string) error {
	directory, err := s.root.Open(name)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func cleanRelative(name string) (string, error) {
	if name == "" || strings.IndexByte(name, 0) >= 0 || filepath.IsAbs(name) || filepath.VolumeName(name) != "" {
		return "", fmt.Errorf("%w: path must be relative", ErrInvalidPath)
	}
	if strings.ContainsAny(name, "*$?[]{}") {
		return "", fmt.Errorf("%w: unresolved variable or glob", ErrInvalidPath)
	}
	clean := filepath.Clean(name)
	if clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("%w: traversal", ErrInvalidPath)
	}
	return filepath.ToSlash(clean), nil
}

func tempName(parent, base string) (string, error) {
	random := make([]byte, 16)
	if _, err := rand.Read(random); err != nil {
		return "", err
	}
	name := "." + base + "." + hex.EncodeToString(random) + ".tmp"
	if parent == "." {
		return name, nil
	}
	return parent + "/" + name, nil
}

func writeAll(ctx context.Context, writer io.Writer, contents []byte) error {
	for len(contents) > 0 {
		if err := context.Cause(ctx); err != nil {
			return err
		}
		written, err := writer.Write(contents)
		if err != nil {
			return err
		}
		if written == 0 {
			return io.ErrShortWrite
		}
		contents = contents[written:]
	}
	return nil
}

type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(buffer []byte) (int, error) {
	if err := context.Cause(r.ctx); err != nil {
		return 0, err
	}
	return r.reader.Read(buffer)
}
