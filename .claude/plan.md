# butcherie: Implementation Plan

A local HTTP API server that drives a real Firefox browser via Selenium/geckodriver.
Provides both an importable Go package and a CLI binary.

---

## Technology choices

| Concern | Choice | Rationale |
|---|---|---|
| WebDriver | `github.com/tebeka/selenium` | geckodriver + W3C WebDriver stack; well-maintained Go bindings |
| HTTP server | stdlib `net/http` | 5 endpoints; no framework needed |
| Integration test gating | `//go:build integration` build tag | Standard Go idiom; no runtime env setup needed |
| Network request tracking | Firefox DevTools Protocol (via geckodriver) | Sees all requests without injection timing risks; no extra dependencies |

---

## Directory structure

```
butcherie/
├── go.mod                          module github.com/ragaskar/butcherie
├── go.sum
│
├── *.go                            package butcherie  (importable library)
│   ├── browser.go                  public API: Config, Server, Status types + New/Start/Shutdown
│   ├── driver.go                   WebDriver setup: profile dir, user.js, build_driver
│   ├── loader.go                   ensureLoaded: incremental scroll + DevTools network drain
│   ├── server.go                   net/http handlers for all 5 endpoints
│   ├── watcher.go                  goroutine that detects Firefox closure
│   ├── browser_test.go             unit tests (no build tag)
│   ├── driver_test.go              unit tests (no build tag)
│   ├── loader_test.go              unit tests (no build tag)
│   └── server_test.go              unit tests (no build tag)
│
├── test/
│   └── browser_integration_test.go //go:build integration — actually launches Firefox
│
└── cmd/
    └── butcherie/
        └── main.go                 CLI: parse --profile / --port, call butcherie.New().Start()
```

**Module/package name**: the library is `package butcherie` at the module root,
so callers import it as `"github.com/ragaskar/butcherie"`.

---

## Public API (package butcherie)

```go
// Config holds all options for creating a Server.
type Config struct {
    Profile        string        // required: profile name (e.g. "vine-reviews")
    Port           int           // TCP port; 0 → OS chooses a free port
    ConfigPath string        // default: ~/.butcherie
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
type Server struct { /* unexported fields */ }

// New creates a Server. Does not start Firefox or the HTTP listener yet.
func New(cfg Config) *Server

// Start launches the HTTP listener and Firefox, blocking until Firefox is ready
// (or the context is cancelled/times out). The context is the primary mechanism
// for controlling startup timeout — almost all callers should pass a context
// with a deadline:
//
//   ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
//   defer cancel()
//   if err := s.Start(ctx); err != nil { ... }
//
// Passing context.Background() with no deadline relies on the OS to surface
// errors (e.g. geckodriver not found) but will block indefinitely if Firefox
// hangs during startup.
func (s *Server) Start(ctx context.Context) error

// Status returns the current startup state without blocking.
func (s *Server) Status() StatusResponse

// URI returns the base URL the HTTP server is listening on (e.g. "http://localhost:31331").
func (s *Server) URI() string

// Shutdown closes Firefox (with SIGKILL fallback) and stops the HTTP server.
func (s *Server) Shutdown() error
```

---

## HTTP API

| Method | Path | Request body | HTTP status | Response |
|---|---|---|---|---|
| `GET` | `/status` | — | 200 | `{"status":"starting\|ready\|failed","errors":[...]}` |
| `POST` | `/navigate` | `{"url":"...","ignore_requests":[...],"skip_load_wait":false}` | 200 / 400 / 504 | `{"html":"...","status_code":200,"headers":{...},"errors":[...]}` |
| `POST` | `/current_page/click` | `{"by":"link_text","value":"...","ignore_requests":[...],"skip_load_wait":false}` | 200 / 400 / 504 | `{"html":"...","status_code":200,"headers":{...},"errors":[...]}` |
| `GET` | `/current_page/source` | — | 200 | `{"html":"...","status_code":200,"headers":{...}}` |
| `POST` | `/shutdown` | — | 200 | `{"status":"shutting down"}` |

HTTP status codes: 400 = malformed request; 504 = `ensureLoaded` network timeout (body still contains error details).

`ignore_requests`: array of regex strings matched against request URIs — matching
in-flight requests are not waited on and do not cause a timeout error.

`skip_ensure_all_assets_loaded`: if `true`, skip progressive load waiting entirely (no scroll, no
network wait).

Click `by` strategies (identical set): `link_text`, `partial_link_text`, `id`,
`css`, `xpath`, `name`, `class_name`, `tag_name`.

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

`ensureLoaded(ctx context.Context, ignoreRequests []string)` is called after every navigate and click (unless skipped). The context carries the deadline — callers should pass a context with a timeout (default 30 s if the incoming HTTP request carries no deadline). Steps:

1. **Incremental scroll**: repeatedly execute `window.scrollBy(0, window.innerHeight)` via `wd.ExecuteScript`, pausing briefly between steps, until `window.scrollY + window.innerHeight >= document.body.scrollHeight`.
2. **Network drain**: via Firefox DevTools Protocol, subscribe to network events (`Network.requestWillBeSent`, `Network.loadingFinished`, `Network.loadingFailed`) to track in-flight requests. Wait until all pending requests complete or `ctx` is done.
   - Requests whose URI matches any regex in `ignoreRequests` are excluded from tracking.
3. **Timeout error**: if `ctx` expires while non-ignored requests are still pending, return an error containing the request details (target URL, method, POST payload if any). The HTTP handler maps `context.DeadlineExceeded` → 504.

