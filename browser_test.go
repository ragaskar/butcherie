package butcherie

import (
	"context"
	"testing"
	"time"
)

func TestNew_Defaults(t *testing.T) {
	srv := New(Config{Profile: "test"})
	if srv.cfg.PostActionDelay != 1500*time.Millisecond {
		t.Errorf("PostActionDelay = %v, want 1.5s", srv.cfg.PostActionDelay)
	}
	if srv.cfg.ConfigPath == "" {
		t.Error("ConfigPath should be set to default")
	}
	if srv.status.Status != StatusStarting {
		t.Errorf("initial status = %q, want %q", srv.status.Status, StatusStarting)
	}
}

func TestStart_CancelledContext(t *testing.T) {
	srv := New(Config{Profile: "test", Port: 0})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // already cancelled

	err := srv.Start(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context, got nil")
	}
}

func TestStart_TimedOutContext(t *testing.T) {
	// Use a port that is valid but geckodriver won't be found, so Start will
	// fail. We just verify the context deadline is respected.
	srv := New(Config{Profile: "test", Port: 0})
	ctx, cancel := context.WithTimeout(context.Background(), 1*time.Millisecond)
	defer cancel()
	time.Sleep(5 * time.Millisecond) // ensure timeout fires

	err := srv.Start(ctx)
	if err == nil {
		t.Fatal("expected error from timed-out context, got nil")
	}
}

func TestStatus_Initial(t *testing.T) {
	srv := New(Config{Profile: "test"})
	st := srv.Status()
	if st.Status != StatusStarting {
		t.Errorf("Status() = %q, want %q", st.Status, StatusStarting)
	}
}

func TestURI_BeforeStart(t *testing.T) {
	srv := New(Config{Profile: "test"})
	if uri := srv.URI(); uri != "" {
		t.Errorf("URI() before Start = %q, want empty", uri)
	}
}
