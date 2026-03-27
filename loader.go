package butcherie

import (
	"context"
	"fmt"
	"regexp"
	"time"
)

const defaultLoadTimeout = 30 * time.Second

// ensureLoaded scrolls the page incrementally to trigger lazy-loaded content,
// then waits for all non-ignored in-flight network requests to complete.
//
// The context controls the overall deadline. If ctx has no deadline and
// opts.Timeout is zero, a 30-second timeout is applied automatically.
// If opts.SkipLoadWait is true the function returns immediately.
func (b *Butcherie) ensureLoaded(ctx context.Context, opts LoadOptions) error {
	if opts.SkipLoadWait {
		return nil
	}

	// Apply per-request timeout if provided, or fall back to the default
	// when ctx carries no deadline of its own.
	if opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opts.Timeout)
		defer cancel()
	} else if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, defaultLoadTimeout)
		defer cancel()
	}

	// Compile ignore patterns.
	ignorePatterns := make([]*regexp.Regexp, 0, len(opts.IgnoreRequests))
	for _, pattern := range opts.IgnoreRequests {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return fmt.Errorf("invalid ignore_requests pattern %q: %w", pattern, err)
		}
		ignorePatterns = append(ignorePatterns, re)
	}

	// Subscribe to network events before scrolling so we catch any requests
	// triggered by lazy-load observers.
	waitIdle := b.cdp.waitForNetworkIdle(ignorePatterns)

	if err := b.scrollToBottom(ctx); err != nil {
		return fmt.Errorf("scroll: %w", err)
	}

	return waitIdle(ctx.Done())
}

// scrollToBottom scrolls the page one viewport-height at a time until the
// bottom is reached, pausing 100 ms between steps to allow IntersectionObserver
// callbacks to fire.
func (b *Butcherie) scrollToBottom(ctx context.Context) error {
	script := `
		window.scrollBy(0, window.innerHeight);
		return [
			window.scrollY + window.innerHeight,
			document.body.scrollHeight
		];
	`
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		result, err := b.wd.ExecuteScript(script, nil)
		if err != nil {
			return err
		}

		arr, ok := result.([]interface{})
		if !ok || len(arr) < 2 {
			return fmt.Errorf("unexpected scroll result: %v", result)
		}
		scrollBottom, _ := arr[0].(float64)
		pageHeight, _ := arr[1].(float64)

		if scrollBottom >= pageHeight {
			break
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return nil
}
