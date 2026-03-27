package butcherie

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/tebeka/selenium"
)

// Status is the lifecycle state of the browser.
type Status string

const (
	StatusStarting Status = "starting"
	StatusReady    Status = "ready"
	StatusFailed   Status = "failed"
)

// StatusResponse describes the lifecycle state of the browser.
type StatusResponse struct {
	Status Status   `json:"status"`
	Errors []string `json:"errors,omitempty"`
}

// Config holds all options for creating a Butcherie instance.
type Config struct {
	Profile         string        // required: profile name (e.g. "vine-reviews")
	Port            int           // TCP port for geckodriver; 0 → OS chooses a free port
	ConfigPath      string        // default: ~/.butcherie
	PostActionDelay time.Duration // sleep after navigate/click; default 1.5s
}

// PageResponse is returned by Navigate, Click, and Source.
type PageResponse struct {
	HTML       string              `json:"html"`
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Errors     []string            `json:"errors,omitempty"`
}

// LoadOptions controls progressive-load waiting behaviour for Navigate and Click.
type LoadOptions struct {
	// IgnoreRequests is a list of regexes matched against request URIs.
	// In-flight requests whose URI matches any pattern are not waited on.
	IgnoreRequests []string `json:"ignore_requests,omitempty"`
	// SkipLoadWait disables scrolling and network draining entirely.
	SkipLoadWait bool `json:"skip_load_wait,omitempty"`
	// Timeout is how long to wait for network requests to complete.
	// Defaults to 30 seconds if zero.
	Timeout time.Duration `json:"timeout,omitempty"`
}

// Butcherie drives a real Firefox browser.
type Butcherie struct {
	cfg        Config
	mu         sync.RWMutex
	wd         WebDriver
	cdp        *cdpClient
	geckoPort  int
	firefoxPID int
}

// New creates a Butcherie instance. Does not start Firefox yet.
func New(cfg Config) *Butcherie {
	if cfg.ConfigPath == "" {
		home, _ := os.UserHomeDir()
		cfg.ConfigPath = filepath.Join(home, ".butcherie")
	}
	if cfg.PostActionDelay == 0 {
		cfg.PostActionDelay = 1500 * time.Millisecond
	}
	return &Butcherie{cfg: cfg}
}

// StartBrowser launches Firefox, blocking until Firefox is ready
// (or the context is cancelled/times out). The context is the primary mechanism
// for controlling startup timeout — almost all callers should pass a context
// with a deadline:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
//	defer cancel()
//	if err := b.StartBrowser(ctx); err != nil { ... }
//
// Passing context.Background() with no deadline relies on the OS to surface
// errors (e.g. geckodriver not found) but will block indefinitely if Firefox
// hangs during startup.
func (b *Butcherie) StartBrowser(ctx context.Context) error {
	type launchResult struct {
		wd        WebDriver
		cdp       *cdpClient
		geckoPort int
		pid       int
		err       error
	}
	ch := make(chan launchResult, 1)
	go func() {
		wd, geckoPort, err := buildDriver(b.cfg)
		if err != nil {
			ch <- launchResult{err: err}
			return
		}
		pid, err := extractFirefoxPID(wd)
		if err != nil {
			_ = wd.Quit()
			ch <- launchResult{err: fmt.Errorf("extract firefox PID: %w", err)}
			return
		}
		cdp, err := newCDPClient(geckoPort, wd.SessionID())
		if err != nil {
			_ = wd.Quit()
			ch <- launchResult{err: fmt.Errorf("connect CDP: %w", err)}
			return
		}
		ch <- launchResult{wd: wd, cdp: cdp, geckoPort: geckoPort, pid: pid}
	}()

	select {
	case <-ctx.Done():
		return fmt.Errorf("startup timed out: %w", ctx.Err())
	case r := <-ch:
		if r.err != nil {
			return r.err
		}
		b.mu.Lock()
		b.wd = r.wd
		b.cdp = r.cdp
		b.geckoPort = r.geckoPort
		b.firefoxPID = r.pid
		b.mu.Unlock()
	}

	go b.watchFirefox()
	return nil
}

