package hubclient

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

const maxDeviceCredentialBytes = 8 << 10
const maxEncryptedCredentialBytes = 64 << 10

// EncryptedHostCredentials stores only TPM2-bound ciphertext on disk.
// The host must have systemd-creds and a usable TPM2; there is no fallback.
type EncryptedHostCredentials struct {
	Directory string
	HubURL    string
	transform func(context.Context, []string, []byte) ([]byte, error)
}

func (s EncryptedHostCredentials) file() (string, error) {
	if !filepath.IsAbs(s.Directory) || s.HubURL == "" {
		return "", errors.New("host credential store is not configured")
	}
	sum := sha256.Sum256([]byte(s.HubURL))
	return filepath.Join(s.Directory, hex.EncodeToString(sum[:])+".cred"), nil
}

func (s EncryptedHostCredentials) credentialName() string {
	sum := sha256.Sum256([]byte(s.HubURL))
	return "coslash-host-" + hex.EncodeToString(sum[:])
}

func (s EncryptedHostCredentials) crypt(ctx context.Context, args []string, input []byte) ([]byte, error) {
	if s.transform != nil {
		return s.transform(ctx, args, input)
	}
	command := exec.CommandContext(ctx, "/usr/bin/systemd-creds", args...)
	command.Stdin = bytes.NewReader(input)
	result, err := command.Output()
	if err != nil {
		return nil, errors.New("TPM2 credential operation unavailable")
	}
	return result, nil
}

func (s EncryptedHostCredentials) privateDirectory(create bool) error {
	if create {
		if err := os.MkdirAll(s.Directory, 0o700); err != nil {
			return err
		}
	}
	info, err := os.Lstat(s.Directory)
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotPaired
	}
	if err != nil {
		return err
	}
	if !info.IsDir() || info.Mode().Perm()&0o077 != 0 {
		return errors.New("host credential directory is not private")
	}
	return nil
}

func (s EncryptedHostCredentials) Load(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	name, err := s.file()
	if err != nil {
		return "", err
	}
	if err := s.privateDirectory(false); err != nil {
		return "", err
	}
	info, err := os.Lstat(name)
	if errors.Is(err, os.ErrNotExist) {
		return "", ErrNotPaired
	}
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0o077 != 0 || info.Size() > maxEncryptedCredentialBytes {
		return "", errors.New("host credential file is invalid or not private")
	}
	file, err := os.Open(name)
	if err != nil {
		return "", err
	}
	defer file.Close()
	opened, err := file.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return "", errors.New("host credential changed while opening")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxEncryptedCredentialBytes+1))
	if err != nil || len(data) > maxEncryptedCredentialBytes {
		return "", errors.New("read host credential: invalid size")
	}
	plain, err := s.crypt(ctx, []string{"decrypt", "--user", "--name=" + s.credentialName(), "-", "-"}, data)
	if err != nil {
		return "", err
	}
	credential := string(plain)
	if !validHostCredential(credential) {
		return "", errors.New("decrypted host credential is invalid")
	}
	return credential, nil
}

func validHostCredential(credential string) bool {
	return credential != "" && len(credential) <= maxDeviceCredentialBytes && strings.TrimSpace(credential) == credential && !strings.ContainsAny(credential, "\r\n\x00")
}

func (s EncryptedHostCredentials) Save(ctx context.Context, credential string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if !validHostCredential(credential) {
		return errors.New("invalid host credential")
	}
	name, err := s.file()
	if err != nil {
		return err
	}
	if err := s.privateDirectory(true); err != nil {
		return err
	}
	ciphertext, err := s.crypt(ctx, []string{"encrypt", "--user", "--with-key=tpm2", "--name=" + s.credentialName(), "-", "-"}, []byte(credential))
	if err != nil {
		return err
	}
	if len(ciphertext) == 0 || len(ciphertext) > maxEncryptedCredentialBytes || strings.Contains(string(ciphertext), credential) {
		return errors.New("encrypted host credential is invalid")
	}
	verified, err := s.crypt(ctx, []string{"decrypt", "--user", "--name=" + s.credentialName(), "-", "-"}, ciphertext)
	if err != nil || string(verified) != credential {
		return errors.New("host credential encryption could not be verified")
	}
	file, err := os.CreateTemp(s.Directory, ".credential-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err = file.Write(ciphertext); err != nil {
		file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		file.Close()
		return err
	}
	if err = file.Close(); err != nil {
		return err
	}
	if err = os.Rename(file.Name(), name); err != nil {
		return err
	}
	directory, err := os.Open(s.Directory)
	if err != nil {
		return err
	}
	defer directory.Close()
	if err := directory.Sync(); err != nil {
		return fmt.Errorf("persist host credential: %w", err)
	}
	return nil
}
