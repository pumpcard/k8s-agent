package collector

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCollectKarpenter_EnrichedPayload(t *testing.T) {
	now := time.Date(2026, 11, 18, 20, 0, 0, 0, time.UTC)
	nodePool := object(
		"karpenter.sh/v1",
		"NodePool",
		"default",
		map[string]any{
			"metadata": map[string]any{
				"name":       "default",
				"uid":        "pool-uid",
				"generation": int64(3),
			},
			"spec": map[string]any{
				"weight": int64(10),
				"limits": map[string]any{"cpu": "20", "memory": "64Gi", "nodes": "8"},
				"template": map[string]any{"spec": map[string]any{
					"nodeClassRef": map[string]any{
						"group": "karpenter.k8s.aws",
						"kind":  "EC2NodeClass",
						"name":  "default",
					},
				}},
			},
			"status": map[string]any{
				"nodes": int64(2),
				"resources": map[string]any{
					"cpu":    "8",
					"memory": "16Gi",
					"nodes":  "2",
				},
				"conditions": []any{
					map[string]any{
						"type":               "Ready",
						"status":             "True",
						"reason":             "Ready",
						"message":            "",
						"observedGeneration": int64(3),
					},
				},
			},
		},
	)
	nodeClaim := object(
		"karpenter.sh/v1",
		"NodeClaim",
		"default-abc",
		map[string]any{
			"metadata": map[string]any{
				"name":              "default-abc",
				"uid":               "claim-uid",
				"creationTimestamp": now.Add(-2 * time.Minute).Format(time.RFC3339),
				"labels": map[string]any{
					"karpenter.sh/nodepool":            "default",
					"karpenter.sh/capacity-type":       "spot",
					"node.kubernetes.io/instance-type": "m7a.large",
					"topology.kubernetes.io/zone":      "us-west-2a",
				},
			},
			"spec": map[string]any{
				"nodeClassRef": map[string]any{
					"group": "karpenter.k8s.aws",
					"kind":  "EC2NodeClass",
					"name":  "default",
				},
			},
			"status": map[string]any{
				"nodeName":   "node-1",
				"providerID": "aws:///us-west-2a/i-123",
				"conditions": []any{
					condition("Launched", "True", "Launched", now.Add(-90*time.Second)),
					condition("Registered", "True", "Registered", now.Add(-60*time.Second)),
					condition("Initialized", "True", "Initialized", now.Add(-30*time.Second)),
					condition("Ready", "True", "Ready", now.Add(-20*time.Second)),
				},
			},
		},
	)
	listKinds := map[schema.GroupVersionResource]string{
		nodePoolGVR:  "NodePoolList",
		nodeClaimGVR: "NodeClaimList",
	}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		listKinds,
		nodePool,
		nodeClaim,
	)

	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{
		Name: "node-1",
		Labels: map[string]string{
			"karpenter.sh/nodepool":      "default",
			"karpenter.sh/nodeclaim":     "default-abc",
			"karpenter.sh/capacity-type": "spot",
		},
	}}
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:               types.UID("pod-uid"),
			Namespace:         "default",
			Name:              "api",
			CreationTimestamp: metav1.NewTime(now.Add(-time.Minute)),
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "Deployment", Name: "api"},
			},
		},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{
				{
					Name: "api",
					Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
						corev1.ResourceCPU:    resource.MustParse("500m"),
						corev1.ResourceMemory: resource.MustParse("1Gi"),
					}},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{
					Type:    corev1.PodScheduled,
					Status:  corev1.ConditionFalse,
					Reason:  corev1.PodReasonUnschedulable,
					Message: "did not match NodePool requirements",
				},
			},
		},
	}
	schedulerEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			UID:             types.UID("scheduler-event"),
			Namespace:       "default",
			Name:            "api.failed-scheduling",
			ResourceVersion: "10",
		},
		InvolvedObject: corev1.ObjectReference{
			UID:       pending.UID,
			Kind:      "Pod",
			Namespace: pending.Namespace,
			Name:      pending.Name,
		},
		Reason:              "FailedScheduling",
		Message:             "did not match NodePool requirements",
		Type:                corev1.EventTypeWarning,
		Count:               2,
		ReportingController: "default-scheduler",
		LastTimestamp:       metav1.NewTime(now),
	}
	karpenterEvent := &corev1.Event{
		ObjectMeta: metav1.ObjectMeta{
			UID:             types.UID("karpenter-event"),
			Namespace:       "default",
			Name:            "default-abc.disruption-blocked",
			ResourceVersion: "11",
		},
		InvolvedObject: corev1.ObjectReference{
			Kind: "NodeClaim",
			Name: "default-abc",
		},
		Reason:              "DisruptionBlocked",
		Message:             "PDB prevents pod eviction",
		Type:                corev1.EventTypeNormal,
		Count:               3,
		ReportingController: "karpenter",
		FirstTimestamp:      metav1.NewTime(now.Add(-time.Hour)),
		LastTimestamp:       metav1.NewTime(now),
	}
	deployment := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "kube-system",
			Name:      "karpenter",
			Labels:    map[string]string{"app.kubernetes.io/name": "karpenter"},
		},
		Spec: appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "controller", Image: "public.ecr.aws/karpenter/controller:1.9.0@sha256:abc"}},
		}}},
	}
	client := k8sfake.NewSimpleClientset(node, pending, schedulerEvent, karpenterEvent, deployment)
	lastSeenResourceVersion = 0

	metrics := CollectKarpenter(context.Background(), client, dynClient, "cluster-1")

	assertStrings(t, metrics.Capabilities, []string{
		"node_pools", "node_karpenter_labels", "node_claims", "pending_pods", "events",
	})
	if metrics.ControllerVersion != "1.9.0" {
		t.Errorf("controller version = %q, want 1.9.0", metrics.ControllerVersion)
	}
	if len(metrics.NodePools) != 1 {
		t.Fatalf("node pools = %d, want 1", len(metrics.NodePools))
	}
	pool := metrics.NodePools[0]
	if pool.Ready == nil || !*pool.Ready || pool.NodeClassName != "default" || pool.NodeCount != 1 {
		t.Errorf("node pool = %+v", pool)
	}
	if pool.CPULimitMillicores == nil || *pool.CPULimitMillicores != 20_000 {
		t.Errorf("cpu limit = %v, want 20000m", pool.CPULimitMillicores)
	}
	if pool.CPUUsageMillicores == nil || *pool.CPUUsageMillicores != 8_000 {
		t.Errorf("cpu usage = %v, want 8000m", pool.CPUUsageMillicores)
	}

	if len(metrics.NodeClaims) != 1 {
		t.Fatalf("node claims = %d, want 1", len(metrics.NodeClaims))
	}
	claim := metrics.NodeClaims[0]
	if claim.NodePoolName != "default" || claim.NodeClassName != "default" || claim.ReadyAt == "" {
		t.Errorf("node claim = %+v", claim)
	}

	if len(metrics.PendingPods) != 1 {
		t.Fatalf("pending pods = %d, want 1", len(metrics.PendingPods))
	}
	pod := metrics.PendingPods[0]
	if pod.OwnerReference != "Deployment/api" || pod.CPURequestMillicores != 500 ||
		pod.MemoryRequestBytes != bytesOf("1Gi") {
		t.Errorf("pending pod = %+v", pod)
	}
	if len(metrics.Events) != 2 {
		t.Fatalf("events = %d, want 2", len(metrics.Events))
	}
	if metrics.Events[0].ReportingController == "" || metrics.Events[0].LastTimestamp == "" ||
		metrics.Events[0].EventType != corev1.EventTypeWarning {
		t.Errorf("event additions were not populated: %+v", metrics.Events[0])
	}

	data, err := json.Marshal(metrics)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{
		`"node_claims"`, `"pending_pods"`, `"capabilities"`, `"controller_version"`,
		`"node_class_name"`, `"condition_message"`, `"last_timestamp"`,
	} {
		if !jsonContains(data, field) {
			t.Errorf("payload missing %s: %s", field, data)
		}
	}

	if again := CollectKarpenter(context.Background(), client, dynClient, "cluster-1"); len(again.Events) != 0 {
		t.Errorf("already-sent events were resent: %+v", again.Events)
	}
}

