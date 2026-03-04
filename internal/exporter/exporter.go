package exporter

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"time"

	"k8s-agent/internal/auth"
)

const defaultEndpoint = "https://api-dev.pump.co/metrics-ingestion/cluster-metrics"
const defaultTimeout = 90 * time.Second

// Config holds export destination, identity (cluster/customer), and timeout.
type Config struct {
	Endpoint string
	Enabled  bool
	Timeout  time.Duration
	Auth     *auth.TokenProvider // nil when auth not configured
}

// ConfigFromEnv builds config from environment variables.
// METRICS_EXPORT_TIMEOUT_SECONDS sets HTTP client timeout (default 90).
func ConfigFromEnv() Config {
	endpoint := os.Getenv("METRICS_EXPORT_ENDPOINT")
	enabled := endpoint != ""
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if v := os.Getenv("METRICS_EXPORT_ENABLED"); v == "false" || v == "0" {
		enabled = false
	}
	timeout := defaultTimeout
	if s := os.Getenv("METRICS_EXPORT_TIMEOUT_SECONDS"); s != "" {
		if n, err := strconv.Atoi(s); err == nil && n > 0 {
			timeout = time.Duration(n) * time.Second
		}
	}
	cfg := Config{
		Endpoint: endpoint,
		Enabled:  enabled,
		Timeout:  timeout,
	}
	if authCfg := auth.ConfigFromEnv(); authCfg != nil {
		cfg.Auth = auth.NewTokenProvider(*authCfg)
	}
	return cfg
}

type Client struct {
	httpClient *http.Client
	auth       *auth.TokenProvider
}

func NewClient(cfg Config) *Client {
	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = defaultTimeout
	}
	return &Client{
		httpClient: &http.Client{Timeout: timeout},
		auth:       cfg.Auth,
	}
}

func (c *Client) Export(endpoint, clusterID string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Id", clusterID)
	if c.auth != nil {
		token, err := c.auth.GetToken()
		if err != nil {
			return fmt.Errorf("auth token: %w", err)
		}
		req.Header.Set("Authorization", "Bearer "+token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		if len(body) > 0 {
			return fmt.Errorf("unexpected status %d: %s", resp.StatusCode, bytes.TrimSpace(body))
		}
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
