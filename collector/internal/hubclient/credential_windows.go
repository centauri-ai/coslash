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
	var credential *windowsCredential
	ok, _, callErr := credReadW.Call(
		uintptr(unsafe.Pointer(target)),
		windowsCredentialTypeGeneric,
		0,
		uintptr(unsafe.Pointer(&credential)),
	)
	if ok == 0 {
		return "", windowsCredentialLoadError(callErr)
	}
	defer credFree.Call(uintptr(unsafe.Pointer(credential)))
	if credential.CredentialBlobSize == 0 || credential.CredentialBlob == nil {
		return "", errors.New("load Hub credential: Credential Manager returned an empty credential")
	}
	value := strings.TrimSpace(string(unsafe.Slice(credential.CredentialBlob, credential.CredentialBlobSize)))
	if value == "" {
		return "", errors.New("load Hub credential: Credential Manager returned an empty credential")
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
	ok, _, callErr := credWriteW.Call(uintptr(unsafe.Pointer(&credential)), 0)
	if ok == 0 {
		return fmt.Errorf("save Hub credential: Credential Manager failed: %w", callErr)
	}
	return nil
}

func windowsCredentialTarget(store OSKeychain) string {
	return store.Service + ":" + store.Account
}
