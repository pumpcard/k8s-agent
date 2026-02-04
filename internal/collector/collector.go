package collector

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

type ClusterMetricsPayload struct {
	Timestamp      string         `json:"timestamp"`
	CollectionMode string         `json:"collection_mode"`
	ClusterHealth  *ClusterHealth `json:"cluster_health"`
	Summary        ClusterSummary `json:"summary"`
	Nodes          []NodeMetrics  `json:"nodes"`
}

type ClusterHealth struct {
	TotalNodes                  int     `json:"total_nodes"`
	ReadyNodes                  int     `json:"ready_nodes"`
	NotReadyNodes               int     `json:"not_ready_nodes"`
	OverallStatus               string  `json:"overall_status"` // healthy | warning | degraded
	AvgCPUUtilizationPercent    float64 `json:"avg_cpu_utilization_percent"`
	AvgMemoryUtilizationPercent float64 `json:"avg_memory_utilization_percent"`
	TotalCPUUsage               string  `json:"total_cpu_usage"`    // Kubernetes quantity format
	TotalMemoryUsage            string  `json:"total_memory_usage"` // Kubernetes quantity format
	TotalCPUCapacity            string  `json:"total_cpu_capacity"`
	TotalMemoryCapacity         string  `json:"total_memory_capacity"`
}

type ClusterSummary struct {
	TotalPods     int `json:"total_pods"`
	RunningPods   int `json:"running_pods"`
	PendingPods   int `json:"pending_pods"`
	FailedPods    int `json:"failed_pods"`
	SucceededPods int `json:"succeeded_pods"`
}

// ResourceMetrics matches API: both string quantities and numeric fields required.
type ResourceMetrics struct {
	CPU           string `json:"cpu"`    // Kubernetes quantity e.g. "4", "3920m"
	Memory        string `json:"memory"` // Kubernetes quantity e.g. "16Gi", "8Mi"
	CPUMillicores int64  `json:"cpu_millicores"`
	MemoryBytes   int64  `json:"memory_bytes"`
}

type NodeCondition struct {
	Type    string  `json:"type"`
	Status  string  `json:"status"`
	Reason  *string `json:"reason,omitempty"`
	Message *string `json:"message,omitempty"`
}

type NodeMetrics struct {
	Name           string             `json:"name"`
	Architecture   string             `json:"architecture"`
	KubeletVersion string             `json:"kubelet_version"`
	OSImage        string             `json:"os_image"`
	Capacity       ResourceMetrics    `json:"capacity"`
	Allocatable    ResourceMetrics    `json:"allocatable"`
	Usage          ResourceMetrics    `json:"usage"`
	Utilization    UtilizationMetrics `json:"utilization"`
	Health         NodeHealth         `json:"health"`
	Conditions     []NodeCondition    `json:"conditions"`
	Pods           []PodSummary       `json:"pods"`
}

// UtilizationMetrics status: healthy | warning | critical | unknown
type UtilizationMetrics struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	Status        string  `json:"status"`
}

type NodeHealth struct {
	Ready              bool   `json:"ready"`
	ReadyStatus        string `json:"ready_status"`
	MemoryPressure     bool   `json:"memory_pressure"`
	DiskPressure       bool   `json:"disk_pressure"`
	PIDPressure        bool   `json:"pid_pressure"`
	NetworkUnavailable bool   `json:"network_unavailable"`
}

type ContainerInfo struct {
	Name         string  `json:"name"`
	Image        string  `json:"image"`
	Ready        bool    `json:"ready"`
	RestartCount int     `json:"restart_count"`
	State        string  `json:"state"` // running | waiting | terminated | pending
	Reason       *string `json:"reason,omitempty"`
	Message      *string `json:"message,omitempty"`
}

type PodSummary struct {
	Namespace      string              `json:"namespace"`
	Name           string              `json:"name"`
	Node           string              `json:"node"`
	Phase          string              `json:"phase"`     // Pending | Running | Succeeded | Failed | Unknown
	QOSClass       string              `json:"qos_class"` // Guaranteed | Burstable | BestEffort
	Ready          bool                `json:"ready"`
	RestartCount   int                 `json:"restart_count"`
	StartTime      *string             `json:"start_time,omitempty"`
	Containers     []ContainerInfo     `json:"containers"`
	Requests       ResourceMetrics     `json:"requests"`
	Limits         ResourceMetrics     `json:"limits"`
	Usage          *ResourceMetrics    `json:"usage,omitempty"`
	Utilization    *UtilizationMetrics `json:"utilization,omitempty"`
	Labels         map[string]string   `json:"labels,omitempty"`
	OwnerReference *string             `json:"owner_reference,omitempty"`
}

func quantityToMilli(q resource.Quantity) int64 {
	return q.MilliValue()
}

