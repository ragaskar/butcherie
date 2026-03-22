package butcherie

import (
	"context"
	"testing"
	"time"
)

// stubWD is a minimal WebDriver stub for loader tests.
type stubWD struct {
	WebDriver
	scrollResults [][]float64 // each call returns the next pair
	scriptErr     error
	call          int
}

func (s *stubWD) ExecuteScript(_ string, _ []interface{}) (interface{}, error) {
	if s.scriptErr != nil {
		return nil, s.scriptErr
	}
	if s.call >= len(s.scrollResults) {
		// Already at bottom.
		pair := s.scrollResults[len(s.scrollResults)-1]
		return []interface{}{pair[0], pair[1]}, nil
	}
	pair := s.scrollResults[s.call]
	s.call++
	return []interface{}{pair[0], pair[1]}, nil
}

func newScrollServer(results [][]float64) *Server {
	srv := &Server{
		cfg: Config{PostActionDelay: 0},
		wd:  &stubWD{scrollResults: results},
		cdp: &cdpClient{
			done: make(chan struct{}),
			subs: nil,
		},
	}
	close(srv.cdp.done) // mark as "closed" so close() won't block in tests
	return srv
}

func TestEnsureLoaded_SkipLoadWait(t *testing.T) {
	srv := newScrollServer(nil)
	ctx := context.Background()
	if err := srv.ensureLoaded(ctx, LoadOptions{SkipLoadWait: true}); err != nil {
		t.Errorf("expected nil, got %v", err)
	}
}

func TestScrollToBottom_SinglePage(t *testing.T) {
	// Page fits in one viewport — scrollY + innerHeight >= scrollHeight immediately.
	srv := &Server{
		cfg: Config{},
		wd:  &stubWD{scrollResults: [][]float64{{800, 600}}}, // scrollBottom > pageHeight
	}
	ctx := context.Background()
	if err := srv.scrollToBottom(ctx); err != nil {
		t.Errorf("scrollToBottom: %v", err)
	}
}

func TestScrollToBottom_MultiPage(t *testing.T) {
	// Three steps needed: 400<1200, 800<1200, 1200>=1200.
	srv := &Server{
		cfg: Config{},
		wd: &stubWD{scrollResults: [][]float64{
			{400, 1200},
			{800, 1200},
			{1200, 1200},
		}},
	}
	ctx := context.Background()
	if err := srv.scrollToBottom(ctx); err != nil {
		t.Errorf("scrollToBottom: %v", err)
	}
	if srv.wd.(*stubWD).call != 3 {
		t.Errorf("expected 3 scroll calls, got %d", srv.wd.(*stubWD).call)
	}
}

func TestScrollToBottom_ContextCancelled(t *testing.T) {
	srv := &Server{
		cfg: Config{},
		wd:  &stubWD{scrollResults: [][]float64{{0, 9999}}}, // never reaches bottom
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if err := srv.scrollToBottom(ctx); err == nil {
		t.Error("expected context error, got nil")
	}
}

func TestEnsureLoaded_InvalidRegex(t *testing.T) {
	srv := newScrollServer([][]float64{{800, 600}})
	ctx := context.Background()
	err := srv.ensureLoaded(ctx, LoadOptions{IgnoreRequests: []string{"[invalid"}})
	if err == nil {
		t.Error("expected error for invalid regex, got nil")
	}
}
