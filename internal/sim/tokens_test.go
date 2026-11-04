package sim

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kentrow/tapyr/internal/auth"
	"github.com/kentrow/tapyr/internal/clock"
	"github.com/kentrow/tapyr/internal/mapping"
	"github.com/kentrow/tapyr/internal/secret"
)

// plainSender sends auth requests without a budget engine.
type plainSender struct{}

func (plainSender) SendAuth(_ context.Context, _ auth.Scope, _ string, req *http.Request) (*http.Response, error) {
	return http.DefaultClient.Do(req)
}

// apiGet sends a request decorated by a, and on an auth failure retries
// once after OnAuthFailure, like the executor.
func apiGet(t *testing.T, a auth.Authenticator, u string) int {
	t.Helper()
	for attempt := 0; ; attempt++ {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, u, http.NoBody)
		if err := a.Decorate(context.Background(), req); err != nil {
			t.Fatalf("Decorate() = %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden || attempt > 0 {
			return resp.StatusCode
		}
		if retry, _ := a.OnAuthFailure(context.Background(), resp); !retry {
			return resp.StatusCode
		}
	}
}

func TestTokenFlows(t *testing.T) {
	routes := `  routes: [{ path: /orgs, response: { file: orgs.json } }]
`
	files := map[string]string{"sim/orgs.json": `[{"id": "o1"}]`, "sim/login.json": `{"data": {"token": "{token}", "ttl": {expiresIn}}}`}
	tests := []struct {
		name   string
		auth   string
		accept string
		client func(t *testing.T, m *auth.Manager, base *url.URL) (auth.Authenticator, error)
		badKey string
	}{
		{
			name: "oauth2 client credentials",
			auth: `{ type: oauth2_client_credentials, oauth2ClientCredentials: { tokenURL: "http://127.0.0.1/oauth/token", clientId: app, clientSecret: s3cr3t } }`,
			accept: `  auth: { accept: { clients: [{ clientId: app, clientSecret: s3cr3t }] }, tokenLifetime: 2m }
`,
		},
		{
			name: "session login with a header",
			auth: `{ type: session_login, sessionLogin: { request: { path: /login, body: { user: admin, pass: s3cr3t } },
			  token: response.body.data.token, apply: { header: X-Auth, format: "Token {token}" } } }`,
			accept: `  auth: { accept: { logins: [{ username: admin, password: s3cr3t }] }, tokenLifetime: 2m, loginResponse: login.json }
`,
		},
		{
			name: "session login with a cookie",
			auth: `{ type: session_login, sessionLogin: { request: { path: /login, body: { user: admin, pass: s3cr3t } },
			  token: response.body.token, apply: { cookie: sid } } }`,
			accept: `  auth: { accept: { logins: [{ username: admin, password: s3cr3t }] }, tokenLifetime: 2m }
`,
		},
	}
	env, err := mapping.NewEnv(0)
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			clk := clock.NewFake(time.Date(2026, time.November, 2, 9, 0, 0, 0, time.UTC))
			p := pkg(t, tt.auth, tt.accept+routes, files)
			s, err := New(p, Options{Clock: clk})
			if err != nil {
				t.Fatal(err)
			}
			srv := httptest.NewServer(s.Handler())
			defer srv.Close()
			base, _ := url.Parse(srv.URL)

			// The client side of the same definition, pointed at the server.
			a := *p.Provider.Spec.Auth
			session := auth.Session{Scope: auth.Scope{Account: "acme"}, BaseURL: base, Secrets: auth.Secrets{"x": secret.New("x")}}
			if o := a.OAuth2ClientCredentials; o != nil {
				oc := *o
				oc.TokenURL = "{{ .params.tokenURL }}"
				a.OAuth2ClientCredentials = &oc
				session.Vars = map[string]string{"tokenURL": srv.URL + "/oauth/token"}
			}
			if l := a.SessionLogin; l != nil {
				prg, err := env.Compile(mapping.ContextLogin, string(l.Token), mapping.OutputString)
				if err != nil {
					t.Fatal(err)
				}
				session.Login = &auth.LoginPrograms{Token: prg}
			}
			m, err := auth.NewManager(auth.ManagerOptions{Clock: clk})
			if err != nil {
				t.Fatal(err)
			}
			m.SetSender(plainSender{})
			client, err := m.For(&a, session)
			if err != nil {
				t.Fatal(err)
			}

			if status, _, _ := get(t, srv.URL+"/orgs", "sim-token-1"); status != http.StatusForbidden {
				t.Errorf("token not issued yet: status %d", status)
			}
			if status := apiGet(t, client, srv.URL+"/orgs"); status != http.StatusOK {
				t.Fatalf("with a token: status %d", status)
			}
			// After the token lifetime, the OAuth2 client has renewed its
			// token (expires_in); the login clients, without expiresIn, get
			// a rejection and log in again.
			clk.Advance(2 * time.Minute)
			if status := apiGet(t, client, srv.URL+"/orgs"); status != http.StatusOK {
				t.Fatalf("after the token lifetime: status %d", status)
			}
			if st := s.Stats(); st.TokensIssued != 2 {
				t.Errorf("%d tokens issued, want 2", st.TokensIssued)
			}
		})
	}
}

