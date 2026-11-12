package collector

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	k8sfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
	metricsv1beta1 "k8s.io/metrics/pkg/apis/metrics/v1beta1"
	metricsfake "k8s.io/metrics/pkg/client/clientset/versioned/fake"
)

func quantity(s string) resource.Quantity {
	return resource.MustParse(s)
}

func bytesOf(s string) int64 {
	q := resource.MustParse(s)
	return q.Value()
}

func newNode(name string) *corev1.Node {
	return &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Status: corev1.NodeStatus{
			Capacity: corev1.ResourceList{
				corev1.ResourceCPU:    quantity("4"),
				corev1.ResourceMemory: quantity("8Gi"),
			},
			Allocatable: corev1.ResourceList{
				corev1.ResourceCPU:    quantity("4"),
				corev1.ResourceMemory: quantity("8Gi"),
			},
		},
	}
}

// newPod builds a pod with one init container, one app container (with a request/limit)
// and one sidecar container (no limit), matching the shapes M1's per-container collection
// needs to tell apart.
func newPod(namespace, name, node string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Spec: corev1.PodSpec{
			NodeName: node,
			InitContainers: []corev1.Container{
				{
					Name: "migrate",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: quantity("50m"), corev1.ResourceMemory: quantity("64Mi")},
						Limits:   corev1.ResourceList{corev1.ResourceCPU: quantity("100m"), corev1.ResourceMemory: quantity("128Mi")},
					},
				},
			},
			Containers: []corev1.Container{
				{
					Name: "app",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: quantity("500m"), corev1.ResourceMemory: quantity("1Gi")},
						Limits:   corev1.ResourceList{corev1.ResourceCPU: quantity("1"), corev1.ResourceMemory: quantity("2Gi")},
					},
				},
				{
					Name: "sidecar",
					Resources: corev1.ResourceRequirements{
						Requests: corev1.ResourceList{corev1.ResourceCPU: quantity("10m"), corev1.ResourceMemory: quantity("32Mi")},
						// No limit set, on purpose: a sidecar without a limit must stay that way.
					},
				},
			},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodRunning,
			InitContainerStatuses: []corev1.ContainerStatus{
				{Name: "migrate", State: corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{ExitCode: 0}}},
			},
			ContainerStatuses: []corev1.ContainerStatus{
				{Name: "app", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
				{Name: "sidecar", Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			},
		},
	}
}

func newPodMetrics(namespace, name string, containerUsage map[string][2]string) metricsv1beta1.PodMetrics {
	containers := make([]metricsv1beta1.ContainerMetrics, 0, len(containerUsage))
	for containerName, usage := range containerUsage {
		containers = append(containers, metricsv1beta1.ContainerMetrics{
			Name: containerName,
			Usage: corev1.ResourceList{
				corev1.ResourceCPU:    quantity(usage[0]),
				corev1.ResourceMemory: quantity(usage[1]),
			},
		})
	}
	return metricsv1beta1.PodMetrics{
		ObjectMeta: metav1.ObjectMeta{Namespace: namespace, Name: name},
		Containers: containers,
	}
}

// newMetricsClient returns a fake metrics clientset that serves the given pod metrics on List.
// metricsfake.NewSimpleClientset(objects...) can't be used directly here: its generated fake client
// lists pod metrics under the hardcoded resource "pods" (to match the real API), while the generic
// ObjectTracker.Add used by NewSimpleClientset registers objects under the pluralized kind
// ("podmetricses") instead, so the two never find each other. A reactor on "pods" sidesteps that.
func newMetricsClient(pods ...metricsv1beta1.PodMetrics) *metricsfake.Clientset {
	client := metricsfake.NewSimpleClientset()
	client.PrependReactor("list", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, &metricsv1beta1.PodMetricsList{Items: pods}, nil
	})
	return client
}

func containerByName(containers []ContainerInfo, name string) *ContainerInfo {
	for i := range containers {
		if containers[i].Name == name {
			return &containers[i]
		}
	}
	return nil
}

