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

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
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
	UID                 string `json:"uid"`
	Timestamp           string `json:"timestamp"`
	Reason              string `json:"reason"`
	Message             string `json:"message"`
	InvolvedObject      string `json:"involved_object"`
	ObjectKind          string `json:"object_kind"`
	Count               int32  `json:"count"`
	Namespace           string `json:"namespace,omitempty"`
	EventType           string `json:"event_type,omitempty"`
	ReportingController string `json:"reporting_controller,omitempty"`
	Action              string `json:"action,omitempty"`
	FirstTimestamp      string `json:"first_timestamp,omitempty"`
	LastTimestamp       string `json:"last_timestamp,omitempty"`
}

type NodePoolInfo struct {
	Name               string `json:"name"`
	NodeCount          int    `json:"node_count"`
	UID                string `json:"uid,omitempty"`
	Ready              *bool  `json:"ready,omitempty"`
	Reason             string `json:"reason,omitempty"`
	Message            string `json:"message,omitempty"`
	NodeClassName      string `json:"node_class_name,omitempty"`
	Weight             *int64 `json:"weight,omitempty"`
	CPULimitMillicores *int64 `json:"cpu_limit_millicores,omitempty"`
	MemoryLimitBytes   *int64 `json:"memory_limit_bytes,omitempty"`
	NodeLimit          *int64 `json:"node_limit,omitempty"`
	CPUUsageMillicores *int64 `json:"cpu_usage_millicores,omitempty"`
	MemoryUsageBytes   *int64 `json:"memory_usage_bytes,omitempty"`
	ObservedGeneration *int64 `json:"observed_generation,omitempty"`
}

type NodeClaimInfo struct {
	UID               string `json:"uid"`
	Name              string `json:"name"`
	NodePoolName      string `json:"node_pool_name,omitempty"`
	NodeClassName     string `json:"node_class_name,omitempty"`
	NodeName          string `json:"node_name,omitempty"`
	ProviderID        string `json:"provider_id,omitempty"`
	InstanceType      string `json:"instance_type,omitempty"`
	Zone              string `json:"zone,omitempty"`
	CapacityType      string `json:"capacity_type,omitempty"`
	CreatedAt         string `json:"created_at,omitempty"`
	LaunchedAt        string `json:"launched_at,omitempty"`
	RegisteredAt      string `json:"registered_at,omitempty"`
	InitializedAt     string `json:"initialized_at,omitempty"`
	ReadyAt           string `json:"ready_at,omitempty"`
	DeletionTimestamp string `json:"deletion_timestamp,omitempty"`
	Status            string `json:"status,omitempty"`
	Reason            string `json:"reason,omitempty"`
	Message           string `json:"message,omitempty"`
}

type PendingPodInfo struct {
	UID                  string `json:"uid"`
	Namespace            string `json:"namespace"`
	Name                 string `json:"name"`
	OwnerReference       string `json:"owner_reference,omitempty"`
	CreatedAt            string `json:"created_at,omitempty"`
	CPURequestMillicores int64  `json:"cpu_request_millicores"`
	MemoryRequestBytes   int64  `json:"memory_request_bytes"`
	ConditionReason      string `json:"condition_reason,omitempty"`
	ConditionMessage     string `json:"condition_message,omitempty"`
}

type KarpenterMetrics struct {
	CollectedAt       string           `json:"collected_at"`
	ClusterID         string           `json:"cluster_id"`
	Events            []KarpenterEvent `json:"events"`
	NodePools         []NodePoolInfo   `json:"node_pools"`
	NodeClaims        []NodeClaimInfo  `json:"node_claims"`
	PendingPods       []PendingPodInfo `json:"pending_pods"`
	ControllerVersion string           `json:"controller_version,omitempty"`
	Capabilities      []string         `json:"capabilities"`
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

	if dynClient == nil {
		collectKarpenterEvents(ctx, client, nil, metrics)
		logKarpenterMetrics(metrics)
		return metrics
	}

	nodePools, nodeLabelsOK, nodePoolsErr := collectNodePools(ctx, client, dynClient)
	if nodePoolsErr == nil {
		metrics.NodePools = nodePools
		metrics.Capabilities = append(metrics.Capabilities, "node_pools")
		if nodeLabelsOK {
			metrics.Capabilities = append(metrics.Capabilities, "node_karpenter_labels")
		}
	}

	claims, nodeClaimsErr := collectNodeClaims(ctx, dynClient)
	if nodeClaimsErr == nil {
		metrics.NodeClaims = claims
		metrics.Capabilities = append(metrics.Capabilities, "node_claims")
	}

	if isResourceAbsent(nodePoolsErr) && isResourceAbsent(nodeClaimsErr) {
		// Karpenter is not installed: keep the old envelope and claim no capabilities.
		collectKarpenterEvents(ctx, client, nil, metrics)
		logKarpenterMetrics(metrics)
		return metrics
	}

	pendingRefs := map[string]bool{}
	if pods, refs, podsOK := collectPendingPods(ctx, client); podsOK {
		metrics.PendingPods = pods
		pendingRefs = refs
		metrics.Capabilities = append(metrics.Capabilities, "pending_pods")
	}

	if collectKarpenterEvents(ctx, client, pendingRefs, metrics) {
		metrics.Capabilities = append(metrics.Capabilities, "events")
	}
	metrics.ControllerVersion = collectKarpenterVersion(ctx, client)

	logKarpenterMetrics(metrics)
	return metrics
}

