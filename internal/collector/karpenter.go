package collector

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"strings"
	"time"

	"k8s-agent/internal/cloud"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
)

var karpenterLog = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

const (
	labelKarpenterNodePool        = "karpenter.sh/nodepool"
	labelKarpenterProvisionerName = "karpenter.sh/provisioner-name" // pre-v0.32
	labelKarpenterCapacityType    = "karpenter.sh/capacity-type"
)

var (
	nodeClaimGVRv1 = schema.GroupVersionResource{
		Group: "karpenter.sh", Version: "v1", Resource: "nodeclaims",
	}
	nodeClaimGVRv1beta1 = schema.GroupVersionResource{
		Group: "karpenter.sh", Version: "v1beta1", Resource: "nodeclaims",
	}
	nodePoolGVRv1 = schema.GroupVersionResource{
		Group: "karpenter.sh", Version: "v1", Resource: "nodepools",
	}
	nodePoolGVRv1beta1 = schema.GroupVersionResource{
		Group: "karpenter.sh", Version: "v1beta1", Resource: "nodepools",
	}
)

// KarpenterNodeInfo captures Karpenter-managed node metadata for joining with AWS CUR data.
// InstanceID is the primary join key with CUR line_item_resource_id.
type KarpenterNodeInfo struct {
	NodeName     string `json:"node_name"`
	InstanceID   string `json:"instance_id,omitempty"`
	InstanceType string `json:"instance_type,omitempty"`
	CapacityType string `json:"capacity_type,omitempty"` // on-demand, spot
	NodePool     string `json:"node_pool,omitempty"`
	Zone         string `json:"zone,omitempty"`
	Region       string `json:"region,omitempty"`
	CreatedAt    string `json:"created_at,omitempty"`
}

type KarpenterEvent struct {
	Timestamp      string `json:"timestamp"`
	FirstTimestamp string `json:"first_timestamp,omitempty"`
	LastTimestamp   string `json:"last_timestamp,omitempty"`
	Reason         string `json:"reason"`
	Message        string `json:"message"`
	InvolvedObject string `json:"involved_object"`
	ObjectKind     string `json:"object_kind"`
	Count          int32  `json:"count"`
}

// KarpenterNodeClaim captures NodeClaim CRD status for node lifecycle tracking.
// ProviderID/InstanceID correlate with CUR; Conditions track provisioning lifecycle.
type KarpenterNodeClaim struct {
	Name         string               `json:"name"`
	NodePool     string               `json:"node_pool,omitempty"`
	NodeName     string               `json:"node_name,omitempty"`
	InstanceType string               `json:"instance_type,omitempty"`
	CapacityType string               `json:"capacity_type,omitempty"`
	Zone         string               `json:"zone,omitempty"`
	ProviderID   string               `json:"provider_id,omitempty"`
	InstanceID   string               `json:"instance_id,omitempty"`
	CreatedAt    string               `json:"created_at"`
	Conditions   []KarpenterCondition `json:"conditions,omitempty"`
}

