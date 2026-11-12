package main

import (
	"context"
	"errors"
	"io"
	"log"
	"net/url"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
	"github.com/centauri-ai/coslash/collector/internal/syncv4"
)

type currentHubTransport struct {
	current func() *hubclient.Client
	queue   *syncv4.Queue
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

func (transport currentHubTransport) V4CheckIn(ctx context.Context, queue hubclient.V4Queue, appliedConfigVersion int64, results []hubclient.V4CommandResult, agentsFound []string, logEntries []hubclient.V4LogEntry) (hubclient.V4CheckIn, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4CheckIn{}, err
	}
	result, err := client.V4CheckIn(ctx, queue, appliedConfigVersion, results, agentsFound, logEntries)
	transport.recordHubContact(err)
	if transport.queue != nil {
		status := "ok"
		if err != nil {
			status = syncv4.DeferReason(err)
		}
		if saveErr := transport.queue.RecordCheckIn(time.Now(), status); saveErr != nil {
			log.Printf("record Hub check-in status: %v", saveErr)
		}
	}
	return result, err
}

func (transport currentHubTransport) V4ReportUpdate(ctx context.Context, result hubclient.V4CommandResult) (bool, error) {
	client, err := transport.client()
	if err != nil {
		return false, err
	}
	active, err := client.V4ReportUpdate(ctx, result)
	return active, err
}

func (transport currentHubTransport) V4Create(ctx context.Context, input hubclient.V4Create) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	result, err := client.V4Create(ctx, input)
	transport.recordHubContact(err)
	return result, err
}

func (transport currentHubTransport) V4Status(ctx context.Context, uploadID string) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	result, err := client.V4Status(ctx, uploadID)
	transport.recordHubContact(err)
	return result, err
}

func (transport currentHubTransport) V4Abort(ctx context.Context, uploadID string) error {
	client, err := transport.client()
	if err != nil {
		return err
	}
	err = client.V4Abort(ctx, uploadID)
	transport.recordHubContact(err)
	return err
}

func (transport currentHubTransport) V4PutChunk(ctx context.Context, uploadID string, missing hubclient.V4Missing, body io.Reader) error {
	client, err := transport.client()
	if err != nil {
		return err
	}
	err = client.V4PutChunk(ctx, uploadID, missing, body)
	transport.recordHubContact(err)
	return err
}

func (transport currentHubTransport) V4Confirm(ctx context.Context, uploadID string, missing ...hubclient.V4Missing) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	result, err := client.V4Confirm(ctx, uploadID, missing...)
	transport.recordHubContact(err)
	return result, err
}

func (transport currentHubTransport) V4Finalize(ctx context.Context, uploadID string) (hubclient.V4Status, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Status{}, err
	}
	result, err := client.V4Finalize(ctx, uploadID)
	transport.recordHubContact(err)
	return result, err
}

func (transport currentHubTransport) V4ListBatch(ctx context.Context, items []hubclient.V4ListItem) ([]hubclient.V4ListResult, error) {
	client, err := transport.client()
	if err != nil {
		return nil, err
	}
	result, err := client.V4ListBatch(ctx, items)
	transport.recordHubContact(err)
	return result, err
}

func (transport currentHubTransport) V4Wait(ctx context.Context, since int64) (hubclient.V4Wait, error) {
	client, err := transport.client()
	if err != nil {
		return hubclient.V4Wait{}, err
	}
	result, err := client.V4Wait(ctx, since)
	transport.recordHubContact(err)
	var problem hubclient.V4Problem
	if transport.queue != nil && errors.As(err, &problem) && (problem.Code == "device_revoked" || problem.Code == "not_paired") {
		if saveErr := transport.queue.RecordCheckIn(time.Now(), "not_paired"); saveErr != nil {
			log.Printf("record Hub check-in status: %v", saveErr)
		}
	}
	return result, err
}

func (transport currentHubTransport) recordHubContact(err error) {
	if transport.queue == nil {
		return
	}
	if _, saveErr := transport.queue.RecordHubContact(time.Now(), err); saveErr != nil {
		log.Printf("record Hub contact status: %v", saveErr)
	}
}

var errHubBindingChanged = errors.New("Hub binding changed")

// bindingPinnedClient returns a client that can only authenticate as binding. The
// check lives inside the credential read that each request performs, so a Hub,
// account or credential change cannot slip in between a check and the send, and
// it needs no copy of the original client's state.
func bindingPinnedClient(client *hubclient.Client, binding string) *hubclient.Client {
	base := *client.BaseURL
	return &hubclient.Client{
		BaseURL: &base, HTTP: client.HTTP,
		Credentials: bindingPinnedCredentials{inner: client.Credentials, base: base, binding: binding},
		DeviceName:  client.DeviceName, CollectorVersion: client.CollectorVersion, InstallChannel: client.InstallChannel,
		LoadSession: client.LoadSession, LoadSourceSession: client.LoadSourceSession, LoadFullSession: client.LoadFullSession,
	}
}

type bindingPinnedCredentials struct {
	inner   hubclient.CredentialStore
	base    url.URL
	binding string
}

func (s bindingPinnedCredentials) Load(ctx context.Context) (string, error) {
	credential, err := s.inner.Load(ctx)
	if err != nil {
		return "", err
	}
	base := s.base
	got, err := (&hubclient.Client{BaseURL: &base, Credentials: fixedCredential(credential)}).V4Binding(ctx)
	if err != nil {
		return "", err
	}
	if got != s.binding {
		return "", errHubBindingChanged
	}
	return credential, nil
}

func (bindingPinnedCredentials) Save(context.Context, string) error { return errHubBindingChanged }
func (bindingPinnedCredentials) Delete(context.Context) error       { return errHubBindingChanged }

func (s bindingPinnedCredentials) DeleteIfMatches(ctx context.Context, expected string) (bool, error) {
	if deleter, ok := s.inner.(hubclient.ConditionalCredentialDeleter); ok {
		return deleter.DeleteIfMatches(ctx, expected)
	}
	return false, nil
}

type fixedCredential string

func (c fixedCredential) Load(context.Context) (string, error) { return string(c), nil }
func (fixedCredential) Save(context.Context, string) error     { return nil }
func (fixedCredential) Delete(context.Context) error           { return nil }
