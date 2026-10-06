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
		_ = os.RemoveAll(profile)
		t.Fatalf("launch browser: %v", err)
	}
	t.Cleanup(func() { removeBrowserProfile(browserLauncher) })
	return controlURL, profile
}

// removeBrowserProfile deletes the profile once the browser has exited. It is
// registered before the test connects, so it runs after the test has closed its
// browser; one that will not exit is killed rather than waited on forever.
func removeBrowserProfile(browserLauncher *launcher.Launcher) {
	removed := make(chan struct{})
	go func() {
		browserLauncher.Cleanup()
		close(removed)
	}()
	select {
	case <-removed:
	case <-time.After(10 * time.Second):
		browserLauncher.Kill()
		<-removed
	}
}
