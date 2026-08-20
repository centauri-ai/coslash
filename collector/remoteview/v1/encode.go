package remoteviewv1

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// Marshal returns canonical remote-view JSON without whitespace.
func Marshal(view View) ([]byte, error) {
	if err := ValidateView(view); err != nil {
		return nil, err
	}
	data, err := json.Marshal(view)
	if err != nil {
		return nil, fmt.Errorf("marshal remote view: %w", err)
	}
	if len(data) > MaxPayloadBytes {
		return nil, fmt.Errorf("remote view is %d bytes; maximum is %d: %w", len(data), MaxPayloadBytes, ErrOversized)
	}
	return data, nil
}

// MarshalProbe returns canonical probe JSON without whitespace.
func MarshalProbe(probe Probe) ([]byte, error) {
	if err := ValidateProbe(probe); err != nil {
		return nil, err
	}
	data, err := json.Marshal(probe)
	if err != nil {
		return nil, fmt.Errorf("marshal remote probe: %w", err)
	}
	if len(data) > MaxPayloadBytes {
		return nil, fmt.Errorf("remote probe is %d bytes; maximum is %d: %w", len(data), MaxPayloadBytes, ErrOversized)
	}
	return data, nil
}

// Decode accepts closed-object remote-view JSON and rejects trailing values.
func Decode(data []byte) (View, error) {
	if len(data) > MaxPayloadBytes {
		return View{}, fmt.Errorf("remote view is %d bytes; maximum is %d: %w", len(data), MaxPayloadBytes, ErrOversized)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var view View
	if err := decoder.Decode(&view); err != nil {
		return View{}, fmt.Errorf("decode remote view: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return View{}, err
	}
	if err := ValidateView(view); err != nil {
		return View{}, err
	}
	return view, nil
}

// DecodeProbe accepts closed-object probe JSON and rejects trailing values.
func DecodeProbe(data []byte) (Probe, error) {
	if len(data) > MaxPayloadBytes {
		return Probe{}, fmt.Errorf("remote probe is %d bytes; maximum is %d: %w", len(data), MaxPayloadBytes, ErrOversized)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var probe Probe
	if err := decoder.Decode(&probe); err != nil {
		return Probe{}, fmt.Errorf("decode remote probe: %w", err)
	}
	if err := ensureEOF(decoder); err != nil {
		return Probe{}, err
	}
	if err := ValidateProbe(probe); err != nil {
		return Probe{}, err
	}
	return probe, nil
}

func ensureEOF(decoder *json.Decoder) error {
	var extra any
	if err := decoder.Decode(&extra); err == nil {
		return fmt.Errorf("payload contains multiple JSON values")
	} else if !errors.Is(err, io.EOF) {
		return fmt.Errorf("decode trailing data: %w", err)
	}
	return nil
}
