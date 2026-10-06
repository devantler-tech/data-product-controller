//go:build browser

package browser_test

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
	"github.com/go-rod/rod/lib/launcher/flags"
)

const browserExitTimeout = 10 * time.Second

// launchBrowser starts a headless browser for one test and returns its control URL.
// Every launch gets its own profile directory, which only the launcher removes, so
// tests launch through here rather than inline.
func launchBrowser(t *testing.T, extra ...flags.Flag) string {
	t.Helper()
	controlURL, _ := launchBrowserProfile(t, extra...)
	return controlURL
}

// launchBrowserProfile also returns the profile directory the launch created.
func launchBrowserProfile(t *testing.T, extra ...flags.Flag) (string, string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()
	browserLauncher := launcher.New().Context(ctx).Headless(true).NoSandbox(true)
	for _, flag := range extra {
		browserLauncher = browserLauncher.Set(flag)
	}
	profile := browserLauncher.Get(flags.UserDataDir)
	controlURL, err := browserLauncher.Launch()
	if err != nil {
		removeBrowserProfile(t, browserLauncher, profile)
		t.Fatalf("launch browser: %v", err)
	}
	t.Cleanup(func() { removeBrowserProfile(t, browserLauncher, profile) })
	return controlURL, profile
}

// removeBrowserProfile deletes the profile once the browser has exited and fails
// the test if it cannot. It is registered before the test connects, so it runs
// after the test has closed its browser; one that will not exit is killed, and
// neither wait is unbounded.
func removeBrowserProfile(t *testing.T, browserLauncher *launcher.Launcher, profile string) {
	t.Helper()
	if browserLauncher.PID() != 0 {
		exited := make(chan struct{})
		go func() {
			browserLauncher.Cleanup()
			close(exited)
		}()
		select {
		case <-exited:
		case <-time.After(browserExitTimeout):
			browserLauncher.Kill()
			select {
			case <-exited:
			case <-time.After(browserExitTimeout):
				t.Errorf("browser %d did not exit", browserLauncher.PID())
			}
		}
	}
	if err := os.RemoveAll(profile); err != nil {
		t.Errorf("remove browser profile %q: %v", profile, err)
	}
}

// fixtureValue reports malformed fixture setup through the owning test.
func fixtureValue[T any](t *testing.T, value any) T {
	t.Helper()
	typed, ok := value.(T)
	if !ok {
		t.Fatalf("fixture value has unexpected type %T", value)
	}
	return typed
}
