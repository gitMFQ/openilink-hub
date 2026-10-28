// Package jsonx is the project-wide JSON entry point. It is a thin wrapper over
// the Go 1.27 encoding/json/v2 engine whose default options replicate
// encoding/json (v1) semantics, so swapping v1 call sites for jsonx keeps the
// wire format, tag handling, method dispatch and error types unchanged while
// moving all processing onto the newer, faster implementation.
//
// Prefer this package over importing "encoding/json" or "encoding/json/v2"
// directly:
//
//   - jsonx.Marshal / jsonx.Unmarshal / jsonx.Valid / jsonx.NewEncoder /
//     jsonx.NewDecoder → v1-compatible behavior for anything crossing a
//     process, HTTP, WebSocket, database or plugin boundary.
//   - jsonx.RawMessage is the deferred-encoding type used in tags and payloads.
//
// The v1 compatibility set is json.DefaultOptionsV1(): case-insensitive member
// matching, HTML/JS escaping, nil map/slice as null, duplicate names allowed,
// invalid UTF-8 tolerated, deterministic field order, legacy Marshaler /
// Unmarshaler method semantics, and legacy error reporting (so errors remain
// *json.UnmarshalTypeError / *json.SyntaxError).
package jsonx

import (
	"encoding/json"
	"encoding/json/jsontext"
	jsonv2 "encoding/json/v2"
	"io"
)

// v1 is the option set that reproduces encoding/json (v1) behavior. It is used
// by every function in this package so the migration is semantics-preserving.
var v1 = json.DefaultOptionsV1()

// RawMessage is a raw encoded JSON value. It is the same type as
// encoding/json.RawMessage (an alias of jsontext.Value), so values pass through
// jsonx and v1 APIs interchangeably.
type RawMessage = json.RawMessage

// Marshal returns the JSON encoding of v using v1-compatible semantics.
func Marshal(v any) ([]byte, error) {
	return jsonv2.Marshal(v, v1)
}

// Unmarshal parses the JSON-encoded data and stores the result in the value
// pointed to by v using v1-compatible semantics.
func Unmarshal(data []byte, v any) error {
	return jsonv2.Unmarshal(data, v, v1)
}

// Valid reports whether data is a valid JSON encoding. It mirrors
// json.Valid: syntax only, duplicate object names tolerated.
func Valid(data []byte) bool {
	var probe any
	return jsonv2.Unmarshal(data, &probe, v1) == nil
}

// Encoder writes JSON values to an output stream. Encode terminates each value
// with a newline, matching encoding/json.Encoder.Encode.
type Encoder struct {
	w io.Writer
}

// NewEncoder returns a new encoder that writes to w.
func NewEncoder(w io.Writer) *Encoder {
	return &Encoder{w: w}
}

// Encode writes the JSON encoding of v to the stream, followed by a newline.
func (e *Encoder) Encode(v any) error {
	b, err := Marshal(v)
	if err != nil {
		return err
	}
	b = append(b, '\n')
	_, err = e.w.Write(b)
	return err
}

// Decoder reads and decodes JSON values from an input stream. It is a thin
// wrapper over jsontext.Decoder plus v1-compatible value decoding, so repeated
// Decode calls keep working like encoding/json.Decoder.
type Decoder struct {
	dec *jsontext.Decoder
}

// NewDecoder returns a new decoder that reads from r.
func NewDecoder(r io.Reader) *Decoder {
	return &Decoder{dec: jsontext.NewDecoder(r, v1)}
}

// Decode reads the next JSON-encoded value from its input and stores it in the
// value pointed to by v.
func (d *Decoder) Decode(v any) error {
	return jsonv2.UnmarshalDecode(d.dec, v, v1)
}
