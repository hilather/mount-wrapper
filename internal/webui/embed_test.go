package webui

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestFSHasIndex(t *testing.T) {
	sub, err := FS()
	if err != nil {
		t.Fatal(err)
	}
	f, err := sub.Open("index.html")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "mount-wrapper") {
		t.Fatalf("unexpected index content: %q", string(data)[:min(80, len(data))])
	}
}

func TestHandlerServesIndex(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "mount-wrapper") {
		t.Fatalf("body: %q", rr.Body.String()[:min(80, rr.Body.Len())])
	}
}

func TestHandlerSPAFallback(t *testing.T) {
	h, err := Handler()
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/settings", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status %d", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "mount-wrapper") {
		t.Fatalf("expected SPA shell for /settings")
	}
}
