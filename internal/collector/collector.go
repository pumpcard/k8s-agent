package collector

import (
	"context"
	"encoding/json"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClusterMetricsPayload struct {
	Timestamp      string         `json:"timestamp"`
	ClusterID      string         `json:"cluster_id,omitempty"`
	CustomerID     string         `json:"customer_id,omitempty"`
	CollectionMode string         `json:"collection_mode"`
	ClusterHealth  *ClusterHealth `json:"cluster_health,omitempty"`
	Summary        ClusterSummary `json:"summary"`
	Nodes          []NodeMetrics  `json:"nodes"`
	Pods           []PodSummary   `json:"pods"`
}

type ClusterHealth struct {
	TotalNodes                    int     `json:"total_nodes"`
	ReadyNodes                    int     `json:"ready_nodes"`
	NotReadyNodes                 int     `json:"not_ready_nodes"`
	OverallStatus                 string  `json:"overall_status"`
	AvgCPUUtilizationPercent      float64 `json:"avg_cpu_utilization_percent"`
	AvgMemoryUtilizationPercent   float64 `json:"avg_memory_utilization_percent"`
	TotalCPUUsageMillicores       int64   `json:"total_cpu_usage_millicores"`
	TotalMemoryUsageBytes         int64   `json:"total_memory_usage_bytes"`
	TotalCPUCapacityMillicores    int64   `json:"total_cpu_capacity_millicores"`
	TotalMemoryCapacityBytes      int64   `json:"total_memory_capacity_bytes"`
	TotalCPUAllocatableMillicores int64   `json:"total_cpu_allocatable_millicores"`
	TotalMemoryAllocatableBytes   int64   `json:"total_memory_allocatable_bytes"`
}

type ClusterSummary struct {
	TotalPods     int `json:"total_pods"`
	RunningPods   int `json:"running_pods"`
	PendingPods   int `json:"pending_pods"`
	FailedPods    int `json:"failed_pods"`
	SucceededPods int `json:"succeeded_pods"`
}

type NodeMetrics struct {
	Name           string           `json:"name"`
	Architecture   string           `json:"architecture"`
	KubeletVersion string           `json:"kubelet_version"`
	OSImage        string           `json:"os_image"`
	Capacity       *NodeResources   `json:"capacity,omitempty"`
	Allocatable    *NodeResources   `json:"allocatable,omitempty"`
	Usage          *NodeResources   `json:"usage,omitempty"` // 0 when no metrics-server
	Utilization    *NodeUtilization `json:"utilization,omitempty"`
	Health         *NodeHealth      `json:"health,omitempty"`
}

type NodeResources struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
}

type NodeUtilization struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	Status        string  `json:"status"`
}

type NodeHealth struct {
	Ready              bool `json:"ready"`
	MemoryPressure     bool `json:"memory_pressure"`
	DiskPressure       bool `json:"disk_pressure"`
	PIDPressure        bool `json:"pid_pressure"`
	NetworkUnavailable bool `json:"network_unavailable"`
}

type PodSummary struct {
	Namespace      string          `json:"namespace"`
	Name           string          `json:"name"`
	NodeName       string          `json:"node_name"`
	Phase          string          `json:"phase"`
	QOSClass       string          `json:"qos_class"`
	Ready          bool            `json:"ready"`
	RestartCount   int             `json:"restart_count"`
	StartTime      *string         `json:"start_time,omitempty"` // RFC3339 or empty
	OwnerReference string          `json:"owner_reference,omitempty"`
	ContainerCount int             `json:"container_count"`
	Requests       *PodResources   `json:"requests,omitempty"`
	Limits         *PodResources   `json:"limits,omitempty"`
	Usage          *PodResources   `json:"usage,omitempty"` // nil when no metrics-server
	Utilization    *PodUtilization `json:"utilization,omitempty"`
	Labels         string          `json:"labels"` // JSON object string
}

type PodResources struct {
	CPUMillicores int64 `json:"cpu_millicores"`
	MemoryBytes   int64 `json:"memory_bytes"`
}

type PodUtilization struct {
	CPUPercent    *float64 `json:"cpu_percent,omitempty"`
	MemoryPercent *float64 `json:"memory_percent,omitempty"`
	Status        string   `json:"status,omitempty"`
}

func quantityToMilli(q resource.Quantity) int64 {
	return q.MilliValue()
}

func quantityToBytes(q resource.Quantity) int64 {
	return q.Value()
}

