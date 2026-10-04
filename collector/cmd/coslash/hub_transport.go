package main

import (
	"context"
	"io"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

type currentHubTransport struct {
	current func() *hubclient.Client
}

func (transport currentHubTransport) client() (*hubclient.Client, error) {
	if transport.current == nil {
		return nil, hubclient.ErrNotPaired
	}
	client := transport.current()
	if client == nil {
		return nil, hubclient.ErrNotPaired
	}
	return client, nil
}

func (transport currentHubTransport) V4Binding(ctx context.Context) (string, error) {
	client, err := transport.client()
	if err != nil {
		return "", err
	}
	return client.V4Binding(ctx)
}

func (transport currentHubTransport) V4CheckIn(ctx context.Context, queue hubclient.V4Queue, appliedConfigVersion int64, results []hubclient.V4CommandResult, agentsFound []string, log []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4CheckIn{}, err
	}
	return client.V4CheckIn(ctx, queue, appliedConfigVersion, results, agentsFound, log)
}

func (transport currentHubTransport) V4Create(ctx context.Context, input hubclient.V4Create) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	return client.V4Create(ctx, input)
}

func (transport currentHubTransport) V4Status(ctx context.Context, uploadID string) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	return client.V4Status(ctx, uploadID)
}

func (transport currentHubTransport) V4PutChunk(ctx context.Context, uploadID string, missing hubclient.V4Missing, body io.Reader) error {
	client, err := transport.client()
	if err != nil {
		return err
	}
	return client.V4PutChunk(ctx, uploadID, missing, body)
}

func (transport currentHubTransport) V4Confirm(ctx context.Context, uploadID string, missing ...hubclient.V4Missing) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	return client.V4Confirm(ctx, uploadID, missing...)
}

func (transport currentHubTransport) V4Finalize(ctx context.Context, uploadID string) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	return client.V4Finalize(ctx, uploadID)
}

func (transport currentHubTransport) V4ListBatch(ctx context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	client, err := transport.client()
	if err != nil {
		return nil, err
	}
	return client.V4ListBatch(ctx, items)
}

func (transport currentHubTransport) V4Wait(ctx context.Context, since int64) (hubclient.V4Wait, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Wait{}, err
	}
	return client.V4Wait(ctx, since)
}
