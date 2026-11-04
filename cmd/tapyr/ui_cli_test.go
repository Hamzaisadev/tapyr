package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	internalui "github.com/kentrow/tapyr/internal/ui"
)

// TestUI checks that tapyr run serves the UI under /ui/ with its
// Content-Security-Policy and redirects / to it, and that
// settings.server.ui.enabled: false removes it.
func TestUI(t *testing.T) {
	for _, enabled := range []bool{true, false} {
		dir := t.TempDir()
		config := "apiVersion: tapyr/v1alpha1\nkind: Settings\nmetadata: { name: ui }\nspec:\n  server: { listen: \"127.0.0.1:0\", ui: { enabled: " +
			map[bool]string{true: "true", false: "false"}[enabled] + " } }\n  state: { path: " + filepath.Join(dir, "tapyr.db") + " }\n"
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(config), 0o600); err != nil {
			t.Fatal(err)
		}
		addr, stop := background(t, nil, "run", "--config", dir)
		client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
		req, _ := http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/", http.NoBody)
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		req, _ = http.NewRequestWithContext(t.Context(), http.MethodGet, "http://"+addr+"/ui/", http.NoBody)
		ui, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		ui.Body.Close()
		if enabled {
			if resp.StatusCode != http.StatusFound || resp.Header.Get("Location") != "/ui/" {
				t.Errorf("/: %d %s", resp.StatusCode, resp.Header.Get("Location"))
			}
			if ui.StatusCode != http.StatusOK || ui.Header.Get("Content-Security-Policy") != internalui.CSP ||
				!strings.HasPrefix(ui.Header.Get("Content-Type"), "text/html") {
				t.Errorf("/ui/: %d %v", ui.StatusCode, ui.Header)
			}
		} else if resp.StatusCode != http.StatusNotFound || ui.StatusCode != http.StatusNotFound {
			t.Errorf("disabled: / %d, /ui/ %d", resp.StatusCode, ui.StatusCode)
		}
		stop()
	}
}