// Navigate navigates Firefox to url, waits for progressive loading to complete,
// and returns the final page HTML, HTTP status code, and response headers.
func (b *Butcherie) Navigate(ctx context.Context, url string, opts LoadOptions) (PageResponse, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	captureResp := b.cdp.captureDocumentResponse()

	if err := b.wd.Get(url); err != nil {
		return PageResponse{Errors: []string{err.Error()}}, err
	}

	time.Sleep(b.cfg.PostActionDelay)

	loadErr := b.ensureLoaded(ctx, opts)

	respDone := make(chan struct{})
	close(respDone)
	statusCode, headers := captureResp(respDone)

	html, err := b.wd.PageSource()
	if err != nil {
		return PageResponse{Errors: []string{err.Error()}}, err
	}

	resp := PageResponse{
		HTML:       html,
		StatusCode: statusCode,
		Headers:    headers,
	}
	if loadErr != nil {
		resp.Errors = []string{loadErr.Error()}
	}
	return resp, loadErr
}

// Click finds an element using the given strategy and value, clicks it,
// waits for progressive loading, and returns the resulting page.
// Supported strategies: link_text, partial_link_text, id, css, xpath, name, class_name, tag_name.
func (b *Butcherie) Click(ctx context.Context, by, value string, opts LoadOptions) (PageResponse, error) {
	seBy := seleniumBy(by)
	if seBy == "" {
		return PageResponse{}, fmt.Errorf("unsupported by strategy: %s", by)
	}

	b.mu.Lock()
	defer b.mu.Unlock()

	elem, err := b.wd.FindElement(seBy, value)
	if err != nil {
		return PageResponse{}, fmt.Errorf("element not found: %w", err)
	}

	captureResp := b.cdp.captureDocumentResponse()

	if err := elem.Click(); err != nil {
		return PageResponse{}, fmt.Errorf("click failed: %w", err)
	}

	time.Sleep(b.cfg.PostActionDelay)

	loadErr := b.ensureLoaded(ctx, opts)

	respDone := make(chan struct{})
	close(respDone)
	statusCode, headers := captureResp(respDone)

	html, err := b.wd.PageSource()
	if err != nil {
		return PageResponse{}, err
	}

	resp := PageResponse{
		HTML:       html,
		StatusCode: statusCode,
		Headers:    headers,
	}
	if loadErr != nil {
		resp.Errors = []string{loadErr.Error()}
	}
	return resp, loadErr
}

// Source returns the current page HTML. StatusCode and Headers are not populated
// (no navigation is occurring).
func (b *Butcherie) Source() (PageResponse, error) {
	b.mu.RLock()
	defer b.mu.RUnlock()

	html, err := b.wd.PageSource()
	if err != nil {
		return PageResponse{Errors: []string{err.Error()}}, err
	}
	return PageResponse{HTML: html}, nil
}

// Config returns the configuration used to create this Butcherie instance.
func (b *Butcherie) Config() Config {
	return b.cfg
}

// StopBrowser closes Firefox (with SIGKILL fallback).
func (b *Butcherie) StopBrowser() error {
	b.mu.RLock()
	wd := b.wd
	pid := b.firefoxPID
	cdp := b.cdp
	b.mu.RUnlock()

	if cdp != nil {
		cdp.close()
	}
	if wd != nil {
		_ = wd.Quit()
	}
	if pid > 0 {
		deadline := time.Now().Add(30 * time.Second)
		for time.Now().Before(deadline) {
			proc, err := os.FindProcess(pid)
			if err != nil {
				break
			}
			if err := proc.Signal(syscall.Signal(0)); err != nil {
				break
			}
			time.Sleep(500 * time.Millisecond)
		}
		if proc, err := os.FindProcess(pid); err == nil {
			if err := proc.Signal(syscall.Signal(0)); err == nil {
				_ = proc.Signal(syscall.SIGKILL)
			}
		}
	}
	return nil
}

// seleniumBy maps the "by" strategy strings to selenium constants.
func seleniumBy(by string) string {
	switch by {
	case "link_text":
		return selenium.ByLinkText
	case "partial_link_text":
		return selenium.ByPartialLinkText
	case "id":
		return selenium.ByID
	case "css":
		return selenium.ByCSSSelector
	case "xpath":
		return selenium.ByXPATH
	case "name":
		return selenium.ByName
	case "class_name":
		return selenium.ByClassName
	case "tag_name":
		return selenium.ByTagName
	default:
		return ""
	}
}
