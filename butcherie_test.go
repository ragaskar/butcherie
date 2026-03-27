package butcherie

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/tebeka/selenium"
)

// mockWD implements WebDriver for butcherie unit tests.
type mockWD struct {
	WebDriver
	pageSource string
	pageErr    error
	getErr     error
	findElem   selenium.WebElement
	findErr    error
	titleErr   error
}

func (m *mockWD) Get(_ string) error                    { return m.getErr }
func (m *mockWD) PageSource() (string, error)           { return m.pageSource, m.pageErr }
func (m *mockWD) Title() (string, error)                { return "", m.titleErr }
func (m *mockWD) SessionID() string                     { return "test-session" }
func (m *mockWD) Capabilities() (selenium.Capabilities, error) {
	return selenium.Capabilities{"moz:processID": float64(1234)}, nil
}
func (m *mockWD) ExecuteScript(_ string, _ []interface{}) (interface{}, error) {
	return []interface{}{float64(800), float64(600)}, nil
}
func (m *mockWD) FindElement(_, _ string) (selenium.WebElement, error) {
	return m.findElem, m.findErr
}

func newTestButcherie(wd *mockWD) *Butcherie {
	b := &Butcherie{
		cfg: Config{PostActionDelay: 0},
		wd:  wd,
		cdp: &cdpClient{
			done: make(chan struct{}),
		},
	}
	close(b.cdp.done)
	return b
}

func TestNew_Defaults(t *testing.T) {
	b := New(Config{Profile: "test"})
	if b.cfg.PostActionDelay != 1500*time.Millisecond {
		t.Errorf("PostActionDelay = %v, want 1.5s", b.cfg.PostActionDelay)
	}
	if b.cfg.ConfigPath == "" {
		t.Error("ConfigPath should be set to default")
	}
}

func TestStartBrowser_CancelledContext(t *testing.T) {
	b := New(Config{Profile: "test", Port: 0})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	err := b.StartBrowser(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestStartBrowser_TimedOutContext(t *testing.T) {
	b := New(Config{Profile: "test", Port: 0})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // ensure timeout fires

	err := b.StartBrowser(ctx)
	if err == nil {
		t.Fatal("expected error from timed-out context, got nil")
	}
}

func TestNavigate_Success(t *testing.T) {
	b := newTestButcherie(&mockWD{pageSource: "<html>ok</html>"})
	resp, err := b.Navigate(context.Background(), "http://example.com", LoadOptions{SkipLoadWait: true})
	if err != nil {
		t.Fatalf("Navigate: %v", err)
	}
	if resp.HTML != "<html>ok</html>" {
		t.Errorf("HTML = %q", resp.HTML)
	}
}

func TestNavigate_GetError(t *testing.T) {
	b := newTestButcherie(&mockWD{getErr: fmt.Errorf("connection refused")})
	_, err := b.Navigate(context.Background(), "http://example.com", LoadOptions{SkipLoadWait: true})
	if err == nil {
		t.Fatal("expected error, got nil")
	}
}

func TestSource_Success(t *testing.T) {
	b := newTestButcherie(&mockWD{pageSource: "<html>source</html>"})
	resp, err := b.Source()
	if err != nil {
		t.Fatalf("Source: %v", err)
	}
	if resp.HTML != "<html>source</html>" {
		t.Errorf("HTML = %q", resp.HTML)
	}
}

func TestClick_UnknownStrategy(t *testing.T) {
	b := newTestButcherie(&mockWD{})
	_, err := b.Click(context.Background(), "unknown", "value", LoadOptions{SkipLoadWait: true})
	if err == nil {
		t.Fatal("expected error for unknown strategy")
	}
}

func TestSeleniumBy(t *testing.T) {
	cases := map[string]string{
		"link_text":         selenium.ByLinkText,
		"partial_link_text": selenium.ByPartialLinkText,
		"id":                selenium.ByID,
		"css":               selenium.ByCSSSelector,
		"xpath":             selenium.ByXPATH,
		"name":              selenium.ByName,
		"class_name":        selenium.ByClassName,
		"tag_name":          selenium.ByTagName,
		"unknown":           "",
	}
	for input, want := range cases {
		if got := seleniumBy(input); got != want {
			t.Errorf("seleniumBy(%q) = %q, want %q", input, got, want)
		}
	}
}
