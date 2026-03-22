package butcherie

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tebeka/selenium"
	"github.com/tebeka/selenium/firefox"
)

// WebDriver is the subset of selenium.WebDriver used by this package.
// Extracted as an interface to allow unit testing without geckodriver.
type WebDriver interface {
	Get(url string) error
	PageSource() (string, error)
	FindElement(by, value string) (selenium.WebElement, error)
	Title() (string, error)
	Quit() error
	Capabilities() (selenium.Capabilities, error)
	ExecuteScript(script string, args []interface{}) (interface{}, error)
	SessionID() string
}

// buildDriver starts geckodriver, launches Firefox with the configured profile,
// and returns a connected WebDriver and the geckodriver port.
func buildDriver(cfg Config) (WebDriver, int, error) {
	geckodriverPath, err := exec.LookPath("geckodriver")
	if err != nil {
		return nil, 0, fmt.Errorf("geckodriver not found on PATH: %w", err)
	}

	profileDir, err := prepareProfile(cfg)
	if err != nil {
		return nil, 0, fmt.Errorf("prepare profile: %w", err)
	}

	geckoPort, err := freePort()
	if err != nil {
		return nil, 0, fmt.Errorf("find free port for geckodriver: %w", err)
	}

	service, err := selenium.NewGeckoDriverService(geckodriverPath, geckoPort)
	if err != nil {
		return nil, 0, fmt.Errorf("start geckodriver: %w", err)
	}

	caps := selenium.Capabilities{"browserName": "firefox"}
	firefoxCaps := firefox.Capabilities{
		Args: []string{"-profile", profileDir},
	}
	caps.AddFirefox(firefoxCaps)

	wd, err := selenium.NewRemote(caps, fmt.Sprintf("http://localhost:%d/wd/hub", geckoPort))
	if err != nil {
		_ = service.Stop()
		return nil, 0, fmt.Errorf("create WebDriver session: %w", err)
	}

	return wd, geckoPort, nil
}

// prepareProfile creates the profile directory and writes user.js.
func prepareProfile(cfg Config) (string, error) {
	configPath := cfg.ConfigPath
	if strings.HasPrefix(configPath, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		configPath = filepath.Join(home, configPath[2:])
	}

	profileDir := filepath.Join(configPath, cfg.Profile)
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		return "", fmt.Errorf("create profile dir %s: %w", profileDir, err)
	}
	if err := writeProfilePrefs(profileDir); err != nil {
		return "", fmt.Errorf("write profile prefs: %w", err)
	}
	return profileDir, nil
}

// writeProfilePrefs writes Firefox startup preferences to user.js.
func writeProfilePrefs(profileDir string) error {
	prefs := `// butcherie-managed preferences
user_pref("browser.startup.homepage", "about:blank");
user_pref("startup.homepage_welcome_url", "");
user_pref("startup.homepage_welcome_url.additional", "");
user_pref("browser.startup.page", 0);
user_pref("browser.shell.checkDefaultBrowser", false);
user_pref("browser.usedOnWindows10", true);
`
	return os.WriteFile(filepath.Join(profileDir, "user.js"), []byte(prefs), 0o644)
}

// extractFirefoxPID returns the PID of the Firefox process from capabilities.
func extractFirefoxPID(wd WebDriver) (int, error) {
	caps, err := wd.Capabilities()
	if err != nil {
		return 0, fmt.Errorf("get capabilities: %w", err)
	}
	pid, ok := caps["moz:processID"]
	if !ok {
		return 0, fmt.Errorf("moz:processID not in capabilities")
	}
	// JSON numbers unmarshal as float64.
	switch v := pid.(type) {
	case float64:
		return int(v), nil
	case int:
		return v, nil
	default:
		return 0, fmt.Errorf("unexpected type for moz:processID: %T", pid)
	}
}

// freePort returns a free TCP port on localhost.
func freePort() (int, error) {
	ln, err := net.Listen("tcp", ":0")
	if err != nil {
		return 0, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	_ = ln.Close()
	return port, nil
}
