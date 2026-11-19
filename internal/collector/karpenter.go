package collector

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
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
	Name               string `json:"name"`
	NodeCount          int    `json:"node_count"`
	Ready              *bool  `json:"ready,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	CPULimitMillicores *int64 `json:"cpu_limit_millicores,omitempty"`
	MemoryLimitBytes   *int64 `json:"memory_limit_bytes,omitempty"`
	NodeLimit          *int64 `json:"node_limit,omitempty"`
	CPUUsageMillicores *int64 `json:"cpu_usage_millicores,omitempty"`
	MemoryUsageBytes   *int64 `json:"memory_usage_bytes,omitempty"`
}

type NodeClaimInfo struct {
	UID           string `json:"uid"`
	Name          string `json:"name"`
	Reason        string `json:"reason,omitempty"`
	Message       string `json:"message,omitempty"`
	LaunchedAt    string `json:"launched_at,omitempty"`
	RegisteredAt  string `json:"registered_at,omitempty"`
	InitializedAt string `json:"initialized_at,omitempty"`
}

type PendingPodInfo struct {
	UID              string `json:"uid"`
	Namespace        string `json:"namespace"`
	Name             string `json:"name"`
	ConditionReason  string `json:"condition_reason,omitempty"`
	ConditionMessage string `json:"condition_message,omitempty"`
}

type KarpenterMetrics struct {
	CollectedAt  string           `json:"collected_at"`
	ClusterID    string           `json:"cluster_id"`
	Events       []KarpenterEvent `json:"events"`
	NodePools    []NodePoolInfo   `json:"node_pools"`
	NodeClaims   []NodeClaimInfo  `json:"node_claims"`
	PendingPods  []PendingPodInfo `json:"pending_pods"`
	Capabilities []string         `json:"capabilities"`
}

func CollectKarpenter(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface, clusterID string) *KarpenterMetrics {
	ts := time.Now().UTC().Format(time.RFC3339)
	metrics := &KarpenterMetrics{
		CollectedAt:  ts,
		ClusterID:    clusterID,
		Events:       []KarpenterEvent{},
		NodePools:    []NodePoolInfo{},
		NodeClaims:   []NodeClaimInfo{},
		PendingPods:  []PendingPodInfo{},
		Capabilities: []string{},
	}

	if collectKarpenterEvents(ctx, client, metrics) {
		metrics.Capabilities = append(metrics.Capabilities, "events")
	}
	if dynClient != nil && collectNodePools(ctx, client, dynClient, metrics) {
		collectNodeClaims(ctx, dynClient, metrics)
		collectPendingPods(ctx, client, metrics)
	}

	logKarpenterMetrics(metrics)
	return metrics
}

func collectKarpenterEvents(ctx context.Context, client kubernetes.Interface, metrics *KarpenterMetrics) bool {
	eventList, err := client.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Error("karpenter_events_list_failed", "error", err)
		return false
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
	return true
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

var nodeClaimGVR = schema.GroupVersionResource{
	Group:    "karpenter.sh",
	Version:  "v1",
	Resource: "nodeclaims",
}

func collectNodePools(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface, metrics *KarpenterMetrics) bool {
	list, err := dynClient.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("nodepool_list_failed", "error", err)
		return false
	}
	metrics.Capabilities = append(metrics.Capabilities, "node_pools")

	nodeCountByPool := make(map[string]int)
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("nodepool_node_count_failed", "error", err)
	} else {
		metrics.Capabilities = append(metrics.Capabilities, "node_karpenter_labels")
		for i := range nodes.Items {
			if pool := nodes.Items[i].Labels["karpenter.sh/nodepool"]; pool != "" {
				nodeCountByPool[pool]++
			}
		}
	}

	for i := range list.Items {
		item := list.Items[i].Object
		name := list.Items[i].GetName()
		np := NodePoolInfo{
			Name:               name,
			NodeCount:          nodeCountByPool[name],
			CPULimitMillicores: quantityAt(item, true, "spec", "limits", "cpu"),
			MemoryLimitBytes:   quantityAt(item, false, "spec", "limits", "memory"),
			NodeLimit:          quantityAt(item, false, "spec", "limits", "nodes"),
			CPUUsageMillicores: quantityAt(item, true, "status", "resources", "cpu"),
			MemoryUsageBytes:   quantityAt(item, false, "status", "resources", "memory"),
		}
		if ready := conditionAt(item, "Ready"); ready != nil {
			if status := ready["status"]; status == "True" || status == "False" {
				isReady := status == "True"
				np.Ready = &isReady
			}
			np.Reason, _ = ready["reason"].(string)
			np.Message, _ = ready["message"].(string)
		}
		karpenterLog.Info("nodepool_collected", "name", np.Name, "node_count", np.NodeCount)
		metrics.NodePools = append(metrics.NodePools, np)
	}
	karpenterLog.Info("nodepools_collected", "count", len(metrics.NodePools))
	return true
}

func collectNodeClaims(ctx context.Context, dynClient dynamic.Interface, metrics *KarpenterMetrics) {
	list, err := dynClient.Resource(nodeClaimGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("nodeclaim_list_failed", "error", err)
		return
	}
	for i := range list.Items {
		item := list.Items[i].Object
		claim := NodeClaimInfo{
			UID:           string(list.Items[i].GetUID()),
			Name:          list.Items[i].GetName(),
			LaunchedAt:    trueSince(item, "Launched"),
			RegisteredAt:  trueSince(item, "Registered"),
			InitializedAt: trueSince(item, "Initialized"),
		}
		for _, conditionType := range []string{"Launched", "Registered", "Initialized", "Ready"} {
			if c := conditionAt(item, conditionType); c != nil && c["status"] != "True" {
				claim.Reason, _ = c["reason"].(string)
				claim.Message, _ = c["message"].(string)
				break
			}
		}
		metrics.NodeClaims = append(metrics.NodeClaims, claim)
	}
	metrics.Capabilities = append(metrics.Capabilities, "node_claims")
}

func collectPendingPods(ctx context.Context, client kubernetes.Interface, metrics *KarpenterMetrics) {
	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "status.phase=Pending"})
	if err != nil {
		karpenterLog.Warn("pending_pods_list_failed", "error", err)
		return
	}
	for i := range pods.Items {
		pod := &pods.Items[i]
		if pod.Status.Phase != corev1.PodPending || pod.Spec.NodeName != "" {
			continue
		}
		pending := PendingPodInfo{UID: string(pod.UID), Namespace: pod.Namespace, Name: pod.Name}
		for _, c := range pod.Status.Conditions {
			if c.Type == corev1.PodScheduled {
				pending.ConditionReason, pending.ConditionMessage = c.Reason, c.Message
			}
		}
		metrics.PendingPods = append(metrics.PendingPods, pending)
	}
	metrics.Capabilities = append(metrics.Capabilities, "pending_pods")
}

func conditionAt(object map[string]any, conditionType string) map[string]any {
	conditions, _, _ := unstructured.NestedSlice(object, "status", "conditions")
	for _, raw := range conditions {
		if c, ok := raw.(map[string]any); ok && c["type"] == conditionType {
			return c
		}
	}
	return nil
}

func trueSince(object map[string]any, conditionType string) string {
	if c := conditionAt(object, conditionType); c != nil && c["status"] == "True" {
		since, _ := c["lastTransitionTime"].(string)
		return since
	}
	return ""
}

func quantityAt(object map[string]any, milli bool, fields ...string) *int64 {
	raw, found, _ := unstructured.NestedFieldNoCopy(object, fields...)
	if !found {
		return nil
	}
	q, err := resource.ParseQuantity(fmt.Sprint(raw))
	if err != nil {
		return nil
	}
	value := q.Value()
	if milli {
		value = q.MilliValue()
	}
	return &value
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
