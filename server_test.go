package butcherie

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/tebeka/selenium"
)

// mockWD implements WebDriver for handler unit tests.
type mockWD struct {
	WebDriver
	pageSource  string
	pageErr     error
	getErr      error
	clickErr    error
	findElem    selenium.WebElement
	findErr     error
	titleResult string
	titleErr    error
}

func (m *mockWD) Get(_ string) error          { return m.getErr }
func (m *mockWD) PageSource() (string, error)  { return m.pageSource, m.pageErr }
func (m *mockWD) Title() (string, error)       { return m.titleResult, m.titleErr }
func (m *mockWD) SessionID() string            { return "test-session" }
func (m *mockWD) Capabilities() (selenium.Capabilities, error) {
	return selenium.Capabilities{"moz:processID": float64(1234)}, nil
}
func (m *mockWD) ExecuteScript(_ string, _ []interface{}) (interface{}, error) {
	return []interface{}{float64(800), float64(600)}, nil
}
func (m *mockWD) FindElement(_, _ string) (selenium.WebElement, error) {
	return m.findElem, m.findErr
}

// mockCDP is a no-op CDP client for handler tests.
type mockCDP struct{}

func (m *mockCDP) waitForNetworkIdle(_ interface{}) func(<-chan struct{}) error {
	return func(_ <-chan struct{}) error { return nil }
}
func (m *mockCDP) captureDocumentResponse() func(<-chan struct{}) (int, map[string][]string) {
	return func(_ <-chan struct{}) (int, map[string][]string) { return 200, nil }
}

func newTestServer(wd *mockWD) *Server {
	srv := &Server{
		cfg: Config{PostActionDelay: 0},
		wd:  wd,
		cdp: &cdpClient{
			done: make(chan struct{}),
		},
		status: StatusResponse{Status: StatusReady},
	}
	close(srv.cdp.done)
	return srv
}

func TestHandleStatus(t *testing.T) {
	srv := newTestServer(&mockWD{})
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	w := httptest.NewRecorder()
	srv.handleStatus(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}
	var resp StatusResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Status != StatusReady {
		t.Errorf("status = %q, want %q", resp.Status, StatusReady)
	}
}

func TestHandleNavigate_MissingURL(t *testing.T) {
	srv := newTestServer(&mockWD{})
	req := httptest.NewRequest(http.MethodPost, "/navigate", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleNavigate(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want 400", w.Code)
	}
}

func TestHandleNavigate_Success(t *testing.T) {
	srv := newTestServer(&mockWD{pageSource: "<html>ok</html>"})
	body := `{"url":"http://example.com","skip_load_wait":true}`
	req := httptest.NewRequest(http.MethodPost, "/navigate", strings.NewReader(body))
	w := httptest.NewRecorder()
	srv.handleNavigate(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}
	var resp PageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.HTML != "<html>ok</html>" {
		t.Errorf("html = %q", resp.HTML)
	}
}

func TestHandleSource(t *testing.T) {
	srv := newTestServer(&mockWD{pageSource: "<html>source</html>"})
	req := httptest.NewRequest(http.MethodGet, "/current_page/source", nil)
	w := httptest.NewRecorder()
	srv.handleSource(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}
	var resp PageResponse
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.HTML != "<html>source</html>" {
		t.Errorf("html = %q", resp.HTML)
	}
}

func TestHandleClick_MissingFields(t *testing.T) {
	srv := newTestServer(&mockWD{})
	req := httptest.NewRequest(http.MethodPost, "/current_page/click", strings.NewReader(`{}`))
	w := httptest.NewRecorder()
	srv.handleClick(w, req)

	if w.Code != http.StatusBadRequest {
		t.Errorf("status code = %d, want 400", w.Code)
	}
}

func TestHandleShutdown(t *testing.T) {
	srv := newTestServer(&mockWD{})
	req := httptest.NewRequest(http.MethodPost, "/shutdown", nil)
	w := httptest.NewRecorder()
	srv.handleShutdown(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status code = %d, want 200", w.Code)
	}
	var resp map[string]string
	if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp["status"] != "shutting down" {
		t.Errorf("status = %q", resp["status"])
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
