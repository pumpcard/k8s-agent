package export

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"

	"k8s-agent/internal/collector"
	"k8s-agent/internal/pump"

	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const payloadPreviewLen = 10000 // max chars of request body in debug logs

// Payload is a collected metrics snapshot and its JSON for logging/sending.
type Payload struct {
	Metrics collector.ClusterMetricsPayload
	JSON    []byte // Marshaled Metrics; use for size log or as base for export body
}

// CollectPayload collects cluster metrics and marshals to JSON. Caller can use Payload.Metrics and Payload.JSON (e.g. len for logging).
func CollectPayload(ctx context.Context, client *kubernetes.Clientset, clusterID string, metricsClient *metricsclient.Clientset) (*Payload, error) {
	metrics := collector.Collect(ctx, client, clusterID, metricsClient)
	jsonData, err := json.Marshal(metrics)
	if err != nil {
		return nil, err
	}
	return &Payload{Metrics: metrics, JSON: jsonData}, nil
}

func totalPods(metrics *collector.ClusterMetricsPayload) int {
	n := 0
	for i := range metrics.Nodes {
		n += len(metrics.Nodes[i].Pods)
	}
	return n
}

func truncateForLog(s string, maxLen int) string {
	if maxLen <= 0 || len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "...[truncated]"
}

// GetExportIDs returns cluster_id and account_id for the export body (trimmed).
// account_id comes only from the collector (node providerID or labels, e.g. pump.co/account-id on EKS).
func GetExportIDs(ctx context.Context, metrics collector.ClusterMetricsPayload) (clusterIDBody, accountIDBody string) {
	return strings.TrimSpace(metrics.ClusterID), strings.TrimSpace(metrics.AccountID)
}

// ResolveExportIDs returns cluster_id and account_id for export.
func ResolveExportIDs(ctx context.Context, log *slog.Logger, metrics collector.ClusterMetricsPayload) (clusterIDBody, accountIDBody string) {
	return GetExportIDs(ctx, metrics)
}

// Export sets cluster_id/account_id on metrics, re-marshals, logs, and sends to Pump. Caller must ensure IDs are non-empty.
func Export(log *slog.Logger, pumpCfg pump.Config, pumpClient *pump.Client, clusterID, clusterIDBody, accountIDBody string, metrics *collector.ClusterMetricsPayload) error {
	metrics.ClusterID = clusterIDBody
	metrics.AccountID = accountIDBody
	jsonData, err := json.Marshal(metrics)
	if err != nil {
		return err
	}

	return pumpClient.Send(pumpCfg.Endpoint, clusterID, jsonData)
}

// RunCycle collects metrics, logs payload size, and if Pump is enabled resolves IDs and sends to Pump.
// Returns (true, nil) when a payload was sent successfully, (false, nil) when skipped or Pump disabled, (false, err) on error.
func RunCycle(ctx context.Context, log *slog.Logger, client *kubernetes.Clientset, clusterID string, metricsClient *metricsclient.Clientset, dynClient dynamic.Interface, pumpCfg pump.Config, pumpClient *pump.Client) (exported bool, err error) {
	payload, err := CollectPayload(ctx, client, clusterID, metricsClient)
	if err != nil {
		return false, err
	}

	karpenterMetrics := collector.CollectKarpenter(ctx, client, dynClient, clusterID)

	payload.Metrics.Karpenter = karpenterMetrics

	if !pumpCfg.Enabled {
		return false, nil
	}
	clusterIDBody, accountIDBody := ResolveExportIDs(ctx, log, payload.Metrics)
	if err := Export(log, pumpCfg, pumpClient, clusterID, clusterIDBody, accountIDBody, &payload.Metrics); err != nil {
		return false, err
	}
	return true, nil
}
