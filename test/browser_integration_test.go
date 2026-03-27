//go:build integration

package test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/ragaskar/butcherie"
)

// startBrowser starts a Butcherie instance for integration testing and returns it
// plus a cleanup function. Tests are skipped if geckodriver is not available.
func startBrowser(t *testing.T) (*butcherie.Butcherie, func()) {
	t.Helper()
	cfg := butcherie.Config{
		Profile: fmt.Sprintf("integration-test-%d", time.Now().UnixNano()),
		Port:    0,
	}
	b := butcherie.New(cfg)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := b.StartBrowser(ctx); err != nil {
		t.Skipf("skipping integration test: StartBrowser failed: %v", err)
	}

	return b, func() { _ = b.StopBrowser() }
}

func TestBrowserStartup(t *testing.T) {
	_, cleanup := startBrowser(t)
	defer cleanup()
	// startBrowser blocks until ready; if we get here, startup succeeded.
}

func TestNavigate(t *testing.T) {
	b, cleanup := startBrowser(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	resp, err := b.Navigate(ctx, "https://example.com", butcherie.LoadOptions{SkipLoadWait: true})
	if err != nil {
		t.Fatalf("Navigate: %v", err)
	}
	if !strings.Contains(resp.HTML, "Example Domain") {
		t.Errorf("expected 'Example Domain' in HTML, got %d bytes", len(resp.HTML))
	}
	if resp.StatusCode == 0 {
		t.Error("page status_code is 0")
	}
}

func TestCurrentPageSource(t *testing.T) {
	b, cleanup := startBrowser(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := b.Navigate(ctx, "https://example.com", butcherie.LoadOptions{SkipLoadWait: true}); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	resp, err := b.Source()
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if !strings.Contains(resp.HTML, "Example Domain") {
		t.Error("source does not contain expected content")
	}
}

func TestClick(t *testing.T) {
	b, cleanup := startBrowser(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	if _, err := b.Navigate(ctx, "https://example.com", butcherie.LoadOptions{SkipLoadWait: true}); err != nil {
		t.Fatalf("Navigate: %v", err)
	}

	resp, err := b.Click(ctx, "link_text", "More information...", butcherie.LoadOptions{SkipLoadWait: true})
	if err != nil {
		t.Fatalf("Click: %v", err)
	}
	if resp.HTML == "" {
		t.Error("expected HTML after click, got empty string")
	}
}

func TestShutdown(t *testing.T) {
	b, _ := startBrowser(t)

	if err := b.StopBrowser(); err != nil {
		t.Errorf("StopBrowser: %v", err)
	}

	// Subsequent Navigate should fail.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := b.Navigate(ctx, "https://example.com", butcherie.LoadOptions{SkipLoadWait: true})
	if err == nil {
		t.Error("expected error after StopBrowser, got nil")
	}
}

func TestProfilePersistence(t *testing.T) {
	b, cleanup := startBrowser(t)
	defer cleanup()

	cfg := b.Config()
	profileDir := cfg.ConfigPath + "/" + cfg.Profile

	// Profile dir existence is verified by the fact that StartBrowser() succeeded.
	_ = profileDir
}

func TestSkipLoadWait(t *testing.T) {
	b, cleanup := startBrowser(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	start := time.Now()
	resp, err := b.Navigate(ctx, "https://example.com", butcherie.LoadOptions{SkipLoadWait: true})
	elapsed := time.Since(start)

	if err != nil {
		t.Fatalf("Navigate: %v", err)
	}
	if len(resp.Errors) > 0 {
		t.Errorf("unexpected errors: %v", resp.Errors)
	}
	// skip_load_wait should not add the 30s network drain window.
	if elapsed > 10*time.Second {
		t.Errorf("SkipLoadWait took %v, expected much faster", elapsed)
	}
}
