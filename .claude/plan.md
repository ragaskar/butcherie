# butcherie: Implementation Plan

A Go library that drives a real Firefox browser via Selenium/geckodriver.

---

## Technology choices

| Concern | Choice | Rationale |
|---|---|---|
| WebDriver | `github.com/tebeka/selenium` | geckodriver + W3C WebDriver stack; well-maintained Go bindings |
| Integration test gating | `//go:build integration` build tag | Standard Go idiom; no runtime env setup needed |
| Network request tracking | Firefox DevTools Protocol (via geckodriver) | Sees all requests without injection timing risks; no extra dependencies |

---

## Architecture

butcherie is a single library layer:

**Core library** (`butcherie.go`) — implements browser automation directly. Go callers use `Navigate`, `Click`, and `Source` on a `*Butcherie` value. There is no HTTP server and no CLI.

This means a Go application imports `butcherie` and drives Firefox programmatically. Future HTTP or CLI wrappers can be added later if needed, and should be thin shims over this library.

---

## Directory structure

```
butcherie/
├── go.mod                          module github.com/ragaskar/butcherie
├── go.sum
│
├── *.go                            package butcherie  (importable library)
│   ├── butcherie.go                core library: Config, Butcherie lifecycle + Navigate/Click/Source
│   ├── driver.go                   WebDriver setup: profile dir, user.js, build_driver
│   ├── loader.go                   ensureLoaded: incremental scroll + DevTools network drain
│   ├── cdp.go                      Firefox DevTools Protocol WebSocket client
│   ├── watcher.go                  goroutine that detects Firefox closure
│   ├── butcherie_test.go           unit tests for core library (no build tag)
│   ├── driver_test.go              unit tests (no build tag)
│   └── loader_test.go              unit tests (no build tag)
│
└── test/
    └── browser_integration_test.go //go:build integration — actually launches Firefox
```

**Module/package name**: the library is `package butcherie` at the module root,
so callers import it as `"github.com/ragaskar/butcherie"`.

---

## Public API (package butcherie)

```go
// Config holds all options for creating a Butcherie instance.
type Config struct {
    Profile         string        // required: profile name (e.g. "vine-reviews")
    Port            int           // TCP port for geckodriver; 0 → OS chooses a free port
    ConfigPath      string        // default: ~/.butcherie
    PostActionDelay time.Duration // sleep after navigate/click; default 1.5s
}

// Status is the lifecycle state of the browser.
type Status string
const (
    StatusStarting Status = "starting"
    StatusReady    Status = "ready"
    StatusFailed   Status = "failed"
)

// StatusResponse is the lifecycle state of the browser.
type StatusResponse struct {
    Status Status `json:"status"`
    Errors []string      `json:"errors,omitempty"`
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
type Butcherie struct { /* unexported fields */ }

// New creates a Butcherie instance. Does not start Firefox yet.
func New(cfg Config) *Butcherie

// Start launches Firefox, blocking until Firefox is ready
// (or the context is cancelled/times out). The context is the primary mechanism
// for controlling startup timeout — almost all callers should pass a context
// with a deadline:
//
//   ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
//   defer cancel()
//   if err := b.Start(ctx); err != nil { ... }
//
// Passing context.Background() with no deadline relies on the OS to surface
// errors (e.g. geckodriver not found) but will block indefinitely if Firefox
// hangs during startup.
func (b *Butcherie) StartBrowser(ctx context.Context) error

// Navigate navigates Firefox to url, waits for progressive loading to complete,
// and returns the final page HTML, HTTP status code, and response headers.
func (b *Butcherie) Navigate(ctx context.Context, url string, opts LoadOptions) (PageResponse, error)

// Click finds an element using the given strategy and value, clicks it,
// waits for progressive loading, and returns the resulting page.
// Supported strategies: link_text, partial_link_text, id, css, xpath, name, class_name, tag_name.
func (b *Butcherie) Click(ctx context.Context, by, value string, opts LoadOptions) (PageResponse, error)

// Source returns the current page HTML. StatusCode and Headers are not populated
// (no navigation is occurring).
func (b *Butcherie) Source() (PageResponse, error)

// Shutdown closes Firefox (with SIGKILL fallback).
func (b *Butcherie) StopBrowser() error
```

---

## Implementation details

### Profile management (`driver.go`)

```
profileDir = filepath.Join(ConfigPath, profile)
// expand ~ via os.UserHomeDir()
// os.MkdirAll(profileDir, 0o755)
// write user.js with the 6 Firefox startup prefs
```

**Profile base dir**: `~/.butcherie/` (overridable via `Config.ConfigPath`).

### WebDriver setup (`driver.go`)

```go
service, _ := selenium.NewGeckoDriverService(geckodriverPath, port)
caps := selenium.Capabilities{"browserName": "firefox"}
// Pass -profile <profileDir> as Firefox CLI arg
caps.AddFirefox(firefox.Capabilities{
    Args: []string{"-profile", profileDir},
})
wd, _ := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", port))
```

Firefox PID extraction: `wd.Capabilities()["moz:processID"]`.

### Firefox watcher (`watcher.go`)

Goroutine launched after Firefox is ready.  Every 3 seconds calls `wd.Title()`;
any error means Firefox is gone → call `os.Exit(0)`.

### Progressive load waiting (`loader.go`)

`ensureLoaded(ctx context.Context, ignoreRequests []string)` is called after every Navigate and Click (unless skipped). The context carries the deadline — callers should pass a context with a timeout (default 30 s if the incoming context carries no deadline). Steps:

