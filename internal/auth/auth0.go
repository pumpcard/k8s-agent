package auth

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	auth0Domain   = "auth.pump.co"
	auth0Audience = "pump"
)

// Auth0Config holds Auth0 client credentials for machine-to-machine flow.
type Auth0Config struct {
	Domain       string
	ClientID     string
	ClientSecret string
	Audience     string
}

// tokenResponse from Auth0 token endpoint.
type tokenResponse struct {
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
	ExpiresIn   int64  `json:"expires_in"`
}

// TokenProvider fetches and caches JWT tokens from Auth0.
type TokenProvider struct {
	cfg       Auth0Config
	client    *http.Client
	mu        sync.RWMutex
	token     string
	expiresAt time.Time
}

// ConfigFromEnv builds Auth0Config from AUTH0_CLIENT_ID and AUTH0_CLIENT_SECRET.
// Returns nil if either is missing.
func ConfigFromEnv() *Auth0Config {
	clientID := strings.TrimSpace(os.Getenv("AUTH0_CLIENT_ID"))
	clientSecret := strings.TrimSpace(os.Getenv("AUTH0_CLIENT_SECRET"))

	if clientID == "" || clientSecret == "" {
		return nil
	}
	return &Auth0Config{
		Domain:       auth0Domain,
		ClientID:     clientID,
		ClientSecret: clientSecret,
		Audience:     auth0Audience,
	}
}

// NewTokenProvider creates a token provider for the given config.
func NewTokenProvider(cfg Auth0Config) *TokenProvider {
	return &TokenProvider{
		cfg:    cfg,
		client: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetToken returns a valid Bearer token, refreshing if necessary.
func (p *TokenProvider) GetToken() (string, error) {
	// Refresh if token expires in less than 5 minutes
	refreshThreshold := time.Now().Add(5 * time.Minute)

	p.mu.RLock()
	if p.token != "" && p.expiresAt.After(refreshThreshold) {
		token := p.token
		p.mu.RUnlock()
		return token, nil
	}
	p.mu.RUnlock()

	p.mu.Lock()
	defer p.mu.Unlock()

	// Double-check after acquiring write lock
	if p.token != "" && p.expiresAt.After(refreshThreshold) {
		return p.token, nil
	}

	if err := p.fetchToken(); err != nil {
		return "", err
	}
	return p.token, nil
}

func (p *TokenProvider) fetchToken() error {
	tokenURL := fmt.Sprintf("https://%s/oauth/token", strings.TrimPrefix(p.cfg.Domain, "https://"))

	body := url.Values{}
	body.Set("grant_type", "client_credentials")
	body.Set("client_id", p.cfg.ClientID)
	body.Set("client_secret", p.cfg.ClientSecret)
	body.Set("audience", p.cfg.Audience)

	req, err := http.NewRequest(http.MethodPost, tokenURL, bytes.NewBufferString(body.Encode()))
	if err != nil {
		return fmt.Errorf("auth0 token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	resp, err := p.client.Do(req)
	if err != nil {
		return fmt.Errorf("auth0 token request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("auth0 token request: status %d: %s", resp.StatusCode, string(respBody))
	}

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return fmt.Errorf("auth0 token response: %w", err)
	}
	if tr.AccessToken == "" {
		return fmt.Errorf("auth0 token response: empty access_token")
	}
	if tr.ExpiresIn <= 0 {
		tr.ExpiresIn = 60 // fallback to 1 minute
	}

	p.token = tr.AccessToken
	p.expiresAt = time.Now().Add(time.Duration(tr.ExpiresIn) * time.Second)
	return nil
}
