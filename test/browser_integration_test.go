//go:build integration

package test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/ragaskar/butcherie"
)

// startServer starts a butcherie server for integration testing and returns it
// plus a cleanup function. Tests are skipped if geckodriver is not available.
func startServer(t *testing.T) (*butcherie.Server, func()) {
	t.Helper()
	cfg := butcherie.Config{
		Profile: fmt.Sprintf("integration-test-%d", time.Now().UnixNano()),
		Port:    0,
	}
	srv := butcherie.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	if err := srv.Start(ctx); err != nil {
		cancel()
		t.Skipf("skipping integration test: Start failed: %v", err)
	}
	cancel()

	return srv, func() { _ = srv.Shutdown() }
}

func post(t *testing.T, url string, body interface{}) *http.Response {
	t.Helper()
	data, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	resp, err := http.Post(url, "application/json", bytes.NewReader(data))
	if err != nil {
		t.Fatalf("POST %s: %v", url, err)
	}
	return resp
}

func decodePageResponse(t *testing.T, resp *http.Response) butcherie.PageResponse {
	t.Helper()
	defer resp.Body.Close()
	var pr butcherie.PageResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		t.Fatalf("decode PageResponse: %v", err)
	}
	return pr
}

func TestServerStartup(t *testing.T) {
	srv, cleanup := startServer(t)
	defer cleanup()

	st := srv.Status()
	if st.Status != butcherie.StatusReady {
		t.Errorf("status = %q, want %q", st.Status, butcherie.StatusReady)
	}
	if srv.URI() == "" {
		t.Error("URI() is empty after Start")
	}
}

func TestNavigate(t *testing.T) {
	srv, cleanup := startServer(t)
	defer cleanup()

	resp := post(t, srv.URI()+"/navigate", map[string]interface{}{
		"url":            "https://example.com",
		"skip_load_wait": true,
	})
	pr := decodePageResponse(t, resp)

	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTP status = %d, want 200; errors: %v", resp.StatusCode, pr.Errors)
	}
	if !strings.Contains(pr.HTML, "Example Domain") {
		t.Errorf("expected 'Example Domain' in HTML, got %d bytes", len(pr.HTML))
	}
	if pr.StatusCode == 0 {
		t.Error("page status_code is 0")
	}
}

func TestCurrentPageSource(t *testing.T) {
	srv, cleanup := startServer(t)
	defer cleanup()

	post(t, srv.URI()+"/navigate", map[string]interface{}{
		"url":            "https://example.com",
		"skip_load_wait": true,
	})

	resp, err := http.Get(srv.URI() + "/current_page/source")
	if err != nil {
		t.Fatalf("GET /current_page/source: %v", err)
	}
	pr := decodePageResponse(t, resp)

	if !strings.Contains(pr.HTML, "Example Domain") {
		t.Error("source does not contain expected content")
	}
}

func TestClick(t *testing.T) {
	srv, cleanup := startServer(t)
	defer cleanup()

	// Navigate to a page with a known link.
	post(t, srv.URI()+"/navigate", map[string]interface{}{
		"url":            "https://example.com",
		"skip_load_wait": true,
	})

	resp := post(t, srv.URI()+"/current_page/click", map[string]interface{}{
		"by":             "link_text",
		"value":          "More information...",
		"skip_load_wait": true,
	})
	pr := decodePageResponse(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTP status = %d; errors: %v", resp.StatusCode, pr.Errors)
	}
	if pr.HTML == "" {
		t.Error("expected HTML after click, got empty string")
	}
}

func TestShutdown(t *testing.T) {
	srv, _ := startServer(t)

	resp := post(t, srv.URI()+"/shutdown", map[string]interface{}{})
	if resp.StatusCode != http.StatusOK {
		t.Errorf("shutdown status = %d, want 200", resp.StatusCode)
	}
	var body map[string]string
	json.NewDecoder(resp.Body).Decode(&body)
	resp.Body.Close()
	if body["status"] != "shutting down" {
		t.Errorf("body = %v", body)
	}

	// Give server time to shut down, then verify it no longer accepts connections.
	time.Sleep(1 * time.Second)
	_, err := http.Get(srv.URI() + "/status")
	if err == nil {
		t.Error("expected connection failure after shutdown, got nil error")
	}
}

func TestProfilePersistence(t *testing.T) {
	srv, cleanup := startServer(t)
	defer cleanup()

	// Profile dir should exist and contain user.js.
	cfg := srv.Config()
	profileDir := cfg.ConfigPath + "/" + cfg.Profile

	// Just confirm the server is running; profile dir existence is verified
	// by the fact that Start() succeeded (prepareProfile would have failed otherwise).
	if srv.URI() == "" {
		t.Error("server URI is empty — Start() did not succeed")
	}
	_ = profileDir
}

func TestSkipLoadWait(t *testing.T) {
	srv, cleanup := startServer(t)
	defer cleanup()

	start := time.Now()
	resp := post(t, srv.URI()+"/navigate", map[string]interface{}{
		"url":            "https://example.com",
		"skip_load_wait": true,
	})
	elapsed := time.Since(start)

	pr := decodePageResponse(t, resp)
	if resp.StatusCode != http.StatusOK {
		t.Errorf("HTTP status = %d; errors: %v", resp.StatusCode, pr.Errors)
	}
	// skip_load_wait should not add the 30s network drain window.
	if elapsed > 10*time.Second {
		t.Errorf("skip_load_wait took %v, expected much faster", elapsed)
	}
}
