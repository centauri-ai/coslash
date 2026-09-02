package remoteprotocol

import (
	"bytes"
	"testing"
)

// FuzzDecode exercises the strict, bounded NDJSON decoder with hostile wire
// bytes. The fixed request keeps every iteration inside the protocol ceilings.
func FuzzDecode(f *testing.F) {
	f.Add([]byte(""))
	f.Add([]byte("{}\n"))
	f.Add([]byte(`{"type":"handshake","protocol_version":1,"request_id":"req-1","sequence":1}` + "\n"))
	request := request()
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = Decode(bytes.NewReader(input), request)
	})
}

func FuzzDecodeCapabilities(f *testing.F) {
	f.Add([]byte("{}"))
	f.Add([]byte(`{"protocol":{"min":1,"max":1},"schema":{"min":1,"max":1},"parser_version":"parser-v1"}`))
	f.Fuzz(func(t *testing.T, input []byte) {
		_, _ = DecodeCapabilities(bytes.NewReader(input))
	})
}