The `Timeout` field in `LoadOptions` is still accepted for HTTP callers that want an explicit per-request deadline; the handler wraps the request context with `context.WithTimeout` using that value before calling `ensureLoaded`.

Firefox DevTools Protocol is accessed via geckodriver's WebSocket endpoint: `ws://localhost:<geckoport>/session/<sessionId>/moz/cdp/`.

### Shutdown sequence (`browser.go`)

1. Call `wd.Quit()` (closes Firefox via geckodriver).
2. Poll `os.FindProcess(firefoxPID)` + `process.Signal(syscall.Signal(0))` for
   up to 30 seconds.
3. If still alive after 30 s, `process.Signal(syscall.SIGKILL)`.
4. Stop the HTTP server via `http.Server.Shutdown(ctx)`.

### HTTP handlers (`server.go`)

- Use a `sync.RWMutex`-protected `driver` field so handlers are goroutine-safe.
- JSON encoding/decoding with `encoding/json`.
- `POST /shutdown` starts a goroutine (0.5 s delay) then shuts down; responds
  immediately with 200.
- `/navigate` and `/click` call `ensureLoaded` after the navigation/click action
  (unless `skip_load_wait` is true), then return `{"html":"...","errors":[...]}`.

---

## CLI (`cmd/butcherie/main.go`)

```
Usage: butcherie --profile <name> [--port 31331]

Flags:
  --profile  string   Profile name (required)
  --port     int      Port to listen on (default 31331)
```

Responsibilities:
1. Parse flags with `flag` package.
2. Create a 60-second context and call `butcherie.New(cfg).Start(ctx)`. Print a "waiting for Firefox..." message before blocking.
3. Block until the process is killed or the server exits.

---

## Testing strategy

### Unit tests (no build tag — always run)

| File | What is tested |
|---|---|
| `driver_test.go` | `writeProfilePrefs` writes correct `user.js` content; `profileDir` expansion |
| `server_test.go` | Each HTTP handler in isolation, with a mock/stub `WebDriver` interface |
| `browser_test.go` | `Config` defaults, `Start` context cancellation and timeout |
| `loader_test.go` | `ensureLoaded`: scroll loop terminates, `ignore_requests` regex filtering, timeout error contains request details, `skip_load_wait` bypasses all behaviour |

**WebDriver interface**: to make handlers testable without geckodriver, extract a
`WebDriver` interface (subset of `tebeka/selenium.WebDriver`) covering the
methods the package calls (`Get`, `PageSource`, `FindElement`, `Title`, `Quit`,
`Capabilities`).  Production code gets a real `selenium.WebDriver`; unit tests
get a struct that implements the interface.

### Integration tests (`//go:build integration`)

Run with: `go test -tags integration ./test/...`

| Test | What it verifies |
|---|---|
| `TestServerStartup` | `New(cfg).Start(ctx)` with 60 s context → status is `ready` |
| `TestNavigate` | `POST /navigate` returns HTML containing expected content |
| `TestCurrentPageSource` | `GET /current_page/source` returns same HTML as last navigate |
| `TestClick` | `POST /current_page/click` with a known link navigates to the linked page |
| `TestShutdown` | `POST /shutdown` returns 200; subsequent requests fail |
| `TestProfilePersistence` | Profile dir and `user.js` exist after startup |
| `TestProgressiveLoad` | `POST /navigate` to a lazy-loading page returns HTML with content that only appears after scrolling |
| `TestNetworkTimeout` | `POST /navigate` with a slow request returns an error with the request URL after timeout |
| `TestIgnoreRequests` | `POST /navigate` with `ignore_requests` matching a slow request succeeds without waiting for it |
| `TestSkipLoadWait` | `POST /navigate` with `skip_load_wait: true` returns immediately without scrolling or waiting |

Integration tests require `geckodriver` and Firefox to be on `$PATH` /
installed.  They launch a real browser.

---

## Documentation (`README.md`)

A `README.md` file should be written covering:

1. **Installation** — `go get`, geckodriver prerequisite.
2. **Quick start** — minimal working example showing `New` → `Start` → HTTP calls → `Shutdown`.
3. **Context convention** — every blocking method (`Start`, and indirectly navigate/click via the HTTP layer) accepts a `context.Context`. Callers should *always* pass a context with a deadline:
   ```go
   ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
   defer cancel()
   if err := s.Start(ctx); err != nil {
       log.Fatal(err)
   }
   ```
   Passing `context.Background()` without a deadline will block indefinitely if Firefox hangs. This is intentional for callers that manage timeouts externally, but should be a conscious choice.
4. **Progressive load waiting** — what `ensureLoaded` does, when to use `ignore_requests`, when to use `skip_load_wait`.
5. **Profile management** — where profiles are stored (`ConfigPath/Profile`), how to customise the base path.
6. **CLI usage** — flags, startup output, signal handling.

---

## Dependencies to add to go.mod

```
github.com/tebeka/selenium       v0.9.9 (or latest)
```

`tebeka/selenium` also uses `github.com/blang/semver` and
`github.com/google/go-cmp` internally, but those come in transitively.

---

## Open questions / deferred decisions

- Should `butcherie` look for `geckodriver` on `$PATH` automatically, or
  require the caller to pass a path?  **Proposed default**: look on `$PATH`
  via `exec.LookPath("geckodriver")`, error if not found.
- Profile base dir is hard-coded to `~/.butcherie/` but is
  overridable via `Config.ConfigPath`.
- `PostActionDelay` defaults to 1.5 s, configurable for tests.