func TestTokenEndpointRejections(t *testing.T) {
	p := pkg(t, `{ type: oauth2_client_credentials, oauth2ClientCredentials: { tokenURL: "http://127.0.0.1/oauth/token", clientId: app, clientSecret: s3cr3t } }`,
		"  auth: { accept: { clients: [{ clientId: app, clientSecret: s3cr3t }] } }\n  routes: [{ path: /orgs, response: { file: orgs.json } }]\n",
		map[string]string{"sim/orgs.json": `[]`})
	s, err := New(p, Options{})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	for _, form := range []string{
		"grant_type=client_credentials&client_id=app&client_secret=wrong",
		"grant_type=password&client_id=app&client_secret=s3cr3t",
	} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+"/oauth/token", strings.NewReader(form))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusForbidden {
			t.Errorf("%s: status %d", form, resp.StatusCode)
		}
	}
	if _, err := New(pkg(t, `{ type: session_login, sessionLogin: { request: { path: /login }, token: response.body.t, apply: { cookie: c } } }`,
		"  auth: { loginResponse: missing.json }\n  routes: [{ path: /orgs, response: { file: orgs.json } }]\n",
		map[string]string{"sim/orgs.json": `[]`}), Options{}); err == nil || !strings.Contains(err.Error(), "login response missing.json") {
		t.Errorf("missing login response: %v", err)
	}
}

// TestLogout renews a session before it expires: the replaced session is
// logged out, and Close logs out the last one.
func TestLogout(t *testing.T) {
	clk := clock.NewFake(time.Date(2026, time.November, 2, 9, 0, 0, 0, time.UTC))
	p := pkg(t, `{ type: session_login, sessionLogin: { request: { path: /login, body: { user: admin, pass: s3cr3t } },
	  token: response.body.token, expiresIn: "duration(string(response.body.expiresIn) + 's')",
	  apply: { header: Authorization, format: "Bearer {token}" }, logout: { path: /logout } } }`,
		"  auth: { accept: { logins: [{ username: admin, password: s3cr3t }] }, tokenLifetime: 2m }\n  routes: [{ path: /orgs, response: { file: orgs.json } }]\n",
		map[string]string{"sim/orgs.json": `[]`})
	s, err := New(p, Options{Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	srv := httptest.NewServer(s.Handler())
	defer srv.Close()
	base, _ := url.Parse(srv.URL)
	env, err := mapping.NewEnv(0)
	if err != nil {
		t.Fatal(err)
	}
	l := p.Provider.Spec.Auth.SessionLogin
	token, err := env.Compile(mapping.ContextLogin, string(l.Token), mapping.OutputString)
	if err != nil {
		t.Fatal(err)
	}
	expires, err := env.Compile(mapping.ContextLogin, string(l.ExpiresIn), mapping.OutputDuration)
	if err != nil {
		t.Fatal(err)
	}
	m, err := auth.NewManager(auth.ManagerOptions{Clock: clk})
	if err != nil {
		t.Fatal(err)
	}
	m.SetSender(plainSender{})
	client, err := m.For(p.Provider.Spec.Auth, auth.Session{
		Scope: auth.Scope{Account: "acme"}, BaseURL: base, Login: &auth.LoginPrograms{Token: token, ExpiresIn: expires},
	})
	if err != nil {
		t.Fatal(err)
	}
	if status := apiGet(t, client, srv.URL+"/orgs"); status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	clk.Advance(100 * time.Second) // past refreshBefore (30s before the 2m expiry)
	if status := apiGet(t, client, srv.URL+"/orgs"); status != http.StatusOK {
		t.Fatalf("after the renewal: status %d", status)
	}
	if st := s.Stats(); st.TokensIssued != 2 || st.LoggedOut != 1 || st.OpenSessions != 1 {
		t.Errorf("after the renewal: %d issued, %d logged out, %d open; want 2, 1, 1", st.TokensIssued, st.LoggedOut, st.OpenSessions)
	}
	m.Close(context.Background())
	if st := s.Stats(); st.LoggedOut != 2 || st.OpenSessions != 0 {
		t.Errorf("after Close: %d logged out, %d open; want 2, 0", st.LoggedOut, st.OpenSessions)
	}
	// A closed session logs in again when used.
	if status := apiGet(t, client, srv.URL+"/orgs"); status != http.StatusOK {
		t.Fatalf("after Close: status %d", status)
	}
}
