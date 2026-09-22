package hubclient

import (
	"context"
	"errors"
)

var ErrNotPaired = errors.New("hub device credential is not available")

type CredentialStore interface {
	Load(context.Context) (string, error)
	Save(context.Context, string) error
}

type OSKeychain struct {
	Service string
	Account string
}
