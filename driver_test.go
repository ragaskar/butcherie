package butcherie

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteProfilePrefs(t *testing.T) {
	dir := t.TempDir()
	if err := writeProfilePrefs(dir); err != nil {
		t.Fatalf("writeProfilePrefs: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(dir, "user.js"))
	if err != nil {
		t.Fatalf("read user.js: %v", err)
	}
	content := string(data)

	for _, want := range []string{
		`browser.startup.homepage`,
		`startup.homepage_welcome_url`,
		`browser.startup.page`,
		`browser.shell.checkDefaultBrowser`,
	} {
		if !strings.Contains(content, want) {
			t.Errorf("user.js missing pref %q", want)
		}
	}
}

func TestPrepareProfile_ExpandsTilde(t *testing.T) {
	home, _ := os.UserHomeDir()
	cfg := Config{
		Profile:    "test-profile",
		ConfigPath: "~/.butcherie-test-tmp",
	}
	dir, err := prepareProfile(cfg)
	if err != nil {
		t.Fatalf("prepareProfile: %v", err)
	}
	defer os.RemoveAll(filepath.Join(home, ".butcherie-test-tmp"))

	want := filepath.Join(home, ".butcherie-test-tmp", "test-profile")
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
	if _, err := os.Stat(dir); err != nil {
		t.Errorf("profile dir not created: %v", err)
	}
}

func TestPrepareProfile_CreatesDir(t *testing.T) {
	base := t.TempDir()
	cfg := Config{
		Profile:    "my-profile",
		ConfigPath: base,
	}
	dir, err := prepareProfile(cfg)
	if err != nil {
		t.Fatalf("prepareProfile: %v", err)
	}

	want := filepath.Join(base, "my-profile")
	if dir != want {
		t.Errorf("got %q, want %q", dir, want)
	}
	if _, err := os.Stat(filepath.Join(dir, "user.js")); err != nil {
		t.Errorf("user.js not created: %v", err)
	}
}