func quantityToBytes(q resource.Quantity) int64 {
	return q.Value()
}

// quantityToString returns Kubernetes quantity format string (e.g. "4", "3920m", "16Gi").
func quantityToString(q resource.Quantity) string {
	return q.String()
}

func resourceMetricsFromQuantities(cpu resource.Quantity, mem resource.Quantity) ResourceMetrics {
	return ResourceMetrics{
		CPU:           quantityToString(cpu),
		Memory:        quantityToString(mem),
		CPUMillicores: quantityToMilli(cpu),
		MemoryBytes:   quantityToBytes(mem),
	}
}

func nodeConditionStatus(conditions []corev1.NodeCondition, t corev1.NodeConditionType) bool {
	for _, c := range conditions {
		if c.Type == t {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func nodeReadyStatus(conditions []corev1.NodeCondition) string {
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

func nodeConditionsFromK8s(conditions []corev1.NodeCondition) []NodeCondition {
	out := make([]NodeCondition, 0, len(conditions))
	for _, c := range conditions {
		nc := NodeCondition{
			Type:   string(c.Type),
			Status: string(c.Status),
		}
		if c.Reason != "" {
			nc.Reason = &c.Reason
		}
		if c.Message != "" {
			nc.Message = &c.Message
		}
		out = append(out, nc)
	}
	return out
}

func podReady(conditions []corev1.PodCondition) bool {
	for _, c := range conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podOwnerRef(owners []metav1.OwnerReference) *string {
	if len(owners) == 0 {
		return nil
	}
	s := owners[0].Kind + "/" + owners[0].Name
	return &s
}

func containerState(cs *corev1.ContainerState) string {
	if cs == nil {
		return "pending"
	}
	if cs.Running != nil {
		return "running"
	}
	if cs.Waiting != nil {
		return "waiting"
	}
	if cs.Terminated != nil {
		return "terminated"
	}
	return "pending"
}

func containerInfoFromStatus(spec corev1.Container, status *corev1.ContainerStatus) ContainerInfo {
	ci := ContainerInfo{
		Name:  spec.Name,
		Image: spec.Image,
		State: "pending",
	}
	if status != nil {
		ci.Ready = status.Ready
		ci.RestartCount = int(status.RestartCount)
		ci.State = containerState(&status.State)
		if status.State.Waiting != nil {
			ci.Reason = &status.State.Waiting.Reason
			ci.Message = &status.State.Waiting.Message
		}
		if status.State.Terminated != nil {
			ci.Reason = &status.State.Terminated.Reason
			ci.Message = &status.State.Terminated.Message
		}
	}
	return ci
}

// phaseAPI returns API enum: Pending | Running | Succeeded | Failed | Unknown
func phaseAPI(p corev1.PodPhase) string {
	switch p {
	case corev1.PodPending:
		return "Pending"
	case corev1.PodRunning:
		return "Running"
	case corev1.PodSucceeded:
		return "Succeeded"
	case corev1.PodFailed:
		return "Failed"
	default:
		return "Unknown"
	}
}

// qosClassAPI returns API enum: Guaranteed | Burstable | BestEffort
func qosClassAPI(q corev1.PodQOSClass) string {
	switch q {
	case corev1.PodQOSGuaranteed:
		return "Guaranteed"
	case corev1.PodQOSBurstable:
		return "Burstable"
	case corev1.PodQOSBestEffort:
		return "BestEffort"
	default:
		return "BestEffort"
	}
}

func Collect(ctx context.Context, client *kubernetes.Clientset, clusterID, customerID string) ClusterMetricsPayload {
	ts := time.Now().UTC().Format(time.RFC3339)
	empty := ClusterMetricsPayload{
		Timestamp:      ts,
		CollectionMode: "cluster",
		Summary:        ClusterSummary{},
		Nodes:          []NodeMetrics{},
	}

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return empty
	}

	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return empty
	}

	podsByNode := make(map[string][]PodSummary)
	var running, pending, failed, succeeded int
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
		containers := make([]ContainerInfo, 0, len(p.Spec.Containers))
		statusByName := make(map[string]*corev1.ContainerStatus)
		for i := range p.Status.ContainerStatuses {
			cs := &p.Status.ContainerStatuses[i]
			statusByName[cs.Name] = cs
			restartCount += int(cs.RestartCount)
		}
		for _, c := range p.Spec.Containers {
			containers = append(containers, containerInfoFromStatus(c, statusByName[c.Name]))
		}
		var startTime *string
		if p.Status.StartTime != nil {
			s := p.Status.StartTime.Format(time.RFC3339)
			startTime = &s
		}
		reqCPU, reqMem := sumContainerRequestsLimits(p.Spec.Containers, false)
		limCPU, limMem := sumContainerRequestsLimits(p.Spec.Containers, true)
		reqCPUQ := resource.NewMilliQuantity(reqCPU, resource.DecimalSI)
		reqMemQ := resource.NewQuantity(reqMem, resource.BinarySI)
		limCPUQ := resource.NewMilliQuantity(limCPU, resource.DecimalSI)
		limMemQ := resource.NewQuantity(limMem, resource.BinarySI)
		labels := p.Labels
		if labels == nil {
			labels = make(map[string]string)
		}
		ps := PodSummary{
			Namespace:      p.Namespace,
			Name:           p.Name,
			Node:           p.Spec.NodeName,
			Phase:          phaseAPI(p.Status.Phase),
			QOSClass:       qosClassAPI(p.Status.QOSClass),
			Ready:          podReady(p.Status.Conditions),
			RestartCount:   restartCount,
			StartTime:      startTime,
			Containers:     containers,
			Requests:       resourceMetricsFromQuantities(*reqCPUQ, *reqMemQ),
			Limits:         resourceMetricsFromQuantities(*limCPUQ, *limMemQ),
			Usage:          nil,
			Utilization:    nil,
			Labels:         labels,
			OwnerReference: podOwnerRef(p.OwnerReferences),
		}
		nodeName := p.Spec.NodeName
		podsByNode[nodeName] = append(podsByNode[nodeName], ps)
	}

	var totalCapCPU, totalCapMem resource.Quantity
	readyNodes := 0
	for _, n := range nodes.Items {
		cap := n.Status.Capacity
		totalCapCPU.Add(cap[corev1.ResourceCPU])
		totalCapMem.Add(cap[corev1.ResourceMemory])
		if nodeConditionStatus(n.Status.Conditions, corev1.NodeReady) {
			readyNodes++
		}
	}
	zeroQ := resource.MustParse("0")
	overallStatus := "healthy"
	if readyNodes < len(nodes.Items) {
		overallStatus = "degraded"
	}
	if len(nodes.Items) == 0 {
		overallStatus = "degraded"
	}
	clusterHealth := &ClusterHealth{
		TotalNodes:                  len(nodes.Items),
		ReadyNodes:                  readyNodes,
		NotReadyNodes:               len(nodes.Items) - readyNodes,
		OverallStatus:               overallStatus,
		AvgCPUUtilizationPercent:    0,
		AvgMemoryUtilizationPercent: 0,
		TotalCPUUsage:               zeroQ.String(),
		TotalMemoryUsage:            zeroQ.String(),
		TotalCPUCapacity:            totalCapCPU.String(),
		TotalMemoryCapacity:         totalCapMem.String(),
	}

	nodeList := make([]NodeMetrics, 0, len(nodes.Items))
	for _, n := range nodes.Items {
		capacity := n.Status.Capacity
		allocatable := n.Status.Allocatable
		capCPU := capacity[corev1.ResourceCPU]
		capMem := capacity[corev1.ResourceMemory]
		allocCPU := allocatable[corev1.ResourceCPU]
		allocMem := allocatable[corev1.ResourceMemory]
		ni := n.Status.NodeInfo
		nodePods := podsByNode[n.Name]
		if nodePods == nil {
			nodePods = []PodSummary{}
		}
		nodeList = append(nodeList, NodeMetrics{
			Name:           n.Name,
			Architecture:   ni.Architecture,
			KubeletVersion: ni.KubeletVersion,
			OSImage:        ni.OSImage,
			Capacity:       resourceMetricsFromQuantities(capCPU, capMem),
			Allocatable:    resourceMetricsFromQuantities(allocCPU, allocMem),
			Usage:          resourceMetricsFromQuantities(zeroQ, zeroQ),
			Utilization: UtilizationMetrics{
				CPUPercent:    0,
				MemoryPercent: 0,
				Status:        "unknown",
			},
			Health: NodeHealth{
				Ready:              nodeConditionStatus(n.Status.Conditions, corev1.NodeReady),
				ReadyStatus:        nodeReadyStatus(n.Status.Conditions),
				MemoryPressure:     nodeConditionStatus(n.Status.Conditions, corev1.NodeMemoryPressure),
				DiskPressure:       nodeConditionStatus(n.Status.Conditions, corev1.NodeDiskPressure),
				PIDPressure:        nodeConditionStatus(n.Status.Conditions, corev1.NodePIDPressure),
				NetworkUnavailable: nodeConditionStatus(n.Status.Conditions, corev1.NodeNetworkUnavailable),
			},
			Conditions: nodeConditionsFromK8s(n.Status.Conditions),
			Pods:       nodePods,
		})
	}

	_ = clusterID
	_ = customerID

	return ClusterMetricsPayload{
		Timestamp:      ts,
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
