//go:build browser

package browser_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/go-rod/rod"
)

func TestLaunchedBrowserProfileIsRemovedWithItsTest(t *testing.T) {
	var profile string
	t.Run("launch", func(t *testing.T) {
		var controlURL string
		controlURL, profile = launchBrowserProfile(t)
		browser := rod.New().ControlURL(controlURL).MustConnect()
		t.Cleanup(func() { _ = browser.Close() })
		browser.MustPage("about:blank").MustWaitLoad()
		if _, err := os.Stat(profile); err != nil {
			t.Fatalf("browser profile while the test runs: %v", err)
		}
	})
	if _, err := os.Stat(profile); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("browser profile %q outlived its test: stat error = %v", profile, err)
	}
}
