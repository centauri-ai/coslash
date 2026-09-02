package remoteprotocol

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
)

// MaxCapabilityBytes bounds the capability document a helper prints. It is far
// smaller than a collect response because it carries versions, not facts.
const MaxCapabilityBytes = 8 << 10

// Capabilities is what a helper answers before any collection: the protocol and
// schema ranges it supports, plus identity for diagnostics. Compatibility is
// decided by overlapping ranges, so helper and Mac build strings never need to
// match.
type Capabilities struct {
	Protocol      VersionRange `json:"protocol"`
	Schema        VersionRange `json:"schema"`
	ParserVersion string       `json:"parser_version"`
	Capabilities  []string     `json:"capabilities,omitempty"`
	Build         string       `json:"build,omitempty"`
	OS            string       `json:"os,omitempty"`
	Arch          string       `json:"arch,omitempty"`
}

// Compatible reports whether this Mac can speak to the helper at all.
func (c Capabilities) Compatible() bool {
	return supports(c.Protocol, ProtocolVersion) && supports(c.Schema, remotefacts.SchemaVersion)
}

// Deprecated reports a helper that still works but no longer offers the current
// protocol as its newest, which is the prompt-to-upgrade case.
func (c Capabilities) Deprecated() bool {
	return c.Compatible() && (c.Protocol.Max < ProtocolVersion || c.Schema.Max < remotefacts.SchemaVersion)
}

func (c Capabilities) Validate() error {
	if c.Protocol.Min <= 0 || c.Protocol.Max < c.Protocol.Min {
		return errors.New("invalid protocol range")
	}
	if c.Schema.Min <= 0 || c.Schema.Max < c.Schema.Min {
		return errors.New("invalid schema range")
	}
	if !validID(c.ParserVersion) {
		return errors.New("invalid parser_version")
	}
	if len(c.Capabilities) > 32 {
		return errors.New("capability list exceeds limit")
	}
	previous := ""
	for _, capability := range c.Capabilities {
		if !validID(capability) || capability <= previous {
			return errors.New("capabilities must be uniquely sorted")
		}
		previous = capability
	}
	for _, value := range []string{c.Build, c.OS, c.Arch} {
		if len(value) > remotefacts.MaxIDBytes {
			return errors.New("capability identity exceeds limit")
		}
	}
	return nil
}

// DecodeCapabilities reads one bounded capability document. Trailing content is
// rejected so a helper cannot smuggle extra output past the handshake.
func DecodeCapabilities(reader io.Reader) (Capabilities, error) {
	limited := &io.LimitedReader{R: reader, N: MaxCapabilityBytes + 1}
	data, err := io.ReadAll(limited)
	if err != nil {
		return Capabilities{}, fmt.Errorf("read capabilities: %w", err)
	}
	if len(data) > MaxCapabilityBytes {
		return Capabilities{}, errors.New("capabilities exceed byte limit")
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var capabilities Capabilities
	if err := decoder.Decode(&capabilities); err != nil {
		return Capabilities{}, fmt.Errorf("decode capabilities: %w", err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Capabilities{}, errors.New("trailing content after capabilities")
	}
	if err := capabilities.Validate(); err != nil {
		return Capabilities{}, err
	}
	return capabilities, nil
}
