# butcherie

A Go library that drives a real Firefox browser via Selenium/geckodriver.

## Prerequisites

- [geckodriver](https://github.com/mozilla/geckodriver/releases) on your `$PATH`
- Firefox installed

## Installation

```bash
go get github.com/ragaskar/butcherie
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
    b := butcherie.New(butcherie.Config{
        Profile: "my-profile",
    })

    ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
    defer cancel()

    if err := b.StartBrowser(ctx); err != nil {
        log.Fatal(err)
    }
    defer b.StopBrowser()

    resp, err := b.Navigate(context.Background(), "https://example.com", butcherie.LoadOptions{})
    if err != nil {
        log.Fatal(err)
    }
    log.Printf("status: %d, html length: %d", resp.StatusCode, len(resp.HTML))
}
```

## Context convention

Every blocking method accepts a `context.Context`. **Always pass a context with a deadline** — this is the primary mechanism for controlling timeouts:

```go
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()
if err := b.StartBrowser(ctx); err != nil {
    log.Fatal(err)
}
```

Passing `context.Background()` without a deadline will block indefinitely if Firefox hangs during startup. This is intentional for callers that manage timeouts externally, but should be a conscious choice.

## Progressive load waiting

After every `Navigate` or `Click`, butcherie:

1. **Scrolls incrementally** — one viewport-height at a time, pausing 100 ms between steps. This triggers `IntersectionObserver`-based lazy loading that a single jump-to-bottom would miss.
2. **Waits for network idle** — using the Firefox DevTools Protocol, it tracks all in-flight XHR/fetch requests and waits until they all complete (or a 30-second timeout fires).

### `IgnoreRequests`

An array of regex strings matched against request URIs. Matching requests are excluded from the network idle check:

```go
resp, err := b.Navigate(ctx, "https://example.com", butcherie.LoadOptions{
    IgnoreRequests: []string{`analytics\.example\.com`, `\.beacon$`},
})
```

### `SkipLoadWait`

Set to `true` to disable scrolling and network waiting entirely:

```go
resp, err := b.Navigate(ctx, "https://example.com", butcherie.LoadOptions{
    SkipLoadWait: true,
})
```

### `Timeout`

Per-request timeout as a `time.Duration`. Defaults to 30 seconds if omitted.

## Profile management

Profiles are stored at `$ConfigPath/$Profile` (default `~/.butcherie/<profile-name>`). The directory is created on first `StartBrowser()` and persists across runs, preserving cookies, storage, and other browser state.

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
