package arubacentral

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/kentrow/tapyr/internal/auth"
)

// simAccept is auth.accept.plugin of a Simulation: the API Gateway
// applications and the bootstrap token pairs the simulator accepts.
type simAccept struct {
	Clients []struct {
		ClientID     string `json:"clientId"`
		ClientSecret string `json:"clientSecret"`
	} `json:"clients"`
	// RefreshTokens are the bootstrap refresh tokens, valid once each.
	RefreshTokens []string `json:"refreshTokens"`
	// AccessTokens are bootstrap access tokens, valid for one lifetime
	// from the simulator start.
	AccessTokens []string `json:"accessTokens"`
}

// Simulator implements auth.Plugin: the refresh endpoint rotates the
// refresh tokens like Aruba Central, each one being valid once.
func (Plugin) Simulator(env auth.SimulatorEnv, accept map[string]any) (auth.PluginSimulator, error) {
	data, err := json.Marshal(accept)
	if err != nil {
		return nil, err
	}
	var a simAccept
	if err := json.Unmarshal(data, &a); err != nil {
		return nil, fmt.Errorf("auth.accept.plugin: %w", err)
	}
	if len(a.Clients) == 0 || len(a.RefreshTokens) == 0 {
		return nil, errors.New("auth.accept.plugin needs clients and refreshTokens")
	}
	s := &simulator{env: env, accept: a, lifetime: env.TokenLifetime, access: map[string]simToken{}, refresh: map[string]string{}}
	if s.lifetime <= 0 {
		s.lifetime = defaultLifetime
	}
	now := env.Clock.Now()
	for _, t := range a.RefreshTokens {
		s.refresh[t] = a.Clients[0].ClientID
	}
	for _, t := range a.AccessTokens {
		s.access[t] = simToken{client: a.Clients[0].ClientID, issued: now, expiry: now.Add(s.lifetime)}
	}
	return s, nil
}

type simToken struct {
	client         string
	issued, expiry time.Time
}

type simulator struct {
	env      auth.SimulatorEnv
	accept   simAccept
	lifetime time.Duration

	mu      sync.Mutex
	seq     int
	issued  int64
	access  map[string]simToken
	refresh map[string]string // valid refresh token -> client
}

// TokenRequest implements auth.PluginSimulator.
func (s *simulator) TokenRequest(req *http.Request) (string, string, bool, bool) {
	if req.Method != http.MethodPost || req.URL.Path != s.env.BasePath+defaultTokenPath {
		return "", "", false, false
	}
	q := req.URL.Query()
	id, sec := q.Get("client_id"), q.Get("client_secret")
	for _, c := range s.accept.Clients {
		if equal(id, c.ClientID) && equal(sec, c.ClientSecret) {
			return defaultTokenPath, id, true, true
		}
	}
	return defaultTokenPath, "", true, false
}

// ServeToken implements auth.PluginSimulator.
func (s *simulator) ServeToken(w http.ResponseWriter, req *http.Request, identity string) {
	q := req.URL.Query()
	s.mu.Lock()
	defer s.mu.Unlock()
	client, ok := s.refresh[q.Get("refresh_token")]
	if q.Get("grant_type") != "refresh_token" || !ok || client != identity {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error_description":"Invalid refresh_token","error":"invalid_grant"}`))
		return
	}
	delete(s.refresh, q.Get("refresh_token")) // rotated: valid once
	s.seq++
	s.issued++
	access, refresh := fmt.Sprintf("sim-access-%d", s.seq), fmt.Sprintf("sim-refresh-%d", s.seq)
	now := s.env.Clock.Now()
	s.access[access] = simToken{client: identity, issued: now, expiry: now.Add(s.lifetime)}
	s.refresh[refresh] = identity
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"access_token": access, "refresh_token": refresh, "token_type": "bearer", "expires_in": int64(s.lifetime / time.Second),
	})
}

// Identity implements auth.PluginSimulator.
func (s *simulator) Identity(req *http.Request) (string, bool) {
	token, ok := strings.CutPrefix(req.Header.Get("Authorization"), "Bearer ")
	if !ok {
		return "", false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	t, ok := s.access[token]
	if !ok || !s.env.Clock.Now().Before(t.expiry) {
		return "", false
	}
	return t.client, true
}

// ExpireBefore implements auth.PluginSimulator.
func (s *simulator) ExpireBefore(t time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for k, v := range s.access {
		if !v.issued.After(t) {
			delete(s.access, k)
		}
	}
}

// Issued implements auth.PluginSimulator.
func (s *simulator) Issued() int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.issued
}

func equal(a, b string) bool {
	return a != "" && subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
