package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"

	"github.com/centauri-ai/coslash/collector/internal/hubclient"
)

func handleProtocolActivation(raw string) (*hubclient.LaunchIntent, bool) {
	intent, err := hubclient.ParseLaunchIntentURL(raw)
	if err != nil {
		fmt.Fprintln(os.Stderr, "coSlash activation could not be started; return to Hub and retry.")
		return nil, true
	}
	if forwardWhenReady(&intent) {
		return nil, true
	}
	if err := writeStoredHubURL(intent.HubURL.String()); err != nil {
		fmt.Fprintln(os.Stderr, "coSlash could not save the Hub address; return to Hub and retry.")
		return nil, true
	}
	return &intent, false
}

func forwardWhenReady(intent *hubclient.LaunchIntent) bool {
	if runtimeOwnerActive() {
		if err := forwardProtocolActivation(intent); err != nil {
			fmt.Fprintln(os.Stderr, "coSlash could not start the requested Hub step; return to Hub and retry.")
		}
		return true
	}
	if !exclusiveRuntimeLockHeld("runtime.lock") {
		return false
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if runtimeOwnerActive() {
			if err := forwardProtocolActivation(intent); err != nil {
				fmt.Fprintln(os.Stderr, "coSlash could not start the requested Hub step; return to Hub and retry.")
			}
			return true
		}
		if !exclusiveRuntimeLockHeld("runtime.lock") {
			return false
		}
		time.Sleep(250 * time.Millisecond)
	}
	fmt.Fprintln(os.Stderr, "coSlash is still starting; return to Hub and retry in a moment.")
	return true
}

func forwardProtocolActivation(intent *hubclient.LaunchIntent) error {
	client, err := newLocalAPIClient()
	if err != nil {
		return err
	}
	var path string
	var body []byte
	switch intent.Action {
	case "pair":
		path = "/api/hub/onboarding/activate"
		body, err = json.Marshal(map[string]string{
			"hubUrl": intent.HubURL.String(), "attemptId": intent.AttemptID, "launchIntent": intent.LaunchToken,
		})
	case "check-in":
		path = "/api/hub/onboarding/check-in"
		body, err = json.Marshal(map[string]string{"hubUrl": intent.HubURL.String()})
	default:
		return errors.New("unsupported coSlash activation")
	}
	if err != nil {
		return errors.New("could not prepare coSlash activation")
	}
	_, err = client.request(http.MethodPost, path, bytes.NewReader(body))
	if err != nil {
		return errors.New("coSlash app did not accept the activation")
	}
	return nil
}
