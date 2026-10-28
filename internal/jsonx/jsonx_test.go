package jsonx

import (
	"bytes"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

// TestV1Parity locks the behaviors that third-party payloads rely on: escaping,
// case-insensitive member matching, nil slice/map rendering, duplicate names and
// legacy error types.
func TestV1Parity(t *testing.T) {
	type payload struct {
		Name string   `json:"name"`
		Tags []string `json:"tags"`
		Meta map[string]int
	}

	t.Run("html and js escaping", func(t *testing.T) {
		got, err := Marshal(map[string]string{"v": "<a>&\u2028"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		want, _ := json.Marshal(map[string]string{"v": "<a>&\u2028"})
		if !bytes.Equal(got, want) {
			t.Fatalf("marshal mismatch: got %s want %s", got, want)
		}
	})

	t.Run("case-insensitive member matching", func(t *testing.T) {
		var p payload
		if err := Unmarshal([]byte(`{"NAME":"n","TAGS":["a"]}`), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.Name != "n" || len(p.Tags) != 1 {
			t.Fatalf("unexpected value: %+v", p)
		}
	})

	t.Run("nil slice and map render as null", func(t *testing.T) {
		got, err := Marshal(payload{Name: "x"})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(got) != `{"name":"x","tags":null,"Meta":null}` {
			t.Fatalf("unexpected encoding: %s", got)
		}
	})

	t.Run("duplicate names tolerated", func(t *testing.T) {
		var p payload
		if err := Unmarshal([]byte(`{"name":"a","name":"b"}`), &p); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if p.Name != "b" {
			t.Fatalf("expected last value to win, got %q", p.Name)
		}
	})

	t.Run("legacy error types", func(t *testing.T) {
		var p payload
		err := Unmarshal([]byte(`{"tags":1}`), &p)
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) {
			t.Fatalf("expected *json.UnmarshalTypeError, got %T (%v)", err, err)
		}
		var syntaxErr *json.SyntaxError
		if err := Unmarshal([]byte(`{`), &p); !errors.As(err, &syntaxErr) {
			t.Fatalf("expected *json.SyntaxError, got %T (%v)", err, err)
		}
	})

	t.Run("raw message round trip", func(t *testing.T) {
		var raw RawMessage = []byte(`{"a":1}`)
		out, err := Marshal(map[string]RawMessage{"raw": raw})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if string(out) != `{"raw":{"a":1}}` {
			t.Fatalf("unexpected raw encoding: %s", out)
		}
	})
}

func TestValid(t *testing.T) {
	cases := map[string]bool{
		`{"a":1}`:       true,
		`[1,2,3]`:       true,
		`"str"`:         true,
		`123`:           true,
		`null`:          true,
		``:              false,
		`{`:             false,
		`{"a":1} x`:     false,
		`{"a":}`:        false,
		`{"a":1,"a":2}`: true,
	}
	for in, want := range cases {
		if got := Valid([]byte(in)); got != want {
			t.Errorf("Valid(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestEncoderDecoder(t *testing.T) {
	var buf bytes.Buffer
	enc := NewEncoder(&buf)
	if err := enc.Encode(map[string]int{"a": 1}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if err := enc.Encode(map[string]int{"b": 2}); err != nil {
		t.Fatalf("encode: %v", err)
	}
	if got, want := buf.String(), "{\"a\":1}\n{\"b\":2}\n"; got != want {
		t.Fatalf("encoded stream = %q, want %q", got, want)
	}

	dec := NewDecoder(strings.NewReader(buf.String()))
	for _, want := range []string{"a", "b"} {
		var m map[string]int
		if err := dec.Decode(&m); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if _, ok := m[want]; !ok {
			t.Fatalf("decoded %v, missing key %q", m, want)
		}
	}
}
