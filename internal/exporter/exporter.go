package exporter

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"time"
)

const defaultEndpoint = "https://api-dev.pump.co/metrics-ingestion/cluster-metrics"

// Config holds export destination and identity (cluster/customer).
type Config struct {
	Endpoint   string
	Enabled    bool
	ClusterID  string
	CustomerID string
}

// ConfigFromEnv builds config from environment variables.
func ConfigFromEnv() Config {
	endpoint := os.Getenv("METRICS_EXPORT_ENDPOINT")
	enabled := endpoint != ""
	if endpoint == "" {
		endpoint = defaultEndpoint
	}
	if v := os.Getenv("METRICS_EXPORT_ENABLED"); v == "false" || v == "0" {
		enabled = false
	}
	clusterID := os.Getenv("CLUSTER_ID")
	if clusterID == "" {
		clusterID = "unknown"
	}
	customerID := os.Getenv("CUSTOMER_ID")
	if customerID == "" {
		customerID = "1"
	}
	return Config{
		Endpoint:   endpoint,
		Enabled:    enabled,
		ClusterID:  clusterID,
		CustomerID: customerID,
	}
}

type Client struct {
	httpClient *http.Client
}

func NewClient() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Export(endpoint, clusterID, customerID string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-Cluster-Id", clusterID)
	req.Header.Set("X-Customer-Id", customerID)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}
	return nil
}