type KarpenterCondition struct {
	Type               string `json:"type"`
	Status             string `json:"status"`
	LastTransitionTime string `json:"last_transition_time,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
}

type KarpenterNodePoolInfo struct {
	Name                string `json:"name"`
	ConsolidationPolicy string `json:"consolidation_policy,omitempty"`
	ConsolidateAfter    string `json:"consolidate_after,omitempty"`
	ExpireAfter         string `json:"expire_after,omitempty"`
}

type KarpenterMetrics struct {
	CollectedAt string                  `json:"collected_at"`
	ClusterID   string                  `json:"cluster_id"`
	Nodes       []KarpenterNodeInfo     `json:"nodes"`
	Events      []KarpenterEvent        `json:"events"`
	NodeClaims  []KarpenterNodeClaim    `json:"node_claims"`
	NodePools   []KarpenterNodePoolInfo `json:"node_pools"`
}

func CollectKarpenter(ctx context.Context, client kubernetes.Interface, dynClient dynamic.Interface, clusterID string) *KarpenterMetrics {
	ts := time.Now().UTC().Format(time.RFC3339)
	metrics := &KarpenterMetrics{
		CollectedAt: ts,
		ClusterID:   clusterID,
		Nodes:       []KarpenterNodeInfo{},
		Events:      []KarpenterEvent{},
		NodeClaims:  []KarpenterNodeClaim{},
		NodePools:   []KarpenterNodePoolInfo{},
	}

	collectKarpenterNodes(ctx, client, metrics)
	collectKarpenterEvents(ctx, client, metrics)
	if dynClient != nil {
		collectNodeClaims(ctx, dynClient, metrics)
		collectNodePools(ctx, dynClient, metrics)
	}

	logKarpenterMetrics(metrics)
	return metrics
}

func collectKarpenterNodes(ctx context.Context, client kubernetes.Interface, metrics *KarpenterMetrics) {
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Error("karpenter_nodes_list_failed", "error", err)
		return
	}

	for i := range nodes.Items {
		node := &nodes.Items[i]
		nodePool := node.Labels[labelKarpenterNodePool]
		if nodePool == "" {
			nodePool = node.Labels[labelKarpenterProvisionerName]
		}
		if nodePool == "" {
			continue
		}

		_, instanceType, instanceID, zone, region := nodeCloudInfo(node)
		metrics.Nodes = append(metrics.Nodes, KarpenterNodeInfo{
			NodeName:     node.Name,
			InstanceID:   instanceID,
			InstanceType: instanceType,
			CapacityType: node.Labels[labelKarpenterCapacityType],
			NodePool:     nodePool,
			Zone:         zone,
			Region:       region,
			CreatedAt:    node.CreationTimestamp.Format(time.RFC3339),
		})
	}
	karpenterLog.Info("karpenter_nodes_collected", "count", len(metrics.Nodes))
}

func collectKarpenterEvents(ctx context.Context, client kubernetes.Interface, metrics *KarpenterMetrics) {
	eventList, err := client.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Error("karpenter_events_list_failed", "error", err)
		return
	}

	for i := range eventList.Items {
		event := &eventList.Items[i]
		if !isKarpenterEvent(event) {
			continue
		}
		ke := KarpenterEvent{
			Reason:         event.Reason,
			Message:        event.Message,
			InvolvedObject: event.InvolvedObject.Name,
			ObjectKind:     event.InvolvedObject.Kind,
			Count:          event.Count,
		}
		if !event.EventTime.IsZero() {
			ke.Timestamp = event.EventTime.Format(time.RFC3339)
		} else if !event.LastTimestamp.IsZero() {
			ke.Timestamp = event.LastTimestamp.Format(time.RFC3339)
		}
		if !event.FirstTimestamp.IsZero() {
			ke.FirstTimestamp = event.FirstTimestamp.Format(time.RFC3339)
		}
		if !event.LastTimestamp.IsZero() {
			ke.LastTimestamp = event.LastTimestamp.Format(time.RFC3339)
		}
		metrics.Events = append(metrics.Events, ke)
	}
	karpenterLog.Info("karpenter_events_collected", "count", len(metrics.Events))
}

func isKarpenterEvent(event *corev1.Event) bool {
	return strings.Contains(strings.ToLower(event.Source.Component), "karpenter") ||
		strings.Contains(strings.ToLower(event.ReportingController), "karpenter")
}

func collectNodeClaims(ctx context.Context, dynClient dynamic.Interface, metrics *KarpenterMetrics) {
	list, err := dynClient.Resource(nodeClaimGVRv1).List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Debug("nodeclaims_v1_list_failed", "error", err)
		list, err = dynClient.Resource(nodeClaimGVRv1beta1).List(ctx, metav1.ListOptions{})
		if err != nil {
			karpenterLog.Debug("nodeclaims_v1beta1_list_failed", "error", err)
			return
		}
	}

	for i := range list.Items {
		item := &list.Items[i]
		labels := item.GetLabels()
		obj := item.Object

		nodePool := labels[labelKarpenterNodePool]
		if nodePool == "" {
			nodePool = labels[labelKarpenterProvisionerName]
		}

		status, _ := obj["status"].(map[string]interface{})
		providerID := nestedStr(status, "providerID")
		var instanceID string
		if providerID != "" {
			_, instanceID, _ = cloud.Parse(providerID)
		}

		metrics.NodeClaims = append(metrics.NodeClaims, KarpenterNodeClaim{
			Name:         item.GetName(),
			NodePool:     nodePool,
			NodeName:     nestedStr(status, "nodeName"),
			InstanceType: nestedStr(status, "instanceType"),
			CapacityType: labels[labelKarpenterCapacityType],
			Zone:         nestedStr(status, "zone"),
			ProviderID:   providerID,
			InstanceID:   instanceID,
			CreatedAt:    item.GetCreationTimestamp().Format(time.RFC3339),
			Conditions:   extractConditions(status),
		})
	}
	karpenterLog.Info("karpenter_nodeclaims_collected", "count", len(metrics.NodeClaims))
}

func collectNodePools(ctx context.Context, dynClient dynamic.Interface, metrics *KarpenterMetrics) {
	list, err := dynClient.Resource(nodePoolGVRv1).List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Debug("nodepools_v1_list_failed", "error", err)
		list, err = dynClient.Resource(nodePoolGVRv1beta1).List(ctx, metav1.ListOptions{})
		if err != nil {
			karpenterLog.Debug("nodepools_v1beta1_list_failed", "error", err)
			return
		}
	}

	for i := range list.Items {
		item := &list.Items[i]
		obj := item.Object
		spec, _ := obj["spec"].(map[string]interface{})
		disruption, _ := spec["disruption"].(map[string]interface{})

		metrics.NodePools = append(metrics.NodePools, KarpenterNodePoolInfo{
			Name:                item.GetName(),
			ConsolidationPolicy: nestedStr(disruption, "consolidationPolicy"),
			ConsolidateAfter:    nestedStr(disruption, "consolidateAfter"),
			ExpireAfter:         nestedStr(disruption, "expireAfter"),
		})
	}
	karpenterLog.Info("karpenter_nodepools_collected", "count", len(metrics.NodePools))
}

func nestedStr(m map[string]interface{}, keys ...string) string {
	if m == nil {
		return ""
	}
	current := m
	for i, key := range keys {
		if i == len(keys)-1 {
			s, _ := current[key].(string)
			return s
		}
		next, ok := current[key].(map[string]interface{})
		if !ok {
			return ""
		}
		current = next
	}
	return ""
}

func extractConditions(status map[string]interface{}) []KarpenterCondition {
	if status == nil {
		return nil
	}
	conditionsRaw, ok := status["conditions"].([]interface{})
	if !ok {
		return nil
	}
	conditions := make([]KarpenterCondition, 0, len(conditionsRaw))
	for _, raw := range conditionsRaw {
		cond, ok := raw.(map[string]interface{})
		if !ok {
			continue
		}
		conditions = append(conditions, KarpenterCondition{
			Type:               nestedStr(cond, "type"),
			Status:             nestedStr(cond, "status"),
			LastTransitionTime: nestedStr(cond, "lastTransitionTime"),
			Reason:             nestedStr(cond, "reason"),
			Message:            nestedStr(cond, "message"),
		})
	}
	return conditions
}

func logKarpenterMetrics(metrics *KarpenterMetrics) {
	data, err := json.Marshal(metrics)
	if err != nil {
		karpenterLog.Error("karpenter_metrics_marshal_failed", "error", err)
		return
	}
	karpenterLog.Info("karpenter_metrics_collected",
		"cluster_id", metrics.ClusterID,
		"karpenter_nodes", len(metrics.Nodes),
		"karpenter_events", len(metrics.Events),
		"node_claims", len(metrics.NodeClaims),
		"node_pools", len(metrics.NodePools),
	)
	karpenterLog.Info("karpenter_metrics", "payload", json.RawMessage(data))
}
