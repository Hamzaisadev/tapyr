package sim

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/auth/plugins"
	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/provider"
)

// TestPluginAuth serves the aruba-central-classic package: the plugin
// answers its refresh endpoint, rotates the refresh tokens, and its token
// requests count in the provider limits.
func TestPluginAuth(t *testing.T) {
	pkgs, err := provider.Builtin(loader(t))
	if err != nil {
		t.Fatal(err)
	}
	clk := clock.NewFake(midnight)
	sc := &config.Scenario{Metadata: config.ObjectMeta{Name: "expiry"}, Spec: config.ScenarioSpec{Steps: []config.ScenarioStep{
		{At: config.Duration(time.Hour), TokenExpiry: true},
	}}}
	s, err := New(provider.Find(pkgs, "aruba-central-classic"), Options{Clock: clk, Scenario: sc, Plugins: plugins.Builtin()})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	refresh := func(client, token string) (int, map[string]any) {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost,
			srv.URL+"/oauth2/token?grant_type=refresh_token&client_secret=sim-client-secret&client_id="+client+"&refresh_token="+token, http.NoBody)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		var body map[string]any
		_ = json.NewDecoder(resp.Body).Decode(&body)
		return resp.StatusCode, body
	}
	if status, _, _ := get(t, srv.URL+"/monitoring/v2/aps", "sim-access-token"); status != http.StatusOK {
		t.Fatalf("bootstrap access token: %d", status)
	}
	if status, _ := refresh("other", "sim-refresh-token"); status != http.StatusUnauthorized {
		t.Errorf("unknown client: %d", status)
	}
	status, body := refresh("sim-client", "sim-refresh-token")
	if status != http.StatusOK || body["refresh_token"] == "" || body["expires_in"] != float64(7200) {
		t.Fatalf("refresh: %d %v", status, body)
	}
	if status, _ := refresh("sim-client", "sim-refresh-token"); status != http.StatusBadRequest {
		t.Errorf("a spent refresh token: %d", status)
	}
	access := body["access_token"].(string)
	if status, headers, _ := get(t, srv.URL+"/monitoring/v2/aps", access); status != http.StatusOK || headers.Get("X-RateLimit-Limit-day") != "5000" {
		t.Fatalf("new access token: %d %v", status, headers)
	}
	// 7 calls per second: the token requests count.
	limited := false
	for range 8 {
		if status, _ := refresh("sim-client", "spent"); status == http.StatusTooManyRequests {
			limited = true
		}
	}
	if !limited {
		t.Error("token requests are not rate limited")
	}
	clk.Advance(time.Hour)
	if status, _, _ := get(t, srv.URL+"/monitoring/v2/aps", access); status != http.StatusUnauthorized {
		t.Errorf("after tokenExpiry: %d", status)
	}
	if st := s.Stats(); st.TokensIssued != 1 {
		t.Errorf("%d tokens issued, want 1", st.TokensIssued)
	}
}