func TestCollectKarpenter_NoDynamicClientPreservesOldEnvelope(t *testing.T) {
	metrics := CollectKarpenter(context.Background(), k8sfake.NewSimpleClientset(), nil, "cluster-1")

	if len(metrics.Capabilities) != 0 || len(metrics.NodeClaims) != 0 || len(metrics.PendingPods) != 0 {
		t.Errorf("unexpected enriched telemetry without dynamic client: %+v", metrics)
	}
}

func TestCollectKarpenter_NodePoolFailureKeepsIndependentTelemetry(t *testing.T) {
	nodeClaim := object(
		"karpenter.sh/v1",
		"NodeClaim",
		"default-abc",
		map[string]any{"metadata": map[string]any{"name": "default-abc", "uid": "claim-uid"}},
	)
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			nodePoolGVR:  "NodePoolList",
			nodeClaimGVR: "NodeClaimList",
		},
		nodeClaim,
	)
	dynClient.PrependReactor("list", "nodepools", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "karpenter.sh", Resource: "nodepools"},
			"",
			nil,
		)
	})
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{UID: "pod-uid", Namespace: "default", Name: "api"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}

	metrics := CollectKarpenter(
		context.Background(),
		k8sfake.NewSimpleClientset(pending),
		dynClient,
		"cluster-1",
	)

	assertStrings(t, metrics.Capabilities, []string{"node_claims", "pending_pods", "events"})
	if len(metrics.NodeClaims) != 1 || len(metrics.PendingPods) != 1 {
		t.Errorf("independent telemetry was suppressed: %+v", metrics)
	}
}