func collectKarpenterEvents(
	ctx context.Context,
	client kubernetes.Interface,
	pendingPodRefs map[string]bool,
	metrics *KarpenterMetrics,
) bool {
	eventList, err := client.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Error("karpenter_events_list_failed", "error", err)
		return false
	}

	var maxRV int64
	for i := range eventList.Items {
		event := &eventList.Items[i]
		if !isKarpenterSource(event) && !isPendingPodSchedulerEvent(event, pendingPodRefs) {
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
			UID:                 string(event.UID),
			Reason:              event.Reason,
			Message:             event.Message,
			InvolvedObject:      event.InvolvedObject.Name,
			ObjectKind:          event.InvolvedObject.Kind,
			Count:               event.Count,
			Namespace:           event.Namespace,
			EventType:           event.Type,
			ReportingController: event.ReportingController,
			Action:              event.Action,
			FirstTimestamp:      formatMetaTime(event.FirstTimestamp),
			LastTimestamp:       formatMetaTime(event.LastTimestamp),
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

func isKarpenterSource(event *corev1.Event) bool {
	return strings.Contains(strings.ToLower(event.Source.Component), "karpenter") ||
		strings.Contains(strings.ToLower(event.ReportingController), "karpenter")
}

func isPendingPodSchedulerEvent(event *corev1.Event, pendingPodRefs map[string]bool) bool {
	source := strings.ToLower(event.Source.Component + " " + event.ReportingController)
	if !strings.Contains(source, "scheduler") || event.InvolvedObject.Kind != "Pod" {
		return false
	}
	uid := string(event.InvolvedObject.UID)
	key := event.InvolvedObject.Namespace + "/" + event.InvolvedObject.Name
	return pendingPodRefs[uid] || pendingPodRefs[key]
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

func collectNodePools(
	ctx context.Context,
	client kubernetes.Interface,
	dynClient dynamic.Interface,
) ([]NodePoolInfo, bool, error) {
	list, err := dynClient.Resource(nodePoolGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("nodepool_list_failed", "error", err)
		return nil, false, err
	}

	nodeCountByPool := make(map[string]int)
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	nodeLabelsOK := err == nil
	if err != nil {
		karpenterLog.Warn("nodepool_node_count_failed", "error", err)
	} else {
		for i := range nodes.Items {
			if pool := nodes.Items[i].Labels["karpenter.sh/nodepool"]; pool != "" {
				nodeCountByPool[pool]++
			}
		}
	}

	nodePools := make([]NodePoolInfo, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		name := item.GetName()
		np := NodePoolInfo{
			Name:               name,
			NodeCount:          nodeCountByPool[name],
			UID:                string(item.GetUID()),
			NodeClassName:      stringAt(item.Object, "spec", "template", "spec", "nodeClassRef", "name"),
			CPULimitMillicores: quantityMilliAt(item.Object, "spec", "limits", "cpu"),
			MemoryLimitBytes:   quantityValueAt(item.Object, "spec", "limits", "memory"),
			NodeLimit:          quantityValueAt(item.Object, "spec", "limits", "nodes"),
			CPUUsageMillicores: quantityMilliAt(item.Object, "status", "resources", "cpu"),
			MemoryUsageBytes:   quantityValueAt(item.Object, "status", "resources", "memory"),
		}
		if weight, ok := int64At(item.Object, "spec", "weight"); ok {
			np.Weight = &weight
		}
		if condition, ok := conditionByType(item.Object, "Ready"); ok {
			if ready, known := conditionBool(condition); known {
				np.Ready = &ready
			}
			np.Reason = stringAt(condition, "reason")
			np.Message = stringAt(condition, "message")
			if generation, ok := int64At(condition, "observedGeneration"); ok {
				np.ObservedGeneration = &generation
			}
		}
		karpenterLog.Info("nodepool_collected", "name", np.Name, "node_count", np.NodeCount)
		nodePools = append(nodePools, np)
	}
	karpenterLog.Info("nodepools_collected", "count", len(nodePools))
	return nodePools, nodeLabelsOK, nil
}

func collectNodeClaims(ctx context.Context, dynClient dynamic.Interface) ([]NodeClaimInfo, error) {
	list, err := dynClient.Resource(nodeClaimGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("nodeclaim_list_failed", "error", err)
		return nil, err
	}

	claims := make([]NodeClaimInfo, 0, len(list.Items))
	for i := range list.Items {
		item := &list.Items[i]
		labels := item.GetLabels()
		claim := NodeClaimInfo{
			UID:           string(item.GetUID()),
			Name:          item.GetName(),
			NodePoolName:  labels["karpenter.sh/nodepool"],
			NodeClassName: stringAt(item.Object, "spec", "nodeClassRef", "name"),
			NodeName:      stringAt(item.Object, "status", "nodeName"),
			ProviderID:    stringAt(item.Object, "status", "providerID"),
			InstanceType:  labels["node.kubernetes.io/instance-type"],
			Zone:          labels["topology.kubernetes.io/zone"],
			CapacityType:  labels["karpenter.sh/capacity-type"],
			CreatedAt:     formatMetaTime(item.GetCreationTimestamp()),
			LaunchedAt:    trueConditionTransition(item.Object, "Launched"),
			RegisteredAt:  trueConditionTransition(item.Object, "Registered"),
			InitializedAt: trueConditionTransition(item.Object, "Initialized"),
			ReadyAt:       trueConditionTransition(item.Object, "Ready"),
		}
		if deletionTime := item.GetDeletionTimestamp(); deletionTime != nil {
			claim.DeletionTimestamp = formatMetaTime(*deletionTime)
		}
		claim.Status, claim.Reason, claim.Message = nodeClaimConditionState(item.Object)
		claims = append(claims, claim)
	}
	karpenterLog.Info("nodeclaims_collected", "count", len(claims))
	return claims, nil
}

func collectPendingPods(
	ctx context.Context,
	client kubernetes.Interface,
) ([]PendingPodInfo, map[string]bool, bool) {
	list, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: "status.phase=Pending"})
	if err != nil {
		karpenterLog.Warn("pending_pods_list_failed", "error", err)
		return nil, nil, false
	}
	var rsToDeploy map[string]string
	pending := make([]PendingPodInfo, 0)
	refs := make(map[string]bool)
	for i := range list.Items {
		pod := &list.Items[i]
		if pod.Status.Phase != corev1.PodPending || pod.Spec.NodeName != "" {
			continue
		}
		if rsToDeploy == nil {
			rsToDeploy = replicaSetToDeploymentMap(ctx, client)
		}
		item := PendingPodInfo{
			UID:       string(pod.UID),
			Namespace: pod.Namespace,
			Name:      pod.Name,
			CreatedAt: formatMetaTime(pod.CreationTimestamp),
		}
		for _, container := range pod.Spec.Containers {
			item.CPURequestMillicores += container.Resources.Requests.Cpu().MilliValue()
			item.MemoryRequestBytes += container.Resources.Requests.Memory().Value()
		}
		if owner := podWorkloadOwner(pod.OwnerReferences, pod.Namespace, rsToDeploy); owner != nil {
			item.OwnerReference = *owner
		}
		for _, condition := range pod.Status.Conditions {
			if condition.Type == corev1.PodScheduled && condition.Status != corev1.ConditionTrue {
				item.ConditionReason = condition.Reason
				item.ConditionMessage = condition.Message
				break
			}
		}
		pending = append(pending, item)
		refs[item.UID] = true
		refs[item.Namespace+"/"+item.Name] = true
	}
	karpenterLog.Info("pending_pods_collected", "count", len(pending))
	return pending, refs, true
}

func collectKarpenterVersion(ctx context.Context, client kubernetes.Interface) string {
	deployments, err := client.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		karpenterLog.Warn("karpenter_version_list_failed", "error", err)
		return ""
	}
	for i := range deployments.Items {
		deployment := &deployments.Items[i]
		if !isKarpenterDeployment(deployment) {
			continue
		}
		if version := deployment.Labels["app.kubernetes.io/version"]; version != "" {
			return version
		}
		for _, container := range deployment.Spec.Template.Spec.Containers {
			if strings.Contains(strings.ToLower(container.Image), "karpenter") {
				return imageVersion(container.Image)
			}
		}
		return ""
	}
	return ""
}