func TestCollect_PerContainerRequestsLimitsUsage(t *testing.T) {
	ctx := context.Background()
	node := newNode("node-1")
	pod := newPod("default", "web-0", "node-1")

	k8sClient := k8sfake.NewSimpleClientset(node, pod)
	metricsClient := newMetricsClient(
		newPodMetrics("default", "web-0", map[string][2]string{
			"app":     {"420m", "900Mi"},
			"sidecar": {"5m", "20Mi"},
		}),
	)

	payload := Collect(ctx, k8sClient, "cluster-1", metricsClient)
	if len(payload.Nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(payload.Nodes))
	}
	pods := payload.Nodes[0].Pods
	if len(pods) != 1 {
		t.Fatalf("expected 1 pod, got %d", len(pods))
	}
	podSummary := pods[0]

	app := containerByName(podSummary.Containers, "app")
	if app == nil {
		t.Fatal("expected an 'app' container")
	}
	if app.Requests.CPUMillicores != 500 || app.Requests.MemoryBytes != bytesOf("1Gi") {
		t.Errorf("app requests = %+v, want 500m/1Gi", app.Requests)
	}
	if app.Limits.CPUMillicores != 1000 || app.Limits.MemoryBytes != bytesOf("2Gi") {
		t.Errorf("app limits = %+v, want 1/2Gi", app.Limits)
	}
	if app.Usage == nil || app.Usage.CPUMillicores != 420 || app.Usage.MemoryBytes != bytesOf("900Mi") {
		t.Errorf("app usage = %+v, want 420m/900Mi", app.Usage)
	}

	sidecar := containerByName(podSummary.Containers, "sidecar")
	if sidecar == nil {
		t.Fatal("expected a 'sidecar' container")
	}
	if sidecar.Limits.CPUMillicores != 0 || sidecar.Limits.MemoryBytes != 0 {
		t.Errorf("sidecar limits = %+v, want zero (no limit set)", sidecar.Limits)
	}
	if sidecar.Usage == nil || sidecar.Usage.CPUMillicores != 5 {
		t.Errorf("sidecar usage = %+v, want 5m", sidecar.Usage)
	}

	if len(podSummary.InitContainers) != 1 {
		t.Fatalf("expected 1 init container, got %d", len(podSummary.InitContainers))
	}
	initContainer := podSummary.InitContainers[0]
	if initContainer.Name != "migrate" {
		t.Errorf("init container name = %q, want migrate", initContainer.Name)
	}
	if initContainer.Requests.CPUMillicores != 50 || initContainer.Limits.CPUMillicores != 100 {
		t.Errorf("init container requests/limits = %+v/%+v, want 50m/100m", initContainer.Requests, initContainer.Limits)
	}
	if initContainer.Usage != nil {
		t.Errorf("init container usage = %+v, want nil (completed, not in metrics-server)", initContainer.Usage)
	}

	// Pod-level usage must still be the sum across containers, for callers reading only the pod total.
	if podSummary.Usage == nil || podSummary.Usage.CPUMillicores != 425 {
		t.Errorf("pod usage = %+v, want 425m (420+5)", podSummary.Usage)
	}
}

func TestCollect_NoMetricsClient_NoUsage(t *testing.T) {
	ctx := context.Background()
	node := newNode("node-1")
	pod := newPod("default", "web-0", "node-1")
	k8sClient := k8sfake.NewSimpleClientset(node, pod)

	payload := Collect(ctx, k8sClient, "cluster-1", nil)
	podSummary := payload.Nodes[0].Pods[0]
	if podSummary.Usage != nil {
		t.Errorf("pod usage = %+v, want nil with no metrics client", podSummary.Usage)
	}
	app := containerByName(podSummary.Containers, "app")
	if app == nil || app.Usage != nil {
		t.Errorf("app usage = %+v, want nil with no metrics client", app)
	}
	// Requests/limits come from the pod spec, not metrics-server, so they're still populated.
	if app.Requests.CPUMillicores != 500 {
		t.Errorf("app requests = %+v, want 500m even with no metrics client", app.Requests)
	}
}
