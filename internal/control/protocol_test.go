package control_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/hilather/mount-wrapper/internal/control"
)

func TestParseRequestOK(t *testing.T) {
	t.Parallel()
	req, err := control.ParseRequest(`{"v":1,"op":"status"}`)
	if err != nil {
		t.Fatal(err)
	}
	if req["op"] != "status" {
		t.Fatalf("op=%v", req["op"])
	}
	if req["v"] != 1 {
		t.Fatalf("v=%v want 1", req["v"])
	}
}

func TestParseRequestDefaultVersion(t *testing.T) {
	t.Parallel()
	req, err := control.ParseRequest(`{"op":"rescan"}`)
	if err != nil {
		t.Fatal(err)
	}
	if req["v"] != 1 {
		t.Fatalf("v=%v want 1", req["v"])
	}
}

func TestParseRequestUnsupportedVersion(t *testing.T) {
	t.Parallel()
	_, err := control.ParseRequest(`{"v":99,"op":"status"}`)
	if err == nil {
		t.Fatal("expected error")
	}
	ce, ok := err.(*control.Error)
	if !ok {
		t.Fatalf("type %T", err)
	}
	if ce.Code != "UNSUPPORTED_VERSION" {
		t.Fatalf("code=%s", ce.Code)
	}
	if !strings.Contains(ce.Message, "unsupported protocol") {
		t.Fatalf("msg=%s", ce.Message)
	}
}

func TestParseRequestMissingOp(t *testing.T) {
	t.Parallel()
	_, err := control.ParseRequest(`{"v":1}`)
	if err == nil {
		t.Fatal("expected error")
	}
	ce := err.(*control.Error)
	if ce.Code != "BAD_REQUEST" {
		t.Fatalf("code=%s", ce.Code)
	}
}

func TestParseRequestBadJSON(t *testing.T) {
	t.Parallel()
	_, err := control.ParseRequest(`{not json`)
	if err == nil {
		t.Fatal("expected error")
	}
	ce := err.(*control.Error)
	if ce.Code != "BAD_REQUEST" {
		t.Fatalf("code=%s", ce.Code)
	}
	if !strings.Contains(ce.Message, "invalid JSON") {
		t.Fatalf("msg=%s", ce.Message)
	}
}

func TestParseRequestJSONNumberVersion(t *testing.T) {
	t.Parallel()
	// encoding path that yields json.Number via UseNumber.
	req, err := control.ParseRequest(`{"v":1,"op":"status","x":2}`)
	if err != nil {
		t.Fatal(err)
	}
	if req["v"] != 1 {
		t.Fatalf("v=%v (%T)", req["v"], req["v"])
	}
}

func TestOKErrResponse(t *testing.T) {
	t.Parallel()
	ok := control.OKResponse(map[string]any{"a": 1})
	if ok["ok"] != true {
		t.Fatalf("%+v", ok)
	}
	data, _ := ok["data"].(map[string]any)
	if data["a"] != 1 {
		t.Fatalf("%+v", data)
	}
	empty := control.OKResponse(nil)
	if _, isMap := empty["data"].(map[string]any); !isMap {
		t.Fatalf("nil data should be empty map: %+v", empty)
	}
	er := control.ErrResponse("nope", "BAD_REQUEST")
	if er["ok"] != false || er["error"] != "nope" || er["code"] != "BAD_REQUEST" {
		t.Fatalf("%+v", er)
	}
}

func TestEncodeResponseRoundtrip(t *testing.T) {
	t.Parallel()
	raw, err := control.EncodeResponse(control.OKResponse(map[string]any{"op": "status"}))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(string(raw), "\n") {
		t.Fatalf("expected trailing newline: %q", raw)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if m["ok"] != true {
		t.Fatalf("%+v", m)
	}
}
