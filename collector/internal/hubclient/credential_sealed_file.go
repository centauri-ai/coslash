package hubclient

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const sealedCredentialVersion = "v1:"

// SealedFileCredentialStore keeps a long credential in a private, encrypted
// file. Its encryption key lives in the OS keychain, where the macOS security
// command can safely save the short key through stdin. The security command's
// interactive password input silently truncates long OAuth credentials.
type SealedFileCredentialStore struct {
	Path     string
	Keychain CredentialStore
}

func (s SealedFileCredentialStore) Load(ctx context.Context) (string, error) {
	encoded, err := (FileCredentialStore{Path: s.Path}).Load(ctx)
	if err != nil {
		return "", err
	}
	key, err := s.loadKey(ctx)
	if err != nil {
		return "", err
	}
	if !strings.HasPrefix(encoded, sealedCredentialVersion) {
		return "", errors.New("stored Hub credential format is invalid")
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(encoded, sealedCredentialVersion))
	if err != nil {
		return "", fmt.Errorf("decode stored Hub credential: %w", err)
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return "", err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	if len(payload) < aead.NonceSize() {
		return "", errors.New("stored Hub credential is incomplete")
	}
	plaintext, err := aead.Open(nil, payload[:aead.NonceSize()], payload[aead.NonceSize():], nil)
	if err != nil {
		return "", fmt.Errorf("decrypt stored Hub credential: %w", err)
	}
	return string(plaintext), nil
}

func (s SealedFileCredentialStore) Save(ctx context.Context, credential string) error {
	if strings.TrimSpace(credential) == "" {
		return errors.New("save Hub credential: empty credential")
	}
	key, err := s.loadKey(ctx)
	if errors.Is(err, ErrNotPaired) {
		key = nil
	} else if err != nil {
		// A previous CLI could have put a truncated OAuth JSON value in this
		// keychain item. An explicit new login replaces that unusable value.
		if _, fileErr := (FileCredentialStore{Path: s.Path}).Load(ctx); !errors.Is(fileErr, ErrNotPaired) {
			return err
		}
		key = nil
	}
	if key == nil {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return fmt.Errorf("generate Hub credential key: %w", err)
		}
		if err := s.Keychain.Save(ctx, hex.EncodeToString(key)); err != nil {
			return fmt.Errorf("save Hub credential key: %w", err)
		}
		stored, err := s.loadKey(ctx)
		if err != nil || !equalBytes(stored, key) {
			return errors.New("Hub keychain did not retain the credential key")
		}
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return err
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return fmt.Errorf("generate Hub credential nonce: %w", err)
	}
	payload := aead.Seal(nonce, nonce, []byte(credential), nil)
	return (FileCredentialStore{Path: s.Path}).Save(ctx, sealedCredentialVersion+base64.StdEncoding.EncodeToString(payload))
}

func (s SealedFileCredentialStore) Delete(ctx context.Context) error {
	if err := (FileCredentialStore{Path: s.Path}).Delete(ctx); err != nil {
		return err
	}
	return s.Keychain.Delete(ctx)
}

func (s SealedFileCredentialStore) loadKey(ctx context.Context) ([]byte, error) {
	encoded, err := s.Keychain.Load(ctx)
	if err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(encoded)
	if err != nil || len(key) != 32 {
		return nil, errors.New("stored Hub credential key is invalid")
	}
	return key, nil
}

func equalBytes(left, right []byte) bool {
	if len(left) != len(right) {
		return false
	}
	var difference byte
	for i := range left {
		difference |= left[i] ^ right[i]
	}
	return difference == 0
}
