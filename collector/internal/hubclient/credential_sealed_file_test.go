package hubclient

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type shortCredentialKeychain struct{ value string }

func (s *shortCredentialKeychain) Load(context.Context) (string, error) {
	if s.value == "" {
		return "", ErrNotPaired
	}
	return s.value, nil
}

func (s *shortCredentialKeychain) Save(_ context.Context, value string) error {
	if len(value) > 128 {
		return errors.New("keychain prompt truncates long values")
	}
	s.value = value
	return nil
}

func (s *shortCredentialKeychain) Delete(context.Context) error {
	s.value = ""
	return nil
}

func TestSealedFileCredentialStoreLongCredentialRoundTrip(t *testing.T) {
	ctx := context.Background()
	keychain := &shortCredentialKeychain{}
	path := filepath.Join(t.TempDir(), "hub-credentials", "agent-access.enc")
	store := SealedFileCredentialStore{Path: path, Keychain: keychain}
	first := strings.Repeat("oauth-token-one", 200)
	if err := store.Save(ctx, first); err != nil {
		t.Fatal(err)
	}
	if len(keychain.value) != 64 {
		t.Fatalf("keychain value length = %d, want 64", len(keychain.value))
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), first) || !strings.HasPrefix(string(data), sealedCredentialVersion) {
		t.Fatal("credential file is not sealed")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS != "windows" && info.Mode().Perm() != 0o600 {
		t.Fatalf("credential file mode = %v", info.Mode())
	}
	if got, err := store.Load(ctx); err != nil || got != first {
		t.Fatalf("load long credential: match=%v, err=%v", got == first, err)
	}
	second := strings.Repeat("oauth-token-two", 200)
	if err := store.Save(ctx, second); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(ctx); err != nil || got != second {
		t.Fatalf("load refreshed credential: match=%v, err=%v", got == second, err)
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("load after logout = %v", err)
	}
	if keychain.value != "" {
		t.Fatal("logout retained the keychain key")
	}
}

func TestSealedFileCredentialStoreReplacesOldTruncatedKeychainValue(t *testing.T) {
	ctx := context.Background()
	keychain := &shortCredentialKeychain{value: `{"access_token":"truncated`}
	store := SealedFileCredentialStore{
		Path: filepath.Join(t.TempDir(), "agent-access.enc"), Keychain: keychain,
	}
	if err := store.Save(ctx, strings.Repeat("credential", 100)); err != nil {
		t.Fatal(err)
	}
	if len(keychain.value) != 64 {
		t.Fatalf("replacement key length = %d", len(keychain.value))
	}
}

func TestSealedFileCredentialStoreReadsAndMigratesLegacyKeychainCredential(t *testing.T) {
	ctx := context.Background()
	legacy := `{"hub_url":"https://hub.example.test/mcp","access_token":"old"}`
	keychain := &shortCredentialKeychain{value: legacy}
	store := SealedFileCredentialStore{
		Path: filepath.Join(t.TempDir(), "agent-access.enc"), Keychain: keychain,
	}
	if got, err := store.Load(ctx); err != nil || got != legacy {
		t.Fatalf("load legacy credential: match=%v, err=%v", got == legacy, err)
	}
	updated := strings.Repeat("refreshed-token", 100)
	if err := store.Save(ctx, updated); err != nil {
		t.Fatal(err)
	}
	if got, err := store.Load(ctx); err != nil || got != updated {
		t.Fatalf("load migrated credential: match=%v, err=%v", got == updated, err)
	}
	if keychain.value == legacy {
		t.Fatal("legacy plaintext remained in Keychain after migration")
	}
	if err := store.Delete(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("load after deleting migrated credential = %v", err)
	}
}
