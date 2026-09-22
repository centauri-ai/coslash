//go:build windows

package hubclient

import (
	"context"
	"errors"
	"testing"
	"unsafe"

	"golang.org/x/sys/windows"
)

func TestWindowsCredentialLoadErrors(t *testing.T) {
	if err := windowsCredentialLoadError(windows.ERROR_NOT_FOUND); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("ERROR_NOT_FOUND = %v; want %v", err, ErrNotPaired)
	}
	if err := windowsCredentialLoadError(windows.ERROR_ACCESS_DENIED); !errors.Is(err, windows.ERROR_ACCESS_DENIED) || errors.Is(err, ErrNotPaired) {
		t.Fatalf("ERROR_ACCESS_DENIED = %v; want wrapped API error", err)
	}
	if _, err := (OSKeychain{Service: "invalid\x00target", Account: "test"}).Load(context.Background()); err == nil || errors.Is(err, ErrNotPaired) {
		t.Fatalf("invalid target error = %v; want distinct construction error", err)
	}
}

func TestOSKeychainWindowsLifecycle(t *testing.T) {
	originalRead, originalWrite, originalFree := readWindowsCredential, writeWindowsCredential, freeWindowsCredential
	t.Cleanup(func() {
		readWindowsCredential, writeWindowsCredential, freeWindowsCredential = originalRead, originalWrite, originalFree
	})
	credentials := map[string][]byte{}
	readWindowsCredential = func(target *uint16) (*windowsCredential, error) {
		value, ok := credentials[windows.UTF16PtrToString(target)]
		if !ok {
			return nil, windows.ERROR_NOT_FOUND
		}
		blob := append([]byte(nil), value...)
		return &windowsCredential{CredentialBlobSize: uint32(len(blob)), CredentialBlob: &blob[0]}, nil
	}
	writeWindowsCredential = func(credential *windowsCredential) error {
		credentials[windows.UTF16PtrToString(credential.TargetName)] = append(
			[]byte(nil), unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)...,
		)
		return nil
	}
	freeWindowsCredential = func(*windowsCredential) {}

	store := OSKeychain{Service: "coslash-test", Account: "disposable"}
	ctx := context.Background()

	if err := store.Save(ctx, "first"); err != nil {
		t.Fatalf("create credential: %v", err)
	}
	if got, err := store.Load(ctx); err != nil || got != "first" {
		t.Fatalf("load created credential = %q, %v; want %q, nil", got, err, "first")
	}

	if err := store.Save(ctx, "second"); err != nil {
		t.Fatalf("replace credential: %v", err)
	}
	if got, err := store.Load(ctx); err != nil || got != "second" {
		t.Fatalf("load replaced credential = %q, %v; want %q, nil", got, err, "second")
	}

	delete(credentials, windowsCredentialTarget(store))
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("load deleted credential error = %v; want %v", err, ErrNotPaired)
	}
}
