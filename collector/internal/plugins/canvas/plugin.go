// Package canvas defines the compile-time boundary for the Canvas suite.
package canvas

import (
	"context"
	"net/http"
)

// RouteRegistrar is the dependency boundary for Canvas HTTP route groups.
// Implementations may register only the route prefixes frozen in CONTRACTS.md.
type RouteRegistrar interface {
	Register(*http.ServeMux)
}

// BackgroundService is the dependency boundary for Canvas-owned workers.
// The component that starts a service also owns closing it.
type BackgroundService interface {
	Start(context.Context) error
	Close(context.Context) error
}

// Plugin is the backend lifecycle exposed to the coSlash application.
//
// Implementations own their routes and background work. The application creates
// exactly one Plugin, registers it before serving requests, and starts and closes
// it with the collector lifecycle.
type Plugin interface {
	Register(*http.ServeMux)
	Start(context.Context) error
	Close(context.Context) error
}

// New returns the compile-only Canvas plugin. Product routes and services are
// added by later migration tasks; until then every lifecycle method is a no-op.
func New() Plugin {
	return &plugin{}
}

type plugin struct{}

func (*plugin) Register(*http.ServeMux) {}

func (*plugin) Start(context.Context) error { return nil }

func (*plugin) Close(context.Context) error { return nil }

var _ Plugin = (*plugin)(nil)
