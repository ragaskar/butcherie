# butcherie

A local HTTP API server that drives a real Firefox browser via Selenium/geckodriver. Provides both an importable Go package and a CLI binary.

## Prerequisites

- [geckodriver](https://github.com/mozilla/geckodriver/releases) on your `$PATH`
- Firefox installed

## Installation

```bash
go get github.com/ragaskar/butcherie
```

Install the CLI:

```bash
go install github.com/ragaskar/butcherie/cmd/butcherie@latest
```

## Quick start

```go
import (
    "context"
    "log"
    "time"

    "github.com/ragaskar/butcherie"
)

func main() {
    srv := butcherie.New(butcherie.Config{
        Profile: "my-profile",
        Port:    31331,
    })

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    if err := srv.Start(ctx); err != nil {
        log.Fatal(err)
    }
    defer srv.Shutdown()

    // Navigate and get page HTML.
    resp, err := http.Post(srv.URI()+"/navigate", "application/json",
        strings.NewReader(`{"url":"https://example.com"}`))
    // ...
}
```

## Context convention

Every blocking method accepts a `context.Context`. **Always pass a context with a deadline** — this is the primary mechanism for controlling timeouts:

```go
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()
if err := s.Start(ctx); err != nil {
    log.Fatal(err)
}
```

Passing `context.Background()` without a deadline will block indefinitely if Firefox hangs during startup. This is intentional for callers that manage timeouts externally, but should be a conscious choice.

## CLI usage

```
butcherie --profile <name> [--port 31331]

Flags:
  --profile  string   Profile name (required)
  --port     int      Port to listen on (default 31331)
```

Example:

```bash
butcherie --profile vine-reviews --port 31331
```

The CLI uses a 60-second startup timeout. Send `SIGINT` or `SIGTERM` to shut down.

## HTTP API

| Method | Path | Request body | HTTP status | Response |
|---|---|---|---|---|
| `GET` | `/status` | — | 200 | `{"status":"starting\|ready\|failed","errors":[...]}` |
| `POST` | `/navigate` | `{"url":"...","ignore_requests":[...],"skip_load_wait":false}` | 200 / 400 / 504 | `{"html":"...","status_code":200,"headers":{...},"errors":[...]}` |
| `POST` | `/current_page/click` | `{"by":"...","value":"...","ignore_requests":[...],"skip_load_wait":false}` | 200 / 400 / 504 | `{"html":"...","status_code":200,"headers":{...},"errors":[...]}` |
| `GET` | `/current_page/source` | — | 200 | `{"html":"...","status_code":200,"headers":{...}}` |
| `POST` | `/shutdown` | — | 200 | `{"status":"shutting down"}` |

HTTP 504 is returned when the progressive load timeout expires. The response body still contains error details.

### Click strategies (`by` field)

`link_text`, `partial_link_text`, `id`, `css`, `xpath`, `name`, `class_name`, `tag_name`

## Progressive load waiting

After every navigate or click, butcherie:

1. **Scrolls incrementally** — one viewport-height at a time, pausing 100 ms between steps. This triggers `IntersectionObserver`-based lazy loading that a single jump-to-bottom would miss.
2. **Waits for network idle** — using the Firefox DevTools Protocol, it tracks all in-flight XHR/fetch requests and waits until they all complete (or a 30-second timeout fires).

### `ignore_requests`

An array of regex strings matched against request URIs. Matching requests are excluded from the network idle check:

```json
{
  "url": "https://example.com",
  "ignore_requests": ["analytics\\.example\\.com", "\\.beacon$"]
}
```

### `skip_load_wait`

Set to `true` to disable scrolling and network waiting entirely:

```json
{
  "url": "https://example.com",
  "skip_load_wait": true
}
```

### `timeout`

Per-request timeout in nanoseconds (Go `time.Duration` JSON encoding). Defaults to 30 seconds if omitted.

## Profile management

Profiles are stored at `$ConfigPath/$Profile` (default `~/.butcherie/<profile-name>`). The directory is created on first `Start()` and persists across runs, preserving cookies, storage, and other browser state.

Override the base path via `Config.ConfigPath`:

```go
butcherie.New(butcherie.Config{
    Profile:    "my-profile",
    ConfigPath: "/data/browser-profiles",
})
```

## Running integration tests

Integration tests launch a real Firefox browser and require geckodriver on `$PATH`:

```bash
go test -tags integration ./test/...
```
