package collector

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var karpenterLog = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

var lastSeenResourceVersion int64

type KarpenterEvent struct {
	UID            string `json:"uid"`
	Timestamp      string `json:"timestamp"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	InvolvedObject string `json:"involved_object"`
	ObjectKind     string `json:"object_kind"`
	Count          int32  `json:"count"`
}

type NodePoolInfo struct {
	Name      string `json:"name"`
	NodeCount int    `json:"node_count"`
}

type KarpenterMetrics struct {
	CollectedAt string           `json:"collected_at"`
	ClusterID   string           `json:"cluster_id"`
	Events      []KarpenterEvent `json:"events"`
	NodePools   []NodePoolInfo   `json:"node_pools"`
}

func CollectKarpenter(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface, clusterID string) *KarpenterMetrics {
	ts := time.Now().UTC().Format(time.RFC3339)
	metrics := &KarpenterMetrics{
		CollectedAt: ts,
		ClusterID:   clusterID,
		Events:      []KarpenterEvent{},
		NodePools:   []NodePoolInfo{},
	}

	collectKarpenterEvents(ctx, client, metrics)
	if dynClient != nil {
		collectNodePools(ctx, client, dynClient, metrics)
	}

	logKarpenterMetrics(metrics)
	return metrics
}

func collectKarpenterEvents(ctx context.Context, client kubernetes.Interface, metrics *KarpenterMetrics) {
	eventList, err := client.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Error("karpenter_events_list_failed", "error", err)
		return
	}

	var maxRV int64
	for i := range eventList.Items {
		event := &eventList.Items[i]
		if !isKarpenterSource(event) || !isRelevantEventReason[event.Reason] {
			continue
		}
		rv, _ := strconv.ParseInt(event.ResourceVersion, 10, 64)
		if rv > maxRV {
			maxRV = rv
		}
		if rv <= lastSeenResourceVersion {
			continue
		}
		ke := KarpenterEvent{
			UID:            string(event.UID),
			Reason:         event.Reason,
			Message:        event.Message,
			InvolvedObject: event.InvolvedObject.Name,
			ObjectKind:     event.InvolvedObject.Kind,
			Count:          event.Count,
		}
		switch {
		case !event.EventTime.IsZero():
			ke.Timestamp = event.EventTime.Format(time.RFC3339)
		case !event.LastTimestamp.IsZero():
			ke.Timestamp = event.LastTimestamp.Format(time.RFC3339)
		case !event.FirstTimestamp.IsZero():
			ke.Timestamp = event.FirstTimestamp.Format(time.RFC3339)
		}
		karpenterLog.Info("karpenter_event",
			"uid", ke.UID,
			"reason", ke.Reason,
			"message", ke.Message,
			"involved_object", ke.InvolvedObject,
			"object_kind", ke.ObjectKind,
			"count", ke.Count,
			"timestamp", ke.Timestamp,
		)
		metrics.Events = append(metrics.Events, ke)
	}
	lastSeenResourceVersion = maxRV
	karpenterLog.Info("karpenter_events_collected", "count", len(metrics.Events))
}

var isRelevantEventReason = map[string]bool{
	"DisruptionTerminating": true,
}

func isKarpenterSource(event *corev1.Event) bool {
	return strings.Contains(strings.ToLower(event.Source.Component), "karpenter") ||
		strings.Contains(strings.ToLower(event.ReportingController), "karpenter")
}

var nodePoolGVR = schema.GroupVersionResource{
	Group:    "karpenter.sh",
	Version:  "v1",
	Resource: "nodepools",
}

func collectNodePools(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface, metrics *KarpenterMetrics) {
	list, err := dynClient.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("nodepool_list_failed", "error", err)
		return
	}

	nodeCountByPool := make(map[string]int)
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("nodepool_node_count_failed", "error", err)
	} else {
		for i := range nodes.Items {
			if pool := nodes.Items[i].Labels["karpenter.sh/nodepool"]; pool != "" {
				nodeCountByPool[pool]++
			}
		}
	}

	for i := range list.Items {
		name := list.Items[i].GetName()
		np := NodePoolInfo{
			Name:      name,
			NodeCount: nodeCountByPool[name],
		}
		karpenterLog.Info("nodepool_collected", "name", np.Name, "node_count", np.NodeCount)
		metrics.NodePools = append(metrics.NodePools, np)
	}
	karpenterLog.Info("nodepools_collected", "count", len(metrics.NodePools))
}

func logKarpenterMetrics(metrics *KarpenterMetrics) {
	data, err := json.Marshal(metrics)
	if err != nil {
		karpenterLog.Error("karpenter_metrics_marshal_failed", "error", err)
		return
	}
	karpenterLog.Info("karpenter_metrics_collected",
		"cluster_id", metrics.ClusterID,
		"karpenter_events", len(metrics.Events),
		"node_pools", len(metrics.NodePools),
	)
	karpenterLog.Info("karpenter_metrics", "payload", json.RawMessage(data))
}
