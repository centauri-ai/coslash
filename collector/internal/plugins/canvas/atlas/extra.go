package atlas

import (
	"encoding/json"
	"reflect"
	"strings"
	"sync"
)

// Forward-compatible round trips.
//
// A board written by a newer coSlash can be opened by an older one. Without the
// capture below, opening it and saving would silently delete every member the
// older build does not know about — configuration the user never saw and never
// chose to discard. So each document type keeps its unknown members verbatim
// and re-emits them on encode.
//
// Only members this build does not declare are captured. A member the struct
// owns is always re-encoded from the typed field, so normalization and policy
// remain the authority on everything the model understands.

var knownMembersCache sync.Map // reflect.Type -> map[string]struct{}

func knownMembers(sample any) map[string]struct{} {
	target := reflect.TypeOf(sample)
	for target.Kind() == reflect.Pointer {
		target = target.Elem()
	}
	if cached, ok := knownMembersCache.Load(target); ok {
		return cached.(map[string]struct{})
	}
	members := make(map[string]struct{}, target.NumField())
	for index := range target.NumField() {
		field := target.Field(index)
		if field.PkgPath != "" {
			// Unexported: the extra map itself, never a JSON member.
			continue
		}
		name := field.Name
		if tag, ok := field.Tag.Lookup("json"); ok {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
		}
		members[name] = struct{}{}
	}
	knownMembersCache.Store(target, members)
	return members
}

// captureExtra returns the members of data that sample's type does not declare.
// A non-object document yields no extras rather than an error: the caller's own
// unmarshal has already reported the type mismatch.
func captureExtra(data []byte, sample any) map[string]json.RawMessage {
	var members map[string]json.RawMessage
	if err := json.Unmarshal(data, &members); err != nil {
		return nil
	}
	known := knownMembers(sample)
	var extra map[string]json.RawMessage
	for name, value := range members {
		if _, declared := known[name]; declared {
			continue
		}
		if extra == nil {
			extra = make(map[string]json.RawMessage, len(members))
		}
		extra[name] = value
	}
	return extra
}

// mergeExtra re-encodes an already-marshalled object with its captured members
// restored. Declared members win, so an extra can never shadow a typed field.
//
// The merged form is emitted through a map, whose keys encoding/json sorts, so
// the result is deterministic for a given input.
func mergeExtra(encoded []byte, extra map[string]json.RawMessage) ([]byte, error) {
	if len(extra) == 0 {
		return encoded, nil
	}
	var members map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &members); err != nil {
		return nil, err
	}
	if members == nil {
		members = make(map[string]json.RawMessage, len(extra))
	}
	for name, value := range extra {
		if _, declared := members[name]; declared {
			continue
		}
		members[name] = value
	}
	return json.Marshal(members)
}

// cloneExtra copies a captured member map so a normalized value never aliases
// the document it was decoded from.
func cloneExtra(extra map[string]json.RawMessage) map[string]json.RawMessage {
	if len(extra) == 0 {
		return nil
	}
	copied := make(map[string]json.RawMessage, len(extra))
	for name, value := range extra {
		duplicate := make(json.RawMessage, len(value))
		copy(duplicate, value)
		copied[name] = duplicate
	}
	return copied
}
