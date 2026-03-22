package butcherie

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

// Config holds all options for creating a Server.
type Config struct {
	Profile         string        // required: profile name (e.g. "vine-reviews")
	Port            int           // TCP port; 0 → OS chooses a free port
	ConfigPath      string        // default: ~/.butcherie
	PostActionDelay time.Duration // sleep after navigate/click; default 1.5s
}

// StartupStatus is the lifecycle state of the browser.
type StartupStatus string

const (
	StatusStarting StartupStatus = "starting"
	StatusReady    StartupStatus = "ready"
	StatusFailed   StartupStatus = "failed"
)

// StatusResponse matches the JSON shape returned by GET /status.
type StatusResponse struct {
	Status StartupStatus `json:"status"`
	Errors []string      `json:"errors,omitempty"`
}

// PageResponse is returned by /navigate, /click, and /current_page/source.
type PageResponse struct {
	HTML       string              `json:"html"`
	StatusCode int                 `json:"status_code"`
	Headers    map[string][]string `json:"headers"`
	Errors     []string            `json:"errors,omitempty"`
}

// LoadOptions controls progressive-load waiting behaviour for navigate and click.
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

// Server is a running butcherie API server.
type Server struct {
	cfg        Config
	mu         sync.RWMutex
	wd         WebDriver
	cdp        *cdpClient
	geckoPort  int
	httpServer *http.Server
	listener   net.Listener
	status     StatusResponse
	firefoxPID int
}

// New creates a Server. Does not start Firefox or the HTTP listener yet.
func New(cfg Config) *Server {
	if cfg.ConfigPath == "" {
		home, _ := os.UserHomeDir()
		cfg.ConfigPath = filepath.Join(home, ".butcherie")
	}
	if cfg.PostActionDelay == 0 {
		cfg.PostActionDelay = 1500 * time.Millisecond
	}
	return &Server{
		cfg:    cfg,
		status: StatusResponse{Status: StatusStarting},
	}
}

// Start launches the HTTP listener and Firefox, blocking until Firefox is ready
// (or the context is cancelled/times out). The context is the primary mechanism
// for controlling startup timeout — almost all callers should pass a context
// with a deadline:
//
//	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
//	defer cancel()
//	if err := s.Start(ctx); err != nil { ... }
//
// Passing context.Background() with no deadline relies on the OS to surface
// errors (e.g. geckodriver not found) but will block indefinitely if Firefox
// hangs during startup.
func (s *Server) Start(ctx context.Context) error {
	addr := fmt.Sprintf(":%d", s.cfg.Port)
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}
	s.listener = ln

	mux := s.buildMux()
	s.httpServer = &http.Server{Handler: mux}

	type launchResult struct {
		wd        WebDriver
		cdp       *cdpClient
		geckoPort int
		pid       int
		err       error
	}
	ch := make(chan launchResult, 1)
	go func() {
		wd, geckoPort, err := buildDriver(s.cfg)
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
		_ = ln.Close()
		return fmt.Errorf("startup timed out: %w", ctx.Err())
	case r := <-ch:
		if r.err != nil {
			_ = ln.Close()
			s.mu.Lock()
			s.status = StatusResponse{Status: StatusFailed, Errors: []string{r.err.Error()}}
			s.mu.Unlock()
			return r.err
		}
		s.mu.Lock()
		s.wd = r.wd
		s.cdp = r.cdp
		s.geckoPort = r.geckoPort
		s.firefoxPID = r.pid
		s.status = StatusResponse{Status: StatusReady}
		s.mu.Unlock()
	}

	go s.watchFirefox()
	go func() { _ = s.httpServer.Serve(ln) }()
	return nil
}

// Status returns the current startup state without blocking.
func (s *Server) Status() StatusResponse {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.status
}

// URI returns the base URL the HTTP server is listening on (e.g. "http://localhost:31331").
func (s *Server) URI() string {
	if s.listener == nil {
		return ""
	}
	return fmt.Sprintf("http://localhost:%d", s.listener.Addr().(*net.TCPAddr).Port)
}

// Shutdown closes Firefox (with SIGKILL fallback) and stops the HTTP server.
func (s *Server) Shutdown() error {
	s.mu.RLock()
	wd := s.wd
	pid := s.firefoxPID
	cdp := s.cdp
	s.mu.RUnlock()

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

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if s.httpServer != nil {
		return s.httpServer.Shutdown(ctx)
	}
	return nil
}
