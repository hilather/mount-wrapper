package control_test

import (
	"testing"

	"github.com/hilather/mount-wrapper/internal/control"
)

// FuzzParseRequest ensures ParseRequest never panics and only returns
// structured control.Error (or nil) for arbitrary line input.
func FuzzParseRequest(f *testing.F) {
	seeds := []string{
		``,
		`{}`,
		`{"v":1,"op":"status"}`,
		`{"op":"rescan"}`,
		`{"v":99,"op":"status"}`,
		`{"v":1}`,
		`{not json`,
		`null`,
		`[]`,
		`"string"`,
		`{"v":"x","op":"status"}`,
		`{"v":1,"op":""}`,
		`{"v":1.5,"op":"status"}`,
		`{"v":1,"op":"status","extra":{"nested":true}}`,
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, line string) {
		req, err := control.ParseRequest(line)
		if err != nil {
			if _, ok := err.(*control.Error); !ok {
				t.Fatalf("unexpected error type %T: %v", err, err)
			}
			if req != nil {
				t.Fatalf("expected nil req on error, got %v", req)
			}
			return
		}
		if req == nil {
			t.Fatal("nil req without error")
		}
		op, _ := req["op"].(string)
		if op == "" {
			t.Fatal("empty op on success")
		}
		if _, ok := req["v"]; !ok {
			t.Fatal("missing v on success")
		}
	})
}