func nodeConditionStatus(conditions []corev1.NodeCondition, t corev1.NodeConditionType) bool {
	for _, c := range conditions {
		if c.Type == t {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func nodeStatusFromReady(conditions []corev1.NodeCondition) string {
	for _, c := range conditions {
		if c.Type == corev1.NodeReady {
			if c.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

func podReady(conditions []corev1.PodCondition) bool {
	for _, c := range conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podOwnerRef(owners []metav1.OwnerReference) string {
	if len(owners) == 0 {
		return ""
	}
	o := owners[0]
	return o.Kind + "/" + o.Name
}

func Collect(ctx context.Context, client *kubernetes.Clientset, clusterID, customerID string) ClusterMetricsPayload {
	ts := time.Now().UTC().Format(time.RFC3339)
	empty := ClusterMetricsPayload{Timestamp: ts}

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return empty
	}

	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return empty
	}

	var nodeList []NodeMetrics
	var totalCapCPU, totalCapMem, totalAllocCPU, totalAllocMem int64
	readyNodes := 0
	for _, n := range nodes.Items {
		capacity := n.Status.Capacity
		allocatable := n.Status.Allocatable
		capCPU := quantityToMilli(capacity[corev1.ResourceCPU])
		capMem := quantityToBytes(capacity[corev1.ResourceMemory])
		allocCPU := quantityToMilli(allocatable[corev1.ResourceCPU])
		allocMem := quantityToBytes(allocatable[corev1.ResourceMemory])
		totalCapCPU += capCPU
		totalCapMem += capMem
		totalAllocCPU += allocCPU
		totalAllocMem += allocMem
		if nodeConditionStatus(n.Status.Conditions, corev1.NodeReady) {
			readyNodes++
		}

		ni := n.Status.NodeInfo
		nodeList = append(nodeList, NodeMetrics{
			Name:           n.Name,
			Architecture:   ni.Architecture,
			KubeletVersion: ni.KubeletVersion,
			OSImage:        ni.OSImage,
			Capacity: &NodeResources{
				CPUMillicores: capCPU,
				MemoryBytes:   capMem,
			},
			Allocatable: &NodeResources{
				CPUMillicores: allocCPU,
				MemoryBytes:   allocMem,
			},
			Usage: &NodeResources{0, 0}, // no metrics-server in agent
			Utilization: &NodeUtilization{
				CPUPercent:    0,
				MemoryPercent: 0,
				Status:        nodeStatusFromReady(n.Status.Conditions),
			},
			Health: &NodeHealth{
				Ready:              nodeConditionStatus(n.Status.Conditions, corev1.NodeReady),
				MemoryPressure:     nodeConditionStatus(n.Status.Conditions, corev1.NodeMemoryPressure),
				DiskPressure:       nodeConditionStatus(n.Status.Conditions, corev1.NodeDiskPressure),
				PIDPressure:        nodeConditionStatus(n.Status.Conditions, corev1.NodePIDPressure),
				NetworkUnavailable: nodeConditionStatus(n.Status.Conditions, corev1.NodeNetworkUnavailable),
			},
		})
	}

	overallStatus := "Healthy"
	if readyNodes < len(nodes.Items) {
		overallStatus = "Degraded"
	}
	if len(nodes.Items) == 0 {
		overallStatus = "Unknown"
	}

	clusterHealth := &ClusterHealth{
		TotalNodes:                    len(nodes.Items),
		ReadyNodes:                    readyNodes,
		NotReadyNodes:                 len(nodes.Items) - readyNodes,
		OverallStatus:                 overallStatus,
		TotalCPUCapacityMillicores:    totalCapCPU,
		TotalMemoryCapacityBytes:      totalCapMem,
		TotalCPUAllocatableMillicores: totalAllocCPU,
		TotalMemoryAllocatableBytes:   totalAllocMem,
	}

	var running, pending, failed, succeeded int
	podList := make([]PodSummary, 0, len(pods.Items))
	for _, p := range pods.Items {
		switch p.Status.Phase {
		case corev1.PodRunning:
			running++
		case corev1.PodPending:
			pending++
		case corev1.PodFailed:
			failed++
		case corev1.PodSucceeded:
			succeeded++
		}

		restartCount := 0
		for _, cs := range p.Status.ContainerStatuses {
			restartCount += int(cs.RestartCount)
		}
		var startTime *string
		if p.Status.StartTime != nil {
			s := p.Status.StartTime.Format(time.RFC3339)
			startTime = &s
		}
		qos := string(p.Status.QOSClass)
		if qos == "" {
			qos = "BestEffort"
		}
		reqCPU, reqMem := sumContainerRequestsLimits(p.Spec.Containers, false)
		limCPU, limMem := sumContainerRequestsLimits(p.Spec.Containers, true)
		phase := string(p.Status.Phase)
		if phase == "" {
			phase = "Unknown"
		}
		labelsJSON := "{}"
		if len(p.Labels) > 0 {
			b, _ := json.Marshal(p.Labels)
			labelsJSON = string(b)
		}
		podList = append(podList, PodSummary{
			Namespace:      p.Namespace,
			Name:           p.Name,
			NodeName:       p.Spec.NodeName,
			Phase:          phase,
			QOSClass:       qos,
			Ready:          podReady(p.Status.Conditions),
			RestartCount:   restartCount,
			StartTime:      startTime,
			OwnerReference: podOwnerRef(p.OwnerReferences),
			ContainerCount: len(p.Spec.Containers),
			Requests:       &PodResources{CPUMillicores: reqCPU, MemoryBytes: reqMem},
			Limits:         &PodResources{CPUMillicores: limCPU, MemoryBytes: limMem},
			Usage:          nil,
			Utilization:    nil,
			Labels:         labelsJSON,
		})
	}

	return ClusterMetricsPayload{
		Timestamp:      ts,
		ClusterID:      clusterID,
		CustomerID:     customerID,
		CollectionMode: "cluster",
		ClusterHealth:  clusterHealth,
		Summary: ClusterSummary{
			TotalPods:     len(pods.Items),
			RunningPods:   running,
			PendingPods:   pending,
			FailedPods:    failed,
			SucceededPods: succeeded,
		},
		Nodes: nodeList,
		Pods:  podList,
	}
}

func sumContainerRequestsLimits(containers []corev1.Container, useLimits bool) (cpuMilli, memBytes int64) {
	for _, c := range containers {
		var rl corev1.ResourceList
		if useLimits {
			rl = c.Resources.Limits
		} else {
			rl = c.Resources.Requests
		}
		cpuMilli += quantityToMilli(rl[corev1.ResourceCPU])
		memBytes += quantityToBytes(rl[corev1.ResourceMemory])
	}
	return cpuMilli, memBytes
}
