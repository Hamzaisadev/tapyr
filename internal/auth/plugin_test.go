package auth

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/config"
	"github.com/kentrow/tapyr/internal/secret"
)

// echoPlugin applies a header made of a secret, and keeps its state in the
// store.
type echoPlugin struct{ created int }

func (*echoPlugin) Name() string { return "echo" }

func (*echoPlugin) Validate(cfg map[string]any) ([]string, error) {
	name, err := PluginSecretName(cfg, "key")
	if err != nil {
		return nil, err
	}
	return []string{name}, nil
}

func (p *echoPlugin) New(env PluginEnv, cfg map[string]any) (PluginAuthenticator, error) {
	key, err := PluginSecret(cfg, "key", env.Secrets)
	if err != nil {
		return nil, err
	}
	p.created++
	return &echoAuth{env: env, key: key}, nil
}

func (*echoPlugin) Simulator(SimulatorEnv, map[string]any) (PluginSimulator, error) {
	return nil, errors.New("no simulator")
}

type echoAuth struct {
	env PluginEnv
	key secret.Value
}

func (a *echoAuth) Decorate(ctx context.Context, req *http.Request) error {
	if v, ok := a.env.Store.Load(ctx); ok {
		req.Header.Set("X-Last", v)
	}
	if err := a.env.Store.Save(ctx, a.key.Reveal(), a.env.Clock.Now().Add(time.Hour)); err != nil {
		return err
	}
	a.env.Observe("success")
	req.Header.Set("X-Key", a.key.Reveal())
	return nil
}

func (a *echoAuth) OnAuthFailure(ctx context.Context, _ *http.Response) (bool, error) {
	a.env.Store.Delete(ctx)
	return false, nil
}

func (a *echoAuth) Status() (time.Time, bool) { return a.env.Clock.Now(), true }

type countObserver struct{ results []string }

func (o *countObserver) TokenRequest(_, _, result string) { o.results = append(o.results, result) }

func TestPlugins(t *testing.T) {
	p := &echoPlugin{}
	obs := &countObserver{}
	store := newMemStore()
	clk := clock.NewFake(start)
	m, err := NewManager(ManagerOptions{Clock: clk, Store: store, StateKey: "k", Plugins: NewPlugins(p), Observer: obs})
	if err != nil {
		t.Fatal(err)
	}
	block := &config.Auth{Type: config.AuthPlugin, Plugin: &config.PluginAuth{Name: "echo", Config: map[string]any{"key": map[string]any{"secret": "apiKey"}}}}
	refs, err := m.ValidatePlugin(block.Plugin)
	if err != nil || len(refs) != 1 || refs[0] != "apiKey" {
		t.Fatalf("ValidatePlugin() = %v, %v", refs, err)
	}
	base, _ := url.Parse("https://api.example.invalid")
	session := func(key string) Session {
		return Session{Scope: Scope{Provider: "p", Account: "acme"}, BaseURL: base, Secrets: secretsOf(map[string]string{"apiKey": key})}
	}
	a, err := m.For(block, session("one"))
	if err != nil {
		t.Fatal(err)
	}
	// Shared while the secrets do not change.
	if again, _ := m.For(block, session("one")); again != a || p.created != 1 {
		t.Errorf("the session is not shared: %d created", p.created)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.invalid/x", http.NoBody)
	if err := a.Decorate(context.Background(), req); err != nil || req.Header.Get("X-Key") != "one" {
		t.Fatalf("Decorate() = %v, %q", err, req.Header.Get("X-Key"))
	}
	// A changed secret opens a new session, which reads the stored state.
	b, err := m.For(block, session("two"))
	if err != nil || b == a {
		t.Fatalf("a changed secret kept the session: %v", err)
	}
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, "https://api.example.invalid/x", http.NoBody)
	if err := b.Decorate(context.Background(), req); err != nil || req.Header.Get("X-Last") != "one" {
		t.Fatalf("the stored state is not shared: %q, %v", req.Header.Get("X-Last"), err)
	}
	for _, sealed := range store.tokens {
		if strings.Contains(string(sealed), "one") || strings.Contains(string(sealed), "two") {
			t.Error("the plugin state is stored unencrypted with a state key")
		}
	}
	if retry, _ := b.OnAuthFailure(context.Background(), nil); retry || len(store.tokens) != 0 {
		t.Errorf("Delete() left %d entries", len(store.tokens))
	}
	if len(obs.results) != 2 {
		t.Errorf("observed %v", obs.results)
	}
	info := m.Sessions()
	if len(info) != 2 || !info[0].Reauth || info[0].Account != "acme" {
		t.Errorf("Sessions() = %+v", info)
	}

	// Errors.
	if _, err := m.For(block, Session{Scope: Scope{Account: "acme"}, BaseURL: base, Secrets: Secrets{}}); err == nil ||
		!strings.Contains(err.Error(), `auth plugin echo: secret "apiKey" is not provided`) {
		t.Errorf("missing secret: %v", err)
	}
	unknown := &config.PluginAuth{Name: "nope"}
	if _, err := m.ValidatePlugin(unknown); err == nil || !strings.Contains(err.Error(), `unknown auth plugin "nope" (available: echo)`) {
		t.Errorf("unknown plugin: %v", err)
	}
	if _, err := m.For(&config.Auth{Type: config.AuthPlugin, Plugin: unknown}, session("one")); err == nil {
		t.Error("For() accepted an unknown plugin")
	}
	bad := &config.PluginAuth{Name: "echo", Config: map[string]any{"key": "literal"}}
	if _, err := m.For(&config.Auth{Type: config.AuthPlugin, Plugin: bad}, session("one")); err == nil ||
		!strings.Contains(err.Error(), "key must be a secret reference") {
		t.Errorf("invalid config: %v", err)
	}
	if _, err := PluginSecret(map[string]any{"key": map[string]any{"secret": "x"}}, "key", Secrets{}); err == nil {
		t.Error("PluginSecret() accepted a missing secret")
	}
	if _, err := PluginSecret(map[string]any{}, "key", Secrets{}); err == nil {
		t.Error("PluginSecret() accepted a missing value")
	}

	// Without a store, the state lives in memory only.
	m2, _ := NewManager(ManagerOptions{Clock: clk, Plugins: NewPlugins(&echoPlugin{})})
	c, err := m2.For(block, session("three"))
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Decorate(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}
