package remotehelper

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"

	"github.com/centauri-ai/coslash/collector/internal/remotefacts"
	"github.com/centauri-ai/coslash/collector/internal/remoteprotocol"
	"github.com/centauri-ai/coslash/collector/internal/vendors"
)

// emitter writes strict NDJSON with a gapless sequence and enforces the record,
// count, and response ceilings the request negotiated. Each record is flushed on
// its own line so a response cut short still delivers whole records the Mac can
// apply.
type emitter struct {
	writer   *bufio.Writer
	request  remoteprotocol.Request
	sequence int
	bytes    int
	records  int
	// reservedBytes protects the publishable completion envelope while changed
	// families consume the negotiated aggregate response budget.
	reservedBytes   int
	reservedRecords int
	budgetSkipped   int
}

func newEmitter(output io.Writer, request remoteprotocol.Request) *emitter {
	return &emitter{writer: bufio.NewWriter(output), request: request}
}

func (e *emitter) prepare(record remoteprotocol.Record) ([]byte, error) {
	record.ProtocolVersion = remoteprotocol.ProtocolVersion
	record.RequestID = e.request.RequestID
	record.Sequence = e.sequence + 1
	return marshalRecord(record)
}

func (e *emitter) prepareBounded(record remoteprotocol.Record) ([]byte, bool, error) {
	line, err := e.prepare(record)
	if err != nil {
		return nil, false, err
	}
	return line, len(bytes.TrimSuffix(line, []byte{'\n'})) <= e.request.Limits.MaxRecordBytes, nil
}

func (e *emitter) fitsPrepared(line []byte, reservedBytes int) bool {
	return e.bytes+len(line)+reservedBytes <= e.request.Limits.MaxResponseBytes &&
		e.records+1+e.reservedRecords <= e.request.Limits.MaxRecords
}

func (e *emitter) emitFamily(record remoteprotocol.Record, counts *remoteprotocol.Counts) (bool, error) {
	line, fits, err := e.prepareBounded(record)
	if err != nil {
		return false, err
	}
	if !fits {
		return false, fmt.Errorf("%w: %s record", ErrRecordLimit, record.Type)
	}
	if !e.fitsPrepared(line, e.reservedBytes) {
		counts.SkippedFamilies++
		e.budgetSkipped++
		return false, nil
	}
	return true, e.emitPrepared(record, line)
}

func (e *emitter) emit(record remoteprotocol.Record) error {
	line, err := e.prepare(record)
	if err != nil {
		return err
	}
	return e.emitPrepared(record, line)
}

func (e *emitter) emitPrepared(record remoteprotocol.Record, line []byte) error {
	if len(bytes.TrimSuffix(line, []byte{'\n'})) > e.request.Limits.MaxRecordBytes {
		return fmt.Errorf("%w: %s record", ErrRecordLimit, record.Type)
	}
	if e.records+1 > e.request.Limits.MaxRecords {
		return fmt.Errorf("%w: record count", ErrRecordLimit)
	}
	if e.bytes+len(line) > e.request.Limits.MaxResponseBytes {
		return fmt.Errorf("%w: response bytes", ErrRecordLimit)
	}
	if _, err := e.writer.Write(line); err != nil {
		return err
	}
	if err := e.writer.Flush(); err != nil {
		return err
	}
	e.sequence++
	e.records++
	e.bytes += len(line)
	return nil
}

func (e *emitter) handshake() error {
	return e.emit(remoteprotocol.Record{
		Type:          remoteprotocol.RecordHandshake,
		BaselineID:    e.request.BaselineID,
		SchemaVersion: remotefacts.SchemaVersion,
		ParserVersion: vendors.ParserVersion,
		Capabilities:  Capabilities,
	})
}

func marshalRecord(record remoteprotocol.Record) ([]byte, error) {
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(record); err != nil {
		return nil, fmt.Errorf("encode %s record: %w", record.Type, err)
	}
	return buffer.Bytes(), nil
}
