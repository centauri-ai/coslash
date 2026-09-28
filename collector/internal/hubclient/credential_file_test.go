package hubclient

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func testCipher(ctx context.Context, args []string, input []byte) ([]byte, error) {
	if len(args) != 6 && len(args) != 5 {
		return nil, errors.New("bad command")
	}
	if args[0] == "encrypt" && args[1] == "--user" && args[2] == "--with-key=tpm2" {
		return []byte("encrypted:" + args[3] + ":" + base64.StdEncoding.EncodeToString(input)), nil
	}
	if args[0] == "decrypt" && len(args) == 5 && args[1] == "--user" {
		prefix := []byte("encrypted:" + args[2] + ":")
		if bytes.HasPrefix(input, prefix) {
			return base64.StdEncoding.DecodeString(string(bytes.TrimPrefix(input, prefix)))
		}
	}
	return nil, errors.New("credential authentication failed")
}

func TestEncryptedHostCredentialsPersistAndSeparateHubs(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "credentials")
	first := EncryptedHostCredentials{Directory: directory, HubURL: "https://one.example", transform: testCipher}
	second := EncryptedHostCredentials{Directory: directory, HubURL: "https://two.example", transform: testCipher}
	if _, err := first.Load(t.Context()); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("unpaired load: %v", err)
	}
	if err := first.Save(t.Context(), "device-secret"); err != nil {
		t.Fatal(err)
	}
	if got, err := first.Load(t.Context()); err != nil || got != "device-secret" {
		t.Fatalf("reopened credential = %q, %v", got, err)
	}
	if _, err := second.Load(t.Context()); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("other Hub credential: %v", err)
	}
	name, _ := first.file()
	data, err := os.ReadFile(name)
	if err != nil || bytes.Contains(data, []byte("device-secret")) {
		t.Fatalf("credential was stored in plaintext: %v", err)
	}
	for _, path := range []string{directory, name} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm()&0o077 != 0 {
			t.Fatalf("non-private %s: %v %v", path, info, err)
		}
	}
}

func TestEncryptedHostCredentialsRejectExposedOrLinkedFiles(t *testing.T) {
	directory := filepath.Join(t.TempDir(), "credentials")
	store := EncryptedHostCredentials{Directory: directory, HubURL: "https://hub.example", transform: testCipher}
	if err := store.Save(t.Context(), "secret"); err != nil {
		t.Fatal(err)
	}
	name, _ := store.file()
	if err := os.Chmod(name, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(t.Context()); err == nil {
		t.Fatal("world-readable credential was accepted")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, name); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(t.Context()); err == nil {
		t.Fatal("symlinked credential was accepted")
	}
	if err := os.Chmod(directory, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := store.Save(context.Background(), "replacement"); err == nil {
		t.Fatal("save in exposed directory was accepted")
	}
}

func TestEncryptedHostCredentialsFailClosedWithoutTPM(t *testing.T) {
	store := EncryptedHostCredentials{Directory: filepath.Join(t.TempDir(), "credentials"), HubURL: "https://hub.example", transform: func(context.Context, []string, []byte) ([]byte, error) {
		return nil, errors.New("TPM unavailable")
	}}
	if err := store.Save(t.Context(), "device-secret"); err == nil {
		t.Fatal("saved credential without encryption provider")
	}
	name, _ := store.file()
	if _, err := os.Stat(name); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("credential file created after encryption failure: %v", err)
	}
}