func isKarpenterDeployment(deployment *appsv1.Deployment) bool {
	return strings.EqualFold(deployment.Labels["app.kubernetes.io/name"], "karpenter") ||
		strings.EqualFold(deployment.Name, "karpenter")
}

func imageVersion(image string) string {
	withoutDigest := strings.SplitN(image, "@", 2)[0]
	colon := strings.LastIndex(withoutDigest, ":")
	slash := strings.LastIndex(withoutDigest, "/")
	if colon <= slash {
		return ""
	}
	return strings.TrimPrefix(withoutDigest[colon+1:], "v")
}

func conditionByType(object map[string]any, conditionType string) (map[string]any, bool) {
	conditions, found, _ := unstructured.NestedSlice(object, "status", "conditions")
	if !found {
		return nil, false
	}
	for _, raw := range conditions {
		condition, ok := raw.(map[string]any)
		if ok && stringAt(condition, "type") == conditionType {
			return condition, true
		}
	}
	return nil, false
}

func conditionBool(condition map[string]any) (bool, bool) {
	switch strings.ToLower(stringAt(condition, "status")) {
	case "true":
		return true, true
	case "false":
		return false, true
	default:
		return false, false
	}
}

func trueConditionTransition(object map[string]any, conditionType string) string {
	condition, ok := conditionByType(object, conditionType)
	if !ok || !strings.EqualFold(stringAt(condition, "status"), "true") {
		return ""
	}
	return stringAt(condition, "lastTransitionTime")
}

