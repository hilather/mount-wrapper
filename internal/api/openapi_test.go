package api_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

// openAPIDoc is a minimal structural view of docs/openapi.yaml (D11 hand-written).
type openAPIDoc struct {
	OpenAPI string `yaml:"openapi"`
	Info    struct {
		Title   string `yaml:"title"`
		Version string `yaml:"version"`
	} `yaml:"info"`
	Paths      map[string]map[string]any `yaml:"paths"`
	Components struct {
		Schemas         map[string]any `yaml:"schemas"`
		Responses       map[string]any `yaml:"responses"`
		SecuritySchemes map[string]any `yaml:"securitySchemes"`
	} `yaml:"components"`
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("no caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

// TestOpenAPIDocument loads docs/openapi.yaml and asserts required /api/* paths,
// /metrics, components.schemas keys, and that successful 200 responses reference
// schemas (not description-only).
func TestOpenAPIDocument(t *testing.T) {
	t.Parallel()
	path := filepath.Join(repoRoot(t), "docs", "openapi.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}

	var doc openAPIDoc
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("yaml parse: %v", err)
	}
	if !strings.HasPrefix(doc.OpenAPI, "3.") {
		t.Fatalf("openapi version %q, want 3.x", doc.OpenAPI)
	}
	if doc.Info.Title == "" {
		t.Fatal("info.title empty")
	}
	if doc.Info.Version == "" {
		t.Fatal("info.version empty")
	}

	requiredPaths := []string{
		"/api/health",
		"/api/status",
		"/api/status/sizes",
		"/api/archives",
		"/api/metrics",
		"/api/config",
		"/api/rescan",
		"/api/unmount",
		"/api/retry",
		"/api/purge",
		"/api/hooks",
		"/api/doctor",
		"/api/wsl-info",
		"/api/events",
		"/metrics",
	}
	for _, p := range requiredPaths {
		if _, ok := doc.Paths[p]; !ok {
			t.Errorf("missing path %s", p)
		}
	}

	requiredSchemas := []string{
		"ErrorBody",
		"Health",
		"Status",
		"Archive",
		"ArchiveMetrics",
		"MetricsSummary",
		"MetricsListResponse",
		"ArchivesPayload",
		"ConfigGetResponse",
		"ConfigSetResponse",
		"RescanResponse",
		"PurgeResponse",
		"DoctorReport",
		"DoctorCheck",
		"HooksListResponse",
		"HooksStatusResponse",
		"HookRow",
		"WSLInfo",
		"Counts",
	}
	if doc.Components.Schemas == nil {
		t.Fatal("components.schemas missing")
	}
	for _, name := range requiredSchemas {
		if _, ok := doc.Components.Schemas[name]; !ok {
			t.Errorf("missing components.schemas.%s", name)
		}
	}

	// Shared error response components used by 401/429.
	for _, name := range []string{"Unauthorized", "RateLimited", "Error"} {
		if _, ok := doc.Components.Responses[name]; !ok {
			t.Errorf("missing components.responses.%s", name)
		}
	}
	if _, ok := doc.Components.SecuritySchemes["bearerAuth"]; !ok {
		t.Error("missing components.securitySchemes.bearerAuth")
	}

	// Every path operation with a "200" must not be description-only:
	// require content schema $ref / oneOf / schema, or (SSE/metrics) a content map.
	for path, methods := range doc.Paths {
		for method, opAny := range methods {
			// Skip OpenAPI path-item extensions / parameters if any.
			if method == "parameters" || strings.HasPrefix(method, "x-") {
				continue
			}
			op, ok := opAny.(map[string]any)
			if !ok {
				continue
			}
			resps, _ := op["responses"].(map[string]any)
			if resps == nil {
				t.Errorf("%s %s: no responses", method, path)
				continue
			}
			r200, ok := resps["200"]
			if !ok {
				// Optional; some ops may only document errors — still require 200 for our API.
				t.Errorf("%s %s: missing 200 response", method, path)
				continue
			}
			if !responseHasSchema(r200) {
				t.Errorf("%s %s: 200 response is description-only (need content schema or $ref)", method, path)
			}
		}
	}

	// Rate-limited POSTs should document 429.
	for _, p := range []string{"/api/rescan", "/api/unmount", "/api/purge"} {
		post, ok := doc.Paths[p]["post"].(map[string]any)
		if !ok {
			t.Errorf("%s: missing post", p)
			continue
		}
		resps, _ := post["responses"].(map[string]any)
		if _, ok := resps["429"]; !ok {
			t.Errorf("%s post: missing 429 RATE_LIMITED response", p)
		}
	}

	// Body text must mention RATE_LIMITED for discoverability.
	if !strings.Contains(string(raw), "RATE_LIMITED") {
		t.Error("openapi.yaml should document RATE_LIMITED")
	}
}

// responseHasSchema reports whether a response object references a real schema
// (inline content, $ref to components.responses, or schema under content).
func responseHasSchema(resp any) bool {
	m, ok := resp.(map[string]any)
	if !ok {
		return false
	}
	if ref, _ := m["$ref"].(string); ref != "" {
		// components.responses.* are allowed; they carry schemas.
		return strings.Contains(ref, "components/responses") || strings.Contains(ref, "components/schemas")
	}
	content, _ := m["content"].(map[string]any)
	if content == nil {
		return false
	}
	for _, mediaAny := range content {
		media, ok := mediaAny.(map[string]any)
		if !ok {
			continue
		}
		schema, ok := media["schema"].(map[string]any)
		if !ok || schema == nil {
			continue
		}
		if ref, _ := schema["$ref"].(string); ref != "" {
			return true
		}
		if _, hasOneOf := schema["oneOf"]; hasOneOf {
			return true
		}
		if _, hasAllOf := schema["allOf"]; hasAllOf {
			return true
		}
		if t, _ := schema["type"].(string); t != "" {
			// Primitive/object schema (e.g. text/plain string for Prometheus / SSE).
			return true
		}
	}
	return false
}