func TestCollectKarpenter_TransientCoreAPIFailuresKeepIndependentTelemetry(t *testing.T) {
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{
			nodePoolGVR:  "NodePoolList",
			nodeClaimGVR: "NodeClaimList",
		},
	)
	dynClient.PrependReactor("list", "nodepools", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "karpenter.sh", Resource: "nodepools"},
			"",
			nil,
		)
	})
	dynClient.PrependReactor("list", "nodeclaims", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			schema.GroupResource{Group: "karpenter.sh", Resource: "nodeclaims"},
			"",
			nil,
		)
	})
	pending := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{UID: "pod-uid", Namespace: "default", Name: "api"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "api"}}},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}

	metrics := CollectKarpenter(
		context.Background(),
		k8sfake.NewSimpleClientset(pending),
		dynClient,
		"cluster-1",
	)

	assertStrings(t, metrics.Capabilities, []string{"pending_pods", "events"})
	if len(metrics.PendingPods) != 1 {
		t.Errorf("pending pods were suppressed: %+v", metrics)
	}
}

func object(apiVersion, kind, name string, fields map[string]any) *unstructured.Unstructured {
	fields["apiVersion"] = apiVersion
	fields["kind"] = kind
	metadata, _ := fields["metadata"].(map[string]any)
	if metadata == nil {
		metadata = map[string]any{}
		fields["metadata"] = metadata
	}
	metadata["name"] = name
	return &unstructured.Unstructured{Object: fields}
}

func condition(conditionType, status, reason string, transition time.Time) map[string]any {
	return map[string]any{
		"type":               conditionType,
		"status":             status,
		"reason":             reason,
		"lastTransitionTime": transition.Format(time.RFC3339),
	}
}

func assertStrings(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("strings = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("strings = %v, want %v", got, want)
		}
	}
}

func jsonContains(data []byte, field string) bool {
	return strings.Contains(string(data), field)
}
