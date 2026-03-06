package collector

import (
	"context"
	"log/slog"
	"os"
	"time"

	"k8s-agent/internal/cloud"
	_ "k8s-agent/internal/cloud/aws"
	_ "k8s-agent/internal/cloud/azure"
	_ "k8s-agent/internal/cloud/gcp"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

var collectorLog = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

type ClusterMetricsPayload struct {
	ClusterID      string         `json:"cluster_id"`
	Timestamp      string         `json:"timestamp"`
	CollectionMode string         `json:"collection_mode"`
	ClusterHealth  *ClusterHealth `json:"cluster_health"`
	Summary        ClusterSummary `json:"summary"`
	Nodes          []NodeMetrics  `json:"nodes"`
	AccountID      string         `json:"account_id"` // Cloud account ID (AWS account, GCP project, or Azure subscription)
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
	Name           string `json:"name"`
	Architecture   string `json:"architecture"`
	KubeletVersion string `json:"kubelet_version"`
	OSImage        string `json:"os_image"`
	// Cloud provider fields for cost/RI/SP correlation (from labels and providerID)
	Provider                        string             `json:"provider,omitempty"`      // aws, gcp, azure, or empty if unknown
	InstanceType                    string             `json:"instance_type,omitempty"` // e.g. m5.2xlarge, n2-standard-4
	InstanceID                      string             `json:"instance_id,omitempty"`   // e.g. i-0abc123 (AWS), VM name (GCP/Azure)
	Zone                            string             `json:"zone,omitempty"`          // availability zone, e.g. us-west-2a
	Region                          string             `json:"region,omitempty"`        // e.g. us-west-2
	ProjectID                       string             `json:"project_id,omitempty"`    // GCP project ID when applicable
	Capacity                        ResourceMetrics    `json:"capacity"`
	Allocatable                     ResourceMetrics    `json:"allocatable"`
	Usage                           ResourceMetrics    `json:"usage"`
	K8sNodeCPUCapacityMillicores    int64              `json:"k8s_node_cpu_capacity_millicores"`
	K8sNodeMemoryCapacityBytes      int64              `json:"k8s_node_memory_capacity_bytes"`
	K8sNodeCPUAllocatableMillicores int64              `json:"k8s_node_cpu_allocatable_millicores"`
	K8sNodeMemoryAllocatableBytes   int64              `json:"k8s_node_memory_allocatable_bytes"`
	K8sNodeCPUUsageMillicores       int64              `json:"k8s_node_cpu_usage_millicores"`
	K8sNodeMemoryUsageBytes         int64              `json:"k8s_node_memory_usage_bytes"`
	Utilization                     UtilizationMetrics `json:"utilization"`
	Health                          NodeHealth         `json:"health"`
	Conditions                      []NodeCondition    `json:"conditions"`
	Pods                            []PodSummary       `json:"pods"`
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

func quantityToMilli(quantity resource.Quantity) int64 {
	return quantity.MilliValue()
}

func quantityToBytes(quantity resource.Quantity) int64 {
	return quantity.Value()
}

// quantityToString returns Kubernetes quantity format string (e.g. "4", "3920m", "16Gi").
func quantityToString(quantity resource.Quantity) string {
	return quantity.String()
}

func resourceMetricsFromQuantities(cpuQuantity resource.Quantity, memQuantity resource.Quantity) ResourceMetrics {
	return ResourceMetrics{
		CPU:           quantityToString(cpuQuantity),
		Memory:        quantityToString(memQuantity),
		CPUMillicores: quantityToMilli(cpuQuantity),
		MemoryBytes:   quantityToBytes(memQuantity),
	}
}

func nodeConditionStatus(conditions []corev1.NodeCondition, conditionType corev1.NodeConditionType) bool {
	for _, condition := range conditions {
		if condition.Type == conditionType {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func nodeReadyStatus(conditions []corev1.NodeCondition) string {
	for _, condition := range conditions {
		if condition.Type == corev1.NodeReady {
			if condition.Status == corev1.ConditionTrue {
				return "Ready"
			}
			return "NotReady"
		}
	}
	return "Unknown"
}

// nodeCloudInfo extracts instance type, instance ID, zone, region, provider from node labels and providerID.
// Uses the cloud package for provider-specific parsing (AWS, GCP, Azure).
func nodeCloudInfo(node *corev1.Node) (provider, instanceType, instanceID, zone, region string) {
	instanceType, zone, region = cloud.Labels(node.Labels)
	provider, instanceID, zoneFromProvider := cloud.Parse(node.Spec.ProviderID)
	if zone == "" && zoneFromProvider != "" {
		zone = zoneFromProvider
	}
	if region == "" && zone != "" {
		region = cloud.ZoneToRegion(zone)
	}
	return provider, instanceType, instanceID, zone, region
}

// nodeAccountID returns the cloud account ID for a node (used to derive the cluster account_id).
// From providerID: GCP project, Azure subscription. For AWS, uses node label pump.co/account-id when set.
func nodeAccountID(node *corev1.Node) string {
	accountID := cloud.AccountID(node.Spec.ProviderID)
	if accountID != "" {
		return accountID
	}
	if node.Labels != nil && node.Labels["pump.co/account-id"] != "" {
		return node.Labels["pump.co/account-id"]
	}
	return ""
}

func nodeConditionsFromK8s(conditions []corev1.NodeCondition) []NodeCondition {
	out := make([]NodeCondition, 0, len(conditions))
	for _, condition := range conditions {
		nodeCondition := NodeCondition{
			Type:   string(condition.Type),
			Status: string(condition.Status),
		}
		if condition.Reason != "" {
			nodeCondition.Reason = &condition.Reason
		}
		if condition.Message != "" {
			nodeCondition.Message = &condition.Message
		}
		out = append(out, nodeCondition)
	}
	return out
}

func podReady(conditions []corev1.PodCondition) bool {
	for _, condition := range conditions {
		if condition.Type == corev1.PodReady {
			return condition.Status == corev1.ConditionTrue
		}
	}
	return false
}

func podOwnerRef(owners []metav1.OwnerReference) *string {
	if len(owners) == 0 {
		return nil
	}
	ownerReferenceString := owners[0].Kind + "/" + owners[0].Name
	return &ownerReferenceString
}

func containerState(state *corev1.ContainerState) string {
	if state == nil {
		return "pending"
	}
	if state.Running != nil {
		return "running"
	}
	if state.Waiting != nil {
		return "waiting"
	}
	if state.Terminated != nil {
		return "terminated"
	}
	return "pending"
}

func containerInfoFromStatus(spec corev1.Container, status *corev1.ContainerStatus) ContainerInfo {
	containerInfo := ContainerInfo{
		Name:  spec.Name,
		Image: spec.Image,
		State: "pending",
	}
	if status != nil {
		containerInfo.Ready = status.Ready
		containerInfo.RestartCount = int(status.RestartCount)
		containerInfo.State = containerState(&status.State)
		if status.State.Waiting != nil {
			containerInfo.Reason = &status.State.Waiting.Reason
			containerInfo.Message = &status.State.Waiting.Message
		}
		if status.State.Terminated != nil {
			containerInfo.Reason = &status.State.Terminated.Reason
			containerInfo.Message = &status.State.Terminated.Message
		}
	}
	return containerInfo
}

// phaseAPI returns API enum: Pending | Running | Succeeded | Failed | Unknown
func phaseAPI(phase corev1.PodPhase) string {
	switch phase {
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
func qosClassAPI(qosClass corev1.PodQOSClass) string {
	switch qosClass {
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

// resourceUsage holds CPU millicores and memory bytes (from metrics.k8s.io).
type resourceUsage struct{ cpuMilli, memBytes int64 }

var systemNamespaces = map[string]bool{
	// Kubernetes core
	"kube-system":     true,
	"kube-public":     true,
	"kube-node-lease": true,
	// GKE
	"gke-managed-cim":          true,
	"gke-gmp-system":           true,
	"gmp-system":               true,
	"gke-managed-filestorecsi": true,
	"gke-managed-system":       true,
	// EKS
	"amazon-cloudwatch": true,
	"aws-observability": true,
	"amazon-guardduty":  true,
	// AKS
	"gatekeeper-system": true,
	"calico-system":     true,
	"tigera-operator":   true,
}

func isSystemNamespace(ns string) bool {
	return systemNamespaces[ns]
}

func Collect(ctx context.Context, client *kubernetes.Clientset, clusterID string, metricsClient *metricsclient.Clientset) ClusterMetricsPayload {
	ts := time.Now().UTC().Format(time.RFC3339)
	empty := ClusterMetricsPayload{
		Timestamp:      ts,
		CollectionMode: "cluster",
		Summary:        ClusterSummary{},
		Nodes:          []NodeMetrics{},
	}

	collectorLog.Debug("collect_start", "cluster_id", clusterID)

	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		collectorLog.Error("nodes_list_failed", "error", err)
		return empty
	}
	collectorLog.Debug("nodes_listed", "count", len(nodes.Items))

	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		collectorLog.Error("pods_list_failed", "error", err)
		return empty
	}
	collectorLog.Debug("pods_listed", "count", len(pods.Items))

	nodeUsageMap := make(map[string]resourceUsage)
	podUsageMap := make(map[string]resourceUsage)
	if metricsClient != nil {
		nodeMetricsList, err := metricsClient.MetricsV1beta1().NodeMetricses().List(ctx, metav1.ListOptions{})
		if err != nil {
			collectorLog.Warn("node_metrics_list_failed", "error", err)
		} else {
			for index := range nodeMetricsList.Items {
				nodeMetrics := &nodeMetricsList.Items[index]
				nodeUsageMap[nodeMetrics.Name] = resourceUsage{
					cpuMilli: quantityToMilli(nodeMetrics.Usage[corev1.ResourceCPU]),
					memBytes: quantityToBytes(nodeMetrics.Usage[corev1.ResourceMemory]),
				}
			}
			for nodeName, usage := range nodeUsageMap {
				collectorLog.Debug("node_usage", "node", nodeName, "cpu_millicores", usage.cpuMilli, "memory_bytes", usage.memBytes)
			}
		}
		podMetricsList, err := metricsClient.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
		if err != nil {
			collectorLog.Warn("pod_metrics_list_failed", "error", err)
		} else {
			for index := range podMetricsList.Items {
				podMetrics := &podMetricsList.Items[index]
				var cpuMilli, memBytes int64
				for containerIdx := range podMetrics.Containers {
					containerMetrics := &podMetrics.Containers[containerIdx]
					cpuMilli += quantityToMilli(containerMetrics.Usage[corev1.ResourceCPU])
					memBytes += quantityToBytes(containerMetrics.Usage[corev1.ResourceMemory])
				}
				podUsageMap[podMetrics.Namespace+"/"+podMetrics.Name] = resourceUsage{cpuMilli: cpuMilli, memBytes: memBytes}
			}
			for podKey, usage := range podUsageMap {
				collectorLog.Debug("pod_usage", "pod", podKey, "cpu_millicores", usage.cpuMilli, "memory_bytes", usage.memBytes)
			}
		}
	} else {
		collectorLog.Debug("read_from", "source", "metrics.k8s.io", "msg", "skipped (no client); usage=0")
	}

	podsByNode := make(map[string][]PodSummary)
	var running, pending, failed, succeeded, skippedSystem int
	for _, pod := range pods.Items {
		if isSystemNamespace(pod.Namespace) {
			skippedSystem++
			continue
		}
		switch pod.Status.Phase {
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
		containers := make([]ContainerInfo, 0, len(pod.Spec.Containers))
		statusByName := make(map[string]*corev1.ContainerStatus)
		for index := range pod.Status.ContainerStatuses {
			containerStatus := &pod.Status.ContainerStatuses[index]
			statusByName[containerStatus.Name] = containerStatus
			restartCount += int(containerStatus.RestartCount)
		}
		for _, container := range pod.Spec.Containers {
			containers = append(containers, containerInfoFromStatus(container, statusByName[container.Name]))
		}
		var startTime *string
		if pod.Status.StartTime != nil {
			formattedStartTime := pod.Status.StartTime.Format(time.RFC3339)
			startTime = &formattedStartTime
		}
		requestedCPUMillicores, requestedMemoryBytes := sumContainerRequestsLimits(pod.Spec.Containers, false)
		limitCPUMillicores, limitMemoryBytes := sumContainerRequestsLimits(pod.Spec.Containers, true)
		requestedCPUQuantity := resource.NewMilliQuantity(requestedCPUMillicores, resource.DecimalSI)
		requestedMemoryQuantity := resource.NewQuantity(requestedMemoryBytes, resource.BinarySI)
		limitCPUQuantity := resource.NewMilliQuantity(limitCPUMillicores, resource.DecimalSI)
		limitMemoryQuantity := resource.NewQuantity(limitMemoryBytes, resource.BinarySI)
		labels := pod.Labels
		if labels == nil {
			labels = make(map[string]string)
		}
		podSummary := PodSummary{
			Namespace:      pod.Namespace,
			Name:           pod.Name,
			Node:           pod.Spec.NodeName,
			Phase:          phaseAPI(pod.Status.Phase),
			QOSClass:       qosClassAPI(pod.Status.QOSClass),
			Ready:          podReady(pod.Status.Conditions),
			RestartCount:   restartCount,
			StartTime:      startTime,
			Containers:     containers,
			Requests:       resourceMetricsFromQuantities(*requestedCPUQuantity, *requestedMemoryQuantity),
			Limits:         resourceMetricsFromQuantities(*limitCPUQuantity, *limitMemoryQuantity),
			Usage:          nil,
			Utilization:    nil,
			Labels:         labels,
			OwnerReference: podOwnerRef(pod.OwnerReferences),
		}
		if usage, ok := podUsageMap[pod.Namespace+"/"+pod.Name]; ok {
			usageCPUQuantity := resource.NewMilliQuantity(usage.cpuMilli, resource.DecimalSI)
			usageMemoryQuantity := resource.NewQuantity(usage.memBytes, resource.BinarySI)
			podSummary.Usage = &ResourceMetrics{
				CPU:           usageCPUQuantity.String(),
				Memory:        usageMemoryQuantity.String(),
				CPUMillicores: usage.cpuMilli,
				MemoryBytes:   usage.memBytes,
			}
			var cpuPercent, memoryPercent float64
			if requestedCPUMillicores > 0 {
				cpuPercent = capPercent(100 * float64(usage.cpuMilli) / float64(requestedCPUMillicores))
			}
			if requestedMemoryBytes > 0 {
				memoryPercent = capPercent(100 * float64(usage.memBytes) / float64(requestedMemoryBytes))
			}
			podSummary.Utilization = &UtilizationMetrics{CPUPercent: cpuPercent, MemoryPercent: memoryPercent, Status: "unknown"}
		}
		nodeName := pod.Spec.NodeName
		podsByNode[nodeName] = append(podsByNode[nodeName], podSummary)
	}

	collectorLog.Debug("pods_filtered", "collected", running+pending+failed+succeeded, "skipped_system", skippedSystem)

	var totalCapCPU, totalCapMem resource.Quantity
	var totalAllocCPU, totalAllocMem resource.Quantity
	var totalUsageCPU, totalUsageMem int64
	readyNodes := 0
	for _, node := range nodes.Items {
		capacity := node.Status.Capacity
		allocatable := node.Status.Allocatable
		totalCapCPU.Add(capacity[corev1.ResourceCPU])
		totalCapMem.Add(capacity[corev1.ResourceMemory])
		totalAllocCPU.Add(allocatable[corev1.ResourceCPU])
		totalAllocMem.Add(allocatable[corev1.ResourceMemory])
		if nodeUsage, ok := nodeUsageMap[node.Name]; ok {
			totalUsageCPU += nodeUsage.cpuMilli
			totalUsageMem += nodeUsage.memBytes
		}
		if nodeConditionStatus(node.Status.Conditions, corev1.NodeReady) {
			readyNodes++
		}
	}
	zeroQuantity := resource.MustParse("0")
	overallStatus := "healthy"
	if readyNodes < len(nodes.Items) {
		overallStatus = "degraded"
	}
	if len(nodes.Items) == 0 {
		overallStatus = "degraded"
	}
	totalUsageCPUQ := resource.NewMilliQuantity(totalUsageCPU, resource.DecimalSI)
	totalUsageMemQ := resource.NewQuantity(totalUsageMem, resource.BinarySI)
	avgCPUUtilizationPercent := 0.0
	avgMemoryUtilizationPercent := 0.0
	if allocMilli := quantityToMilli(totalAllocCPU); allocMilli > 0 {
		avgCPUUtilizationPercent = 100 * float64(totalUsageCPU) / float64(allocMilli)
	}
	if allocBytes := quantityToBytes(totalAllocMem); allocBytes > 0 {
		avgMemoryUtilizationPercent = 100 * float64(totalUsageMem) / float64(allocBytes)
	}
	clusterHealth := &ClusterHealth{
		TotalNodes:                  len(nodes.Items),
		ReadyNodes:                  readyNodes,
		NotReadyNodes:               len(nodes.Items) - readyNodes,
		OverallStatus:               overallStatus,
		AvgCPUUtilizationPercent:    avgCPUUtilizationPercent,
		AvgMemoryUtilizationPercent: avgMemoryUtilizationPercent,
		TotalCPUUsage:               totalUsageCPUQ.String(),
		TotalMemoryUsage:            totalUsageMemQ.String(),
		TotalCPUCapacity:            totalCapCPU.String(),
		TotalMemoryCapacity:         totalCapMem.String(),
	}
	collectorLog.Debug("cluster_health",
		"total_nodes", clusterHealth.TotalNodes,
		"ready_nodes", clusterHealth.ReadyNodes,
		"overall_status", clusterHealth.OverallStatus,
		"total_cpu_usage", clusterHealth.TotalCPUUsage,
		"total_memory_usage", clusterHealth.TotalMemoryUsage,
		"avg_cpu_utilization_percent", avgCPUUtilizationPercent,
		"avg_memory_utilization_percent", avgMemoryUtilizationPercent)

	clusterAccountID := ""
	for _, node := range nodes.Items {
		if id := nodeAccountID(&node); id != "" {
			clusterAccountID = id
			break
		}
	}

	nodeList := make([]NodeMetrics, 0, len(nodes.Items))
	for _, node := range nodes.Items {
		capacity := node.Status.Capacity
		allocatable := node.Status.Allocatable
		capacityCPU := capacity[corev1.ResourceCPU]
		capacityMemory := capacity[corev1.ResourceMemory]
		allocatableCPU := allocatable[corev1.ResourceCPU]
		allocatableMemory := allocatable[corev1.ResourceMemory]
		nodeInfo := node.Status.NodeInfo
		provider, instanceType, instanceID, zone, region := nodeCloudInfo(&node)
		nodePods := podsByNode[node.Name]
		if nodePods == nil {
			nodePods = []PodSummary{}
		}
		usageCPU := zeroQuantity
		usageMem := zeroQuantity
		usageMilli := int64(0)
		usageBytes := int64(0)
		cpuPercent := 0.0
		memoryPercent := 0.0
		if nodeUsage, ok := nodeUsageMap[node.Name]; ok {
			usageMilli = nodeUsage.cpuMilli
			usageBytes = nodeUsage.memBytes
			usageCPU = *resource.NewMilliQuantity(nodeUsage.cpuMilli, resource.DecimalSI)
			usageMem = *resource.NewQuantity(nodeUsage.memBytes, resource.BinarySI)
			allocMilli := quantityToMilli(allocatableCPU)
			allocBytes := quantityToBytes(allocatableMemory)
			if allocMilli > 0 {
				cpuPercent = 100 * float64(nodeUsage.cpuMilli) / float64(allocMilli)
			}
			if allocBytes > 0 {
				memoryPercent = 100 * float64(nodeUsage.memBytes) / float64(allocBytes)
			}
		}
		nodeList = append(nodeList, NodeMetrics{
			Name:                            node.Name,
			Architecture:                    nodeInfo.Architecture,
			KubeletVersion:                  nodeInfo.KubeletVersion,
			OSImage:                         nodeInfo.OSImage,
			Provider:                        provider,
			InstanceType:                    instanceType,
			InstanceID:                      instanceID,
			Zone:                            zone,
			Region:                          region,
			ProjectID:                       cloud.ProjectID(node.Spec.ProviderID),
			Capacity:                        resourceMetricsFromQuantities(capacityCPU, capacityMemory),
			Allocatable:                     resourceMetricsFromQuantities(allocatableCPU, allocatableMemory),
			Usage:                           resourceMetricsFromQuantities(usageCPU, usageMem),
			K8sNodeCPUCapacityMillicores:    quantityToMilli(capacityCPU),
			K8sNodeMemoryCapacityBytes:      quantityToBytes(capacityMemory),
			K8sNodeCPUAllocatableMillicores: quantityToMilli(allocatableCPU),
			K8sNodeMemoryAllocatableBytes:   quantityToBytes(allocatableMemory),
			K8sNodeCPUUsageMillicores:       usageMilli,
			K8sNodeMemoryUsageBytes:         usageBytes,
			Utilization: UtilizationMetrics{
				CPUPercent:    cpuPercent,
				MemoryPercent: memoryPercent,
				Status:        "unknown",
			},
			Health: NodeHealth{
				Ready:              nodeConditionStatus(node.Status.Conditions, corev1.NodeReady),
				ReadyStatus:        nodeReadyStatus(node.Status.Conditions),
				MemoryPressure:     nodeConditionStatus(node.Status.Conditions, corev1.NodeMemoryPressure),
				DiskPressure:       nodeConditionStatus(node.Status.Conditions, corev1.NodeDiskPressure),
				PIDPressure:        nodeConditionStatus(node.Status.Conditions, corev1.NodePIDPressure),
				NetworkUnavailable: nodeConditionStatus(node.Status.Conditions, corev1.NodeNetworkUnavailable),
			},
			Conditions: nodeConditionsFromK8s(node.Status.Conditions),
			Pods:       nodePods,
		})
	}

	payload := ClusterMetricsPayload{
		ClusterID:      clusterID,
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
		Nodes:     nodeList,
		AccountID: clusterAccountID,
	}
	for _, node := range payload.Nodes {
		collectorLog.Debug("payload_node",
			"node", node.Name,
			"pods", len(node.Pods),
			"cpu_millicores", node.K8sNodeCPUUsageMillicores,
			"memory_bytes", node.K8sNodeMemoryUsageBytes,
			"capacity_cpu_milli", node.K8sNodeCPUCapacityMillicores,
			"allocatable_memory_bytes", node.K8sNodeMemoryAllocatableBytes)
	}
	collectorLog.Debug("collect_done",
		"summary", slog.Group("payload",
			"nodes", len(payload.Nodes),
			"pods_total", payload.Summary.TotalPods,
			"running", payload.Summary.RunningPods))
	return payload
}

func capPercent(v float64) float64 {
	if v > 100 {
		return 100
	}
	return v
}

func sumContainerRequestsLimits(containers []corev1.Container, useLimits bool) (cpuMillicores, memoryBytes int64) {
	for _, container := range containers {
		var resourceList corev1.ResourceList
		if useLimits {
			resourceList = container.Resources.Limits
		} else {
			resourceList = container.Resources.Requests
		}
		cpuMillicores += quantityToMilli(resourceList[corev1.ResourceCPU])
		memoryBytes += quantityToBytes(resourceList[corev1.ResourceMemory])
	}
	return cpuMillicores, memoryBytes
}
