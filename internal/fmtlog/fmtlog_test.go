package fmtlog

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

type badValue struct{}

func (badValue) MarshalJSON() ([]byte, error) { return nil, &json.UnsupportedValueError{} }

func TestLogger(t *testing.T) {
	var out bytes.Buffer
	l := New(&out, "svc", "test")
	l.Log("info", "hello", map[string]any{"kept": 1, "nil": nil, "empty": ""})
	var line map[string]any
	if err := json.Unmarshal(out.Bytes(), &line); err != nil {
		t.Fatal(err)
	}
	if line["service.name"] != "svc" || line["kept"] != float64(1) {
		t.Fatalf("unexpected log: %#v", line)
	}
	if _, ok := line["nil"]; ok {
		t.Fatal("nil field was retained")
	}

	before := out.Len()
	l.Log("info", "bad", map[string]any{"bad": make(chan int)})
	if out.Len() != before {
		t.Fatal("marshal failure wrote output")
	}

	Init("", "")
	if Default().service != "tech-internal-api" || Default().env != "dev" {
		t.Fatalf("bad defaults: %#v", Default())
	}
	var global bytes.Buffer
	defaultLogger = New(&global, "svc", "env")
	Info("i", nil)
	Warn("w", nil)
	Error("e", nil)
	if got := strings.Count(global.String(), "\n"); got != 3 {
		t.Fatalf("global lines = %d", got)
	}
	if New(nil, "s", "e").out == nil {
		t.Fatal("nil writer not defaulted")
	}
	id := NewRequestID()
	if len(id) != 32 {
		t.Fatalf("request id length = %d", len(id))
	}
}
