package collector

import (
	"context"
	"errors"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestCollectKarpenter(t *testing.T) {
	nodePool := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata":   map[string]any{"name": "default"},
		"spec":       map[string]any{"limits": map[string]any{"cpu": "20", "memory": "64Gi"}},
		"status": map[string]any{
			"resources": map[string]any{"cpu": "19", "memory": "16Gi"},
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "False", "reason": "NodeClassNotReady", "message": "not ready"},
			},
		},
	}}
	nodeClaim := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodeClaim",
		"metadata":   map[string]any{"name": "default-abc", "uid": "claim-uid", "creationTimestamp": "2026-11-18T19:59:00Z"},
		"status": map[string]any{
			"conditions": []any{
				map[string]any{"type": "Launched", "status": "True", "lastTransitionTime": "2026-11-18T20:00:00Z"},
				map[string]any{"type": "Registered", "status": "False", "reason": "Pending", "message": "waiting"},
			},
		},
	}}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{nodePoolGVR: "NodePoolList", nodeClaimGVR: "NodeClaimList"},
		nodePool,
		nodeClaim,
	)
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID:               "pod-uid",
			Namespace:         "default",
			Name:              "api",
			CreationTimestamp: metav1.NewTime(time.Date(2026, 11, 18, 19, 58, 0, 0, time.UTC)),
		},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "a", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("500m"), corev1.ResourceMemory: resource.MustParse("1Gi"),
			}}},
			{Name: "b", Resources: corev1.ResourceRequirements{Requests: corev1.ResourceList{
				corev1.ResourceCPU: resource.MustParse("250m"),
			}}},
		}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodScheduled, Status: corev1.ConditionFalse, Reason: "Unschedulable", Message: "no nodes"},
			},
		},
	}
	deleting := metav1.Now()
	terminating := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			UID: "terminating-uid", Namespace: "default", Name: "old", DeletionTimestamp: &deleting, Finalizers: []string{"x"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodPending},
	}

	metrics := CollectKarpenter(context.Background(), k8sfake.NewSimpleClientset(pod, terminating), dynClient, "cluster-1")

	want := []string{"events", "node_pools", "node_karpenter_labels", "node_claims", "pending_pods"}
	if len(metrics.Capabilities) != len(want) {
		t.Fatalf("capabilities = %v, want %v", metrics.Capabilities, want)
	}
	for i := range want {
		if metrics.Capabilities[i] != want[i] {
			t.Fatalf("capabilities = %v, want %v", metrics.Capabilities, want)
		}
	}

	pool := metrics.NodePools[0]
	if pool.Ready == nil || *pool.Ready || pool.Reason != "NodeClassNotReady" ||
		*pool.CPULimitMillicores != 20_000 || *pool.CPUUsageMillicores != 19_000 || pool.NodeLimit != nil {
		t.Errorf("node pool = %+v", pool)
	}

	claim := metrics.NodeClaims[0]
	if claim.UID != "claim-uid" || claim.CreatedAt != "2026-11-18T19:59:00Z" || claim.LaunchedAt != "2026-11-18T20:00:00Z" ||
		claim.RegisteredAt != "" || claim.ReadyAt != "" || claim.Reason != "Pending" || claim.Message != "waiting" {
		t.Errorf("node claim = %+v", claim)
	}

	if len(metrics.PendingPods) != 1 {
		t.Fatalf("pending pods = %+v, want only the non-terminating pod", metrics.PendingPods)
	}
	pending := metrics.PendingPods[0]
	if pending.UID != "pod-uid" || pending.CreatedAt != "2026-11-18T19:58:00Z" || pending.ConditionReason != "Unschedulable" || pending.ConditionMessage != "no nodes" ||
		pending.CPURequestMillicores != 750 || pending.MemoryRequestBytes != 1<<30 {
		t.Errorf("pending pod = %+v", pending)
	}
}

func TestCollectKarpenter_NodePoolFailureKeepsNodeClaimsAndPendingPods(t *testing.T) {
	nodeClaim := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodeClaim",
		"metadata":   map[string]any{"name": "default-abc", "uid": "claim-uid"},
	}}
	dynClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(
		runtime.NewScheme(),
		map[schema.GroupVersionResource]string{nodePoolGVR: "NodePoolList", nodeClaimGVR: "NodeClaimList"},
		nodeClaim,
	)
	dynClient.PrependReactor("list", "nodepools", func(k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, errors.New("temporarily unavailable")
	})
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{UID: "pod-uid", Namespace: "default", Name: "api"},
		Status:     corev1.PodStatus{Phase: corev1.PodPending},
	}

	metrics := CollectKarpenter(context.Background(), k8sfake.NewSimpleClientset(pod), dynClient, "cluster-1")

	want := []string{"events", "node_claims", "pending_pods"}
	if len(metrics.Capabilities) != len(want) {
		t.Fatalf("capabilities = %v, want %v", metrics.Capabilities, want)
	}
	for i := range want {
		if metrics.Capabilities[i] != want[i] {
			t.Fatalf("capabilities = %v, want %v", metrics.Capabilities, want)
		}
	}
	if len(metrics.NodeClaims) != 1 || len(metrics.PendingPods) != 1 {
		t.Errorf("metrics = %+v", metrics)
	}
}

func TestCollectKarpenter_WithoutKarpenter(t *testing.T) {
	metrics := CollectKarpenter(context.Background(), k8sfake.NewSimpleClientset(), nil, "cluster-1")

	if len(metrics.Capabilities) != 1 || metrics.Capabilities[0] != "events" ||
		metrics.NodeClaims == nil || metrics.PendingPods == nil {
		t.Errorf("metrics = %+v", metrics)
	}
}