func nodeClaimConditionState(object map[string]any) (status, reason, message string) {
	for _, conditionType := range []string{"Launched", "Registered", "Initialized", "Ready"} {
		condition, ok := conditionByType(object, conditionType)
		if !ok || strings.EqualFold(stringAt(condition, "status"), "true") {
			continue
		}
		return stringAt(condition, "status"), stringAt(condition, "reason"), stringAt(condition, "message")
	}
	if condition, ok := conditionByType(object, "Ready"); ok {
		return stringAt(condition, "status"), stringAt(condition, "reason"), stringAt(condition, "message")
	}
	return "", "", ""
}

func stringAt(object map[string]any, fields ...string) string {
	value, _, _ := unstructured.NestedString(object, fields...)
	return value
}

func isResourceAbsent(err error) bool {
	return err != nil && (apierrors.IsNotFound(err) ||
		strings.Contains(strings.ToLower(err.Error()), "resource not found"))
}

func int64At(object map[string]any, fields ...string) (int64, bool) {
	value, found, _ := unstructured.NestedFieldNoCopy(object, fields...)
	if !found {
		return 0, false
	}
	switch typed := value.(type) {
	case int64:
		return typed, true
	case int:
		return int64(typed), true
	case float64:
		return int64(typed), true
	default:
		return 0, false
	}
}

func quantityAt(object map[string]any, fields ...string) (resource.Quantity, bool) {
	value, found, _ := unstructured.NestedFieldNoCopy(object, fields...)
	if !found || value == nil {
		return resource.Quantity{}, false
	}
	quantity, err := resource.ParseQuantity(fmt.Sprint(value))
	return quantity, err == nil
}

func quantityMilliAt(object map[string]any, fields ...string) *int64 {
	quantity, ok := quantityAt(object, fields...)
	if !ok {
		return nil
	}
	value := quantity.MilliValue()
	return &value
}

func quantityValueAt(object map[string]any, fields ...string) *int64 {
	quantity, ok := quantityAt(object, fields...)
	if !ok {
		return nil
	}
	value := quantity.Value()
	return &value
}

func formatMetaTime(value metav1.Time) string {
	if value.IsZero() {
		return ""
	}
	return value.UTC().Format(time.RFC3339)
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
		"node_claims", len(metrics.NodeClaims),
		"pending_pods", len(metrics.PendingPods),
		"capabilities", metrics.Capabilities,
	)
	karpenterLog.Info("karpenter_metrics", "payload", json.RawMessage(data))
}