1. **Incremental scroll**: repeatedly execute `window.scrollBy(0, window.innerHeight)` via `wd.ExecuteScript`, pausing briefly between steps, until `window.scrollY + window.innerHeight >= document.body.scrollHeight`.
2. **Network drain**: via Firefox DevTools Protocol, subscribe to network events (`Network.requestWillBeSent`, `Network.loadingFinished`, `Network.loadingFailed`) to track in-flight requests. Wait until all pending requests complete or `ctx` is done.
   - Requests whose URI matches any regex in `ignoreRequests` are excluded from tracking.
3. **Timeout error**: if `ctx` expires while non-ignored requests are still pending, return an error containing the request details (target URL, method, POST payload if any).

The `Timeout` field in `LoadOptions` is still accepted; Navigate/Click wrap the incoming context with `context.WithTimeout` using that value before calling `ensureLoaded`.

Firefox DevTools Protocol is accessed via geckodriver's WebSocket endpoint: `ws://localhost:<geckoport>/session/<sessionId>/moz/cdp/`.

### Core library (`butcherie.go`)

`Navigate`, `Click`, and `Source` implement all browser automation logic directly:
- Acquire the instance mutex (operations are serialised — the browser can only do one thing at a time).
- Subscribe to CDP document response capture before the action.
- Call the WebDriver action (`wd.Get`, `elem.Click`).
- Sleep `PostActionDelay`.
- Call `ensureLoaded`.
- Return `PageResponse`.

### Shutdown sequence (`butcherie.go`)

1. Call `wd.Quit()` (closes Firefox via geckodriver).
2. Poll `os.FindProcess(firefoxPID)` + `process.Signal(syscall.Signal(0))` for
   up to 30 seconds.
3. If still alive after 30 s, `process.Signal(syscall.SIGKILL)`.

---

## Testing strategy

### Unit tests (no build tag — always run)

| File | What is tested |
|---|---|
| `driver_test.go` | `writeProfilePrefs` writes correct `user.js` content; `profileDir` expansion |
| `butcherie_test.go` | `Config` defaults, `StartBrowser` context cancellation/timeout; `Navigate`/`Click`/`Source` with a stub WebDriver |
| `loader_test.go` | `ensureLoaded`: scroll loop terminates, `ignore_requests` regex filtering, timeout error contains request details, `skip_load_wait` bypasses all behaviour |

**WebDriver interface**: to make the library testable without geckodriver, extract a
`WebDriver` interface (subset of `tebeka/selenium.WebDriver`) covering the
methods the package calls (`Get`, `PageSource`, `FindElement`, `Title`, `Quit`,
`Capabilities`, `ExecuteScript`, `SessionID`).  Production code gets a real `selenium.WebDriver`; unit tests
get a struct that implements the interface.

### Integration tests (`//go:build integration`)

Run with: `go test -tags integration ./test/...`

| Test | What it verifies |
|---|---|
| `TestBrowserStartup` | `New(cfg).StartBrowser(ctx)` with 60 s context returns nil error |
| `TestNavigate` | `Navigate` returns HTML containing expected content |
| `TestCurrentPageSource` | `Source` returns same HTML as last navigate |
| `TestClick` | `Click` with a known link navigates to the linked page |
| `TestShutdown` | `StopBrowser` closes Firefox; subsequent `Navigate` calls fail |
| `TestProfilePersistence` | Profile dir and `user.js` exist after startup |
| `TestProgressiveLoad` | `Navigate` to a lazy-loading page returns HTML with content that only appears after scrolling |
| `TestNetworkTimeout` | `Navigate` with a slow request returns an error with the request URL after timeout |
| `TestIgnoreRequests` | `Navigate` with `IgnoreRequests` matching a slow request succeeds without waiting for it |
| `TestSkipLoadWait` | `Navigate` with `SkipLoadWait: true` returns immediately without scrolling or waiting |

Integration tests require `geckodriver` and Firefox to be on `$PATH` /
installed.  They launch a real browser.

---

## Documentation (`README.md`)

A `README.md` file should be written covering:

1. **Installation** — `go get`, geckodriver prerequisite.
2. **Quick start** — minimal working example showing `New` → `Start` → `Navigate`.
3. **Context convention** — every blocking method (`StartBrowser`, `Navigate`, `Click`) accepts a `context.Context`. Callers should *always* pass a context with a deadline:
   ```go
   ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
   defer cancel()
   if err := b.StartBrowser(ctx); err != nil {
       log.Fatal(err)
   }
   ```
   Passing `context.Background()` without a deadline will block indefinitely if Firefox hangs. This is intentional for callers that manage timeouts externally, but should be a conscious choice.
4. **Progressive load waiting** — what `ensureLoaded` does, when to use `IgnoreRequests`, when to use `SkipLoadWait`.
5. **Profile management** — where profiles are stored (`ConfigPath/Profile`), how to customise the base path.

---

## Dependencies to add to go.mod

```
github.com/tebeka/selenium       v0.9.9 (or latest)
github.com/gorilla/websocket     v1.5.3 (or latest)
```

---

## Open questions / deferred decisions

- Should `butcherie` look for `geckodriver` on `$PATH` automatically, or
  require the caller to pass a path?  **Proposed default**: look on `$PATH`
  via `exec.LookPath("geckodriver")`, error if not found.
- Profile base dir is hard-coded to `~/.butcherie/` but is
  overridable via `Config.ConfigPath`.
- `PostActionDelay` defaults to 1.5 s, configurable for tests.
