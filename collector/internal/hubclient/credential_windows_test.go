//go:build windows

package hubclient

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var credDeleteW = advapi32.NewProc("CredDeleteW")

func TestOSKeychainWindowsLifecycle(t *testing.T) {
	target := fmt.Sprintf("coslash-test-%d-%d", os.Getpid(), time.Now().UnixNano())
	store := OSKeychain{Service: target, Account: "disposable"}
	ctx := context.Background()
	t.Cleanup(func() { _ = deleteWindowsCredential(store) })

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

	if err := deleteWindowsCredential(store); err != nil {
		t.Fatalf("delete credential: %v", err)
	}
	if _, err := store.Load(ctx); !errors.Is(err, ErrNotPaired) {
		t.Fatalf("load deleted credential error = %v; want %v", err, ErrNotPaired)
	}
}

func deleteWindowsCredential(store OSKeychain) error {
	target, err := windows.UTF16PtrFromString(windowsCredentialTarget(store))
	if err != nil {
		return err
	}
	ok, _, callErr := credDeleteW.Call(uintptr(unsafe.Pointer(target)), windowsCredentialTypeGeneric, 0)
	if ok == 0 {
		return callErr
	}
	return nil
}
