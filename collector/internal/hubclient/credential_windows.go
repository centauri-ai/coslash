//go:build windows

package hubclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	windowsCredentialTypeGeneric  = 1
	windowsCredentialPersistLocal = 2
)

var (
	advapi32   = windows.NewLazySystemDLL("advapi32.dll")
	credReadW  = advapi32.NewProc("CredReadW")
	credWriteW = advapi32.NewProc("CredWriteW")
	credFree   = advapi32.NewProc("CredFree")

	readWindowsCredential = func(target *uint16) (*windowsCredential, error) {
		var credential *windowsCredential
		ok, _, callErr := credReadW.Call(
			uintptr(unsafe.Pointer(target)),
			windowsCredentialTypeGeneric,
			0,
			uintptr(unsafe.Pointer(&credential)),
		)
		if ok == 0 {
			return nil, callErr
		}
		return credential, nil
	}
	writeWindowsCredential = func(credential *windowsCredential) error {
		ok, _, callErr := credWriteW.Call(uintptr(unsafe.Pointer(credential)), 0)
		if ok == 0 {
			return callErr
		}
		return nil
	}
	freeWindowsCredential = func(credential *windowsCredential) {
		credFree.Call(uintptr(unsafe.Pointer(credential)))
	}
)

type windowsCredential struct {
	Flags              uint32
	Type               uint32
	TargetName         *uint16
	Comment            *uint16
	LastWritten        windows.Filetime
	CredentialBlobSize uint32
	CredentialBlob     *byte
	Persist            uint32
	AttributeCount     uint32
	Attributes         uintptr
	TargetAlias        *uint16
	UserName           *uint16
}

func (s OSKeychain) Load(ctx context.Context) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	target, err := windows.UTF16PtrFromString(windowsCredentialTarget(s))
	if err != nil {
		return "", fmt.Errorf("load Hub credential: invalid target: %w", err)
	}
	credential, err := readWindowsCredential(target)
	if err != nil {
		return "", windowsCredentialLoadError(err)
	}
	defer freeWindowsCredential(credential)
	if credential.CredentialBlobSize == 0 || credential.CredentialBlob == nil {
		return "", ErrNotPaired
	}
	value := strings.TrimSpace(string(unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)))
	if value == "" {
		return "", ErrNotPaired
	}
	return value, nil
}

func windowsCredentialLoadError(err error) error {
	if errors.Is(err, windows.ERROR_NOT_FOUND) {
		return ErrNotPaired
	}
	return fmt.Errorf("load Hub credential: Credential Manager failed: %w", err)
}

func (s OSKeychain) Save(ctx context.Context, value string) error {
	if strings.TrimSpace(value) == "" {
		return errors.New("save Hub credential: empty credential")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	target, err := windows.UTF16PtrFromString(windowsCredentialTarget(s))
	if err != nil {
		return fmt.Errorf("save Hub credential: invalid target: %w", err)
	}
	account, err := windows.UTF16PtrFromString(s.Account)
	if err != nil {
		return fmt.Errorf("save Hub credential: invalid account: %w", err)
	}
	blob := []byte(value)
	credential := windowsCredential{
		Type:               windowsCredentialTypeGeneric,
		TargetName:         target,
		CredentialBlobSize: uint32(len(blob)),
		CredentialBlob:     &blob[0],
		Persist:            windowsCredentialPersistLocal,
		UserName:           account,
	}
	if err := writeWindowsCredential(&credential); err != nil {
		return fmt.Errorf("save Hub credential: Credential Manager failed: %w", err)
	}
	return nil
}

func windowsCredentialTarget(store OSKeychain) string {
	return store.Service + ":" + store.Account
}
