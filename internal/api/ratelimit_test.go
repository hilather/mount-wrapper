package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/hilather/mount-wrapper/internal/api"
)

func TestDestructiveRateLimitPurge(t *testing.T) {
	be := &fakeBackend{version: "test"}
	srv := api.New(be, api.ServerOptions{
		Bind:                   "127.0.0.1:0",
		Version:                "web-test",
		DestructiveMinInterval: 50 * time.Millisecond,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	body := []byte(`{"archive_id":"a1","yes":true}`)
	post := func() *http.Response {
		res, err := http.Post(ts.URL+"/api/purge", "application/json", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		return res
	}

	res1 := post()
	defer res1.Body.Close()
	if res1.StatusCode != http.StatusOK {
		t.Fatalf("first purge: status=%d", res1.StatusCode)
	}

	res2 := post()
	defer res2.Body.Close()
	if res2.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second purge: want 429, got %d", res2.StatusCode)
	}
	var payload map[string]any
	if err := json.NewDecoder(res2.Body).Decode(&payload); err != nil {
		t.Fatal(err)
	}
	if payload["code"] != "RATE_LIMITED" {
		t.Fatalf("code=%v", payload["code"])
	}

	time.Sleep(60 * time.Millisecond)
	res3 := post()
	defer res3.Body.Close()
	if res3.StatusCode != http.StatusOK {
		t.Fatalf("after wait: status=%d", res3.StatusCode)
	}
}

func TestDestructiveRateLimitRescan(t *testing.T) {
	be := &fakeBackend{version: "test"}
	srv := api.New(be, api.ServerOptions{
		Bind:                   "127.0.0.1:0",
		Version:                "web-test",
		DestructiveMinInterval: time.Hour, // only first succeeds
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	post := func() int {
		res, err := http.Post(ts.URL+"/api/rescan", "application/json", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		return res.StatusCode
	}
	if got := post(); got != http.StatusOK {
		t.Fatalf("first rescan: %d", got)
	}
	if got := post(); got != http.StatusTooManyRequests {
		t.Fatalf("second rescan: want 429 got %d", got)
	}
}

func TestUnmountAllRateLimitedButSingleNot(t *testing.T) {
	be := &fakeBackend{version: "test"}
	srv := api.New(be, api.ServerOptions{
		Bind:                   "127.0.0.1:0",
		Version:                "web-test",
		DestructiveMinInterval: time.Hour,
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	// Single-target unmounts are not rate-limited.
	for i := 0; i < 3; i++ {
		res, err := http.Post(ts.URL+"/api/unmount", "application/json",
			bytes.NewReader([]byte(`{"archive_id":"a1"}`)))
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			t.Fatalf("single unmount %d: status=%d", i, res.StatusCode)
		}
		res.Body.Close()
	}

	// First unmount-all OK, second limited.
	res, err := http.Post(ts.URL+"/api/unmount", "application/json",
		bytes.NewReader([]byte(`{"all":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	if res.StatusCode != http.StatusOK {
		res.Body.Close()
		t.Fatalf("first unmount-all: %d", res.StatusCode)
	}
	res.Body.Close()

	res, err = http.Post(ts.URL+"/api/unmount", "application/json",
		bytes.NewReader([]byte(`{"all":true}`)))
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("second unmount-all: want 429 got %d", res.StatusCode)
	}
}

func TestDestructiveRateLimitDisabled(t *testing.T) {
	be := &fakeBackend{version: "test"}
	srv := api.New(be, api.ServerOptions{
		Bind:                   "127.0.0.1:0",
		Version:                "web-test",
		DestructiveMinInterval: -1, // disable
	})
	ts := httptest.NewServer(srv.Handler())
	t.Cleanup(ts.Close)

	for i := 0; i < 5; i++ {
		res, err := http.Post(ts.URL+"/api/rescan", "application/json", bytes.NewReader([]byte(`{}`)))
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode != http.StatusOK {
			res.Body.Close()
			t.Fatalf("rescan %d: %d", i, res.StatusCode)
		}
		res.Body.Close()
	}
}
