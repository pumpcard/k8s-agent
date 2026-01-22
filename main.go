package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// Metrics structures for JSON output
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

type ResourceMetrics struct {
	CPU    string  `json:"cpu"`
	Memory string  `json:"memory"`
	CPUVal float64 `json:"cpu_millicores"`
	MemVal int64   `json:"memory_bytes"`
}

type UtilizationMetrics struct {
	CPUPercent    float64 `json:"cpu_percent"`
	MemoryPercent float64 `json:"memory_percent"`
	Status        string  `json:"status"` // healthy, warning, critical
}

type NodeHealth struct {
	Ready              bool   `json:"ready"`
	ReadyStatus        string `json:"ready_status"`
	MemoryPressure     bool   `json:"memory_pressure"`
	DiskPressure       bool   `json:"disk_pressure"`
	PIDPressure        bool   `json:"pid_pressure"`
	NetworkUnavailable bool   `json:"network_unavailable"`
}

type NodeCondition struct {
	Type    string `json:"type"`
	Status  string `json:"status"`
	Reason  string `json:"reason,omitempty"`
	Message string `json:"message,omitempty"`
}

type PodSummary struct {
	Namespace      string             `json:"namespace"`
	Name           string             `json:"name"`
	Node           string             `json:"node"`
	Phase          string             `json:"phase"`
	QOSClass       string             `json:"qos_class"`
	Ready          bool               `json:"ready"`
	RestartCount   int32              `json:"restart_count"`
	StartTime      string             `json:"start_time,omitempty"`
	Containers     []ContainerInfo    `json:"containers"`
	Requests       ResourceMetrics    `json:"requests"`
	Limits         ResourceMetrics    `json:"limits"`
	Usage          ResourceMetrics    `json:"usage,omitempty"`
	Utilization    UtilizationMetrics `json:"utilization,omitempty"`
	Labels         map[string]string  `json:"labels,omitempty"`
	OwnerReference string             `json:"owner_reference,omitempty"`
}

type ContainerInfo struct {
	Name         string `json:"name"`
	Image        string `json:"image"`
	Ready        bool   `json:"ready"`
	RestartCount int32  `json:"restart_count"`
	State        string `json:"state"` // running, waiting, terminated
	Reason       string `json:"reason,omitempty"`
	Message      string `json:"message,omitempty"`
}

type ClusterMetrics struct {
	Timestamp      string         `json:"timestamp"`
	CollectionMode string         `json:"collection_mode"`
	Nodes          []NodeMetrics  `json:"nodes"`
	ClusterHealth  ClusterHealth  `json:"cluster_health"`
	Summary        ClusterSummary `json:"summary"`
}

type ClusterHealth struct {
	TotalNodes          int     `json:"total_nodes"`
	ReadyNodes          int     `json:"ready_nodes"`
	NotReadyNodes       int     `json:"not_ready_nodes"`
	OverallStatus       string  `json:"overall_status"`
	AvgCPUUtil          float64 `json:"avg_cpu_utilization_percent"`
	AvgMemoryUtil       float64 `json:"avg_memory_utilization_percent"`
	TotalCPUUsage       string  `json:"total_cpu_usage"`
	TotalMemoryUsage    string  `json:"total_memory_usage"`
	TotalCPUCapacity    string  `json:"total_cpu_capacity"`
	TotalMemoryCapacity string  `json:"total_memory_capacity"`
}

type ClusterSummary struct {
	TotalPods     int `json:"total_pods"`
	RunningPods   int `json:"running_pods"`
	PendingPods   int `json:"pending_pods"`
	FailedPods    int `json:"failed_pods"`
	SucceededPods int `json:"succeeded_pods"`
}

func main() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		panic(err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}

	for {
		ctx := context.Background()
		metrics := collectMetrics(ctx, client)

		jsonData, err := json.MarshalIndent(metrics, "", "  ")
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error marshaling JSON: %v\n", err)
		} else {
			fmt.Println(string(jsonData))
		}

		fmt.Println("----")
		time.Sleep(10 * time.Second) // Increased to 10s for metrics API
	}
}

func collectMetrics(ctx context.Context, client *kubernetes.Clientset) ClusterMetrics {
	metrics := ClusterMetrics{
		Timestamp:      time.Now().UTC().Format(time.RFC3339),
		CollectionMode: "cluster",
		Nodes:          []NodeMetrics{},
	}

	// Get nodes
	nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing nodes: %v\n", err)
		return metrics
	}

	// Get node metrics from metrics API
	nodeMetricsMap := getNodeMetrics(ctx, client)

	// Get pods
	pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error listing pods: %v\n", err)
		return metrics
	}

	// Get pod metrics from metrics API
	podMetricsMap := getPodMetrics(ctx, client)

	// Process nodes
	var totalCPUUsage, totalMemoryUsage, totalCPUCapacity, totalMemoryCapacity resource.Quantity
	var readyNodes, notReadyNodes int
	var totalCPUUtil, totalMemoryUtil float64

	for _, node := range nodes.Items {
		nodeMetric := processNode(node, nodeMetricsMap, pods.Items, podMetricsMap)
		metrics.Nodes = append(metrics.Nodes, nodeMetric)

		// Aggregate cluster metrics
		if nodeMetric.Health.Ready {
			readyNodes++
		} else {
			notReadyNodes++
		}

		// Sum resources
		if cpuUsage, err := resource.ParseQuantity(nodeMetric.Usage.CPU); err == nil {
			totalCPUUsage.Add(cpuUsage)
		}
		if memUsage, err := resource.ParseQuantity(nodeMetric.Usage.Memory); err == nil {
			totalMemoryUsage.Add(memUsage)
		}
		if cpuCap, err := resource.ParseQuantity(nodeMetric.Capacity.CPU); err == nil {
			totalCPUCapacity.Add(cpuCap)
		}
		if memCap, err := resource.ParseQuantity(nodeMetric.Capacity.Memory); err == nil {
			totalMemoryCapacity.Add(memCap)
		}

		totalCPUUtil += nodeMetric.Utilization.CPUPercent
		totalMemoryUtil += nodeMetric.Utilization.MemoryPercent
	}

	// Calculate cluster health
	avgCPUUtil := 0.0
	avgMemoryUtil := 0.0
	if len(metrics.Nodes) > 0 {
		avgCPUUtil = totalCPUUtil / float64(len(metrics.Nodes))
		avgMemoryUtil = totalMemoryUtil / float64(len(metrics.Nodes))
	}

	overallStatus := "healthy"
	if notReadyNodes > 0 {
		overallStatus = "degraded"
	} else if avgCPUUtil > 80 || avgMemoryUtil > 80 {
		overallStatus = "warning"
	}

	metrics.ClusterHealth = ClusterHealth{
		TotalNodes:          len(metrics.Nodes),
		ReadyNodes:          readyNodes,
		NotReadyNodes:       notReadyNodes,
		OverallStatus:       overallStatus,
		AvgCPUUtil:          avgCPUUtil,
		AvgMemoryUtil:       avgMemoryUtil,
		TotalCPUUsage:       totalCPUUsage.String(),
		TotalMemoryUsage:    totalMemoryUsage.String(),
		TotalCPUCapacity:    totalCPUCapacity.String(),
		TotalMemoryCapacity: totalMemoryCapacity.String(),
	}

	// Process pods summary
	var running, pending, failed, succeeded int
	for _, pod := range pods.Items {
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
	}

	metrics.Summary = ClusterSummary{
		TotalPods:     len(pods.Items),
		RunningPods:   running,
		PendingPods:   pending,
		FailedPods:    failed,
		SucceededPods: succeeded,
	}

	return metrics
}

// extractCapacityMetrics extracts capacity metrics from a node
func extractCapacityMetrics(node corev1.Node) ResourceMetrics {
	capacity := ResourceMetrics{
		CPU:    node.Status.Capacity.Cpu().String(),
		Memory: node.Status.Capacity.Memory().String(),
	}
	if cpuVal := node.Status.Capacity.Cpu(); cpuVal != nil {
		capacity.CPUVal = float64(cpuVal.MilliValue())
	}
	if memVal := node.Status.Capacity.Memory(); memVal != nil {
		capacity.MemVal = memVal.Value()
	}
	return capacity
}

// extractAllocatableMetrics extracts allocatable metrics from a node
func extractAllocatableMetrics(node corev1.Node) ResourceMetrics {
	allocatable := ResourceMetrics{
		CPU:    node.Status.Allocatable.Cpu().String(),
		Memory: node.Status.Allocatable.Memory().String(),
	}
	if cpuVal := node.Status.Allocatable.Cpu(); cpuVal != nil {
		allocatable.CPUVal = float64(cpuVal.MilliValue())
	}
	if memVal := node.Status.Allocatable.Memory(); memVal != nil {
		allocatable.MemVal = memVal.Value()
	}
	return allocatable
}

// extractUsageMetrics extracts usage metrics from the metrics API
func extractUsageMetrics(nodeName string, nodeMetricsMap map[string]NodeUsageMetrics) (ResourceMetrics, bool) {
	nodeMetrics, hasMetrics := nodeMetricsMap[nodeName]
	if !hasMetrics {
		return ResourceMetrics{}, false
	}

	return ResourceMetrics{
		CPU:    nodeMetrics.CPU.String(),
		Memory: nodeMetrics.Memory.String(),
		CPUVal: float64(nodeMetrics.CPU.MilliValue()),
		MemVal: nodeMetrics.Memory.Value(),
	}, true
}

// calculateUtilization calculates CPU and memory utilization percentages
func calculateUtilization(usage, allocatable ResourceMetrics) (float64, float64) {
	cpuUtil := 0.0
	memoryUtil := 0.0

	if allocatable.CPUVal > 0 {
		cpuUtil = (usage.CPUVal / allocatable.CPUVal) * 100
	}
	if allocatable.MemVal > 0 {
		memoryUtil = (float64(usage.MemVal) / float64(allocatable.MemVal)) * 100
	}

	return cpuUtil, memoryUtil
}

// determineUtilizationStatus determines the health status based on utilization percentages
func determineUtilizationStatus(cpuUtil, memoryUtil float64) string {
	if cpuUtil > 90 || memoryUtil > 90 {
		return "critical"
	} else if cpuUtil > 75 || memoryUtil > 75 {
		return "warning"
	}
	return "healthy"
}

// extractNodeHealth extracts health information and conditions from a node
func extractNodeHealth(node corev1.Node) (NodeHealth, []NodeCondition) {
	health := NodeHealth{}
	conditions := []NodeCondition{}

	for _, condition := range node.Status.Conditions {
		conditions = append(conditions, NodeCondition{
			Type:    string(condition.Type),
			Status:  string(condition.Status),
			Reason:  condition.Reason,
			Message: condition.Message,
		})

		switch condition.Type {
		case corev1.NodeReady:
			health.Ready = condition.Status == corev1.ConditionTrue
			health.ReadyStatus = string(condition.Status)
		case corev1.NodeMemoryPressure:
			health.MemoryPressure = condition.Status == corev1.ConditionTrue
		case corev1.NodeDiskPressure:
			health.DiskPressure = condition.Status == corev1.ConditionTrue
		case corev1.NodePIDPressure:
			health.PIDPressure = condition.Status == corev1.ConditionTrue
		case corev1.NodeNetworkUnavailable:
			health.NetworkUnavailable = condition.Status == corev1.ConditionTrue
		}
	}

	return health, conditions
}

// getNodePods returns all pods running on a specific node
func getNodePods(nodeName string, pods []corev1.Pod, podMetricsMap map[string]PodUsageMetrics) []PodSummary {
	nodePods := []PodSummary{}
	for _, pod := range pods {
		if pod.Spec.NodeName == nodeName {
			podSummary := processPod(pod, podMetricsMap)
			nodePods = append(nodePods, podSummary)
		}
	}
	return nodePods
}

func processNode(node corev1.Node, nodeMetricsMap map[string]NodeUsageMetrics, pods []corev1.Pod, podMetricsMap map[string]PodUsageMetrics) NodeMetrics {
	// Extract resource metrics
	capacity := extractCapacityMetrics(node)
	allocatable := extractAllocatableMetrics(node)
	usage, hasMetrics := extractUsageMetrics(node.Name, nodeMetricsMap)

	// Calculate utilization
	var cpuUtil, memoryUtil float64
	var utilStatus string
	if hasMetrics {
		cpuUtil, memoryUtil = calculateUtilization(usage, allocatable)
		utilStatus = determineUtilizationStatus(cpuUtil, memoryUtil)
	} else {
		utilStatus = "unknown"
	}

	// Extract health information
	health, conditions := extractNodeHealth(node)

	// Get pods on this node
	nodePods := getNodePods(node.Name, pods, podMetricsMap)

	return NodeMetrics{
		Name:           node.Name,
		Architecture:   node.Status.NodeInfo.Architecture,
		KubeletVersion: node.Status.NodeInfo.KubeletVersion,
		OSImage:        node.Status.NodeInfo.OSImage,
		Capacity:       capacity,
		Allocatable:    allocatable,
		Usage:          usage,
		Utilization: UtilizationMetrics{
			CPUPercent:    cpuUtil,
			MemoryPercent: memoryUtil,
			Status:        utilStatus,
		},
		Health:     health,
		Conditions: conditions,
		Pods:       nodePods,
	}
}

type NodeUsageMetrics struct {
	CPU    resource.Quantity
	Memory resource.Quantity
}

type PodUsageMetrics struct {
	CPU    resource.Quantity
	Memory resource.Quantity
}

func getNodeMetrics(ctx context.Context, client *kubernetes.Clientset) map[string]NodeUsageMetrics {
	result := make(map[string]NodeUsageMetrics)

	// Try to get metrics from metrics API
	// Note: This requires metrics-server to be installed
	restClient := client.RESTClient()
	metricsPath := "/apis/metrics.k8s.io/v1beta1/nodes"

	raw, err := restClient.Get().
		AbsPath(metricsPath).
		Do(ctx).
		Raw()

	if err != nil {
		// Metrics API not available, return empty map
		return result
	}

	var metricsList struct {
		Items []struct {
			Metadata struct {
				Name string `json:"name"`
			} `json:"metadata"`
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"items"`
	}

	if err := json.Unmarshal(raw, &metricsList); err != nil {
		return result
	}

	for _, item := range metricsList.Items {
		cpu, _ := resource.ParseQuantity(item.Usage.CPU)
		memory, _ := resource.ParseQuantity(item.Usage.Memory)
		result[item.Metadata.Name] = NodeUsageMetrics{
			CPU:    cpu,
			Memory: memory,
		}
	}

	return result
}

// podMetricsResponse represents the structure of the metrics API response
type podMetricsResponse struct {
	Items []struct {
		Metadata struct {
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
		} `json:"metadata"`
		Containers []struct {
			Usage struct {
				CPU    string `json:"cpu"`
				Memory string `json:"memory"`
			} `json:"usage"`
		} `json:"containers"`
	} `json:"items"`
}

// fetchPodMetricsFromAPI fetches raw pod metrics from the Kubernetes metrics API
func fetchPodMetricsFromAPI(ctx context.Context, client *kubernetes.Clientset) ([]byte, error) {
	restClient := client.RESTClient()
	metricsPath := "/apis/metrics.k8s.io/v1beta1/pods"

	raw, err := restClient.Get().
		AbsPath(metricsPath).
		Do(ctx).
		Raw()

	return raw, err
}

// parsePodMetricsResponse parses the JSON response from the metrics API
func parsePodMetricsResponse(raw []byte) (podMetricsResponse, error) {
	var metricsList podMetricsResponse
	err := json.Unmarshal(raw, &metricsList)
	return metricsList, err
}

// aggregateContainerMetrics aggregates CPU and memory usage across all containers in a pod
func aggregateContainerMetrics(containers []struct {
	Usage struct {
		CPU    string `json:"cpu"`
		Memory string `json:"memory"`
	} `json:"usage"`
}) (resource.Quantity, resource.Quantity) {
	var totalCPU, totalMemory resource.Quantity

	for _, container := range containers {
		if cpu, err := resource.ParseQuantity(container.Usage.CPU); err == nil {
			totalCPU.Add(cpu)
		}
		if memory, err := resource.ParseQuantity(container.Usage.Memory); err == nil {
			totalMemory.Add(memory)
		}
	}

	return totalCPU, totalMemory
}

// buildPodMetricsMap builds a map of pod usage metrics keyed by "namespace/name"
func buildPodMetricsMap(metricsList podMetricsResponse) map[string]PodUsageMetrics {
	result := make(map[string]PodUsageMetrics)

	for _, item := range metricsList.Items {
		totalCPU, totalMemory := aggregateContainerMetrics(item.Containers)
		key := fmt.Sprintf("%s/%s", item.Metadata.Namespace, item.Metadata.Name)
		result[key] = PodUsageMetrics{
			CPU:    totalCPU,
			Memory: totalMemory,
		}
	}

	return result
}

func getPodMetrics(ctx context.Context, client *kubernetes.Clientset) map[string]PodUsageMetrics {
	// Fetch raw metrics from API
	raw, err := fetchPodMetricsFromAPI(ctx, client)
	if err != nil {
		return make(map[string]PodUsageMetrics)
	}

	// Parse the response
	metricsList, err := parsePodMetricsResponse(raw)
	if err != nil {
		return make(map[string]PodUsageMetrics)
	}

	// Build and return the metrics map
	return buildPodMetricsMap(metricsList)
}

func processPod(pod corev1.Pod, podMetricsMap map[string]PodUsageMetrics) PodSummary {
	// Calculate requests and limits
	var requestsCPU, requestsMemory, limitsCPU, limitsMemory resource.Quantity
	containers := []ContainerInfo{}
	var totalRestartCount int32
	var podReady bool = true

	// Process each container
	for _, container := range pod.Spec.Containers {
		// Calculate resource requests and limits
		if req := container.Resources.Requests; req != nil {
			if cpu := req[corev1.ResourceCPU]; !cpu.IsZero() {
				requestsCPU.Add(cpu)
			}
			if memory := req[corev1.ResourceMemory]; !memory.IsZero() {
				requestsMemory.Add(memory)
			}
		}
		if lim := container.Resources.Limits; lim != nil {
			if cpu := lim[corev1.ResourceCPU]; !cpu.IsZero() {
				limitsCPU.Add(cpu)
			}
			if memory := lim[corev1.ResourceMemory]; !memory.IsZero() {
				limitsMemory.Add(memory)
			}
		}

		// Get container status
		containerInfo := ContainerInfo{
			Name:  container.Name,
			Image: container.Image,
		}

		// Find container status
		for _, status := range pod.Status.ContainerStatuses {
			if status.Name == container.Name {
				containerInfo.Ready = status.Ready
				containerInfo.RestartCount = status.RestartCount
				totalRestartCount += status.RestartCount

				if !status.Ready {
					podReady = false
				}

				// Determine container state
				if status.State.Running != nil {
					containerInfo.State = "running"
				} else if status.State.Waiting != nil {
					containerInfo.State = "waiting"
					containerInfo.Reason = status.State.Waiting.Reason
					containerInfo.Message = status.State.Waiting.Message
				} else if status.State.Terminated != nil {
					containerInfo.State = "terminated"
					containerInfo.Reason = status.State.Terminated.Reason
					containerInfo.Message = status.State.Terminated.Message
				}
				break
			}
		}

		// If no status found, container might be in init state
		if containerInfo.State == "" {
			containerInfo.State = "pending"
		}

		containers = append(containers, containerInfo)
	}

	requests := ResourceMetrics{
		CPU:    requestsCPU.String(),
		Memory: requestsMemory.String(),
		CPUVal: float64(requestsCPU.MilliValue()),
		MemVal: requestsMemory.Value(),
	}

	limits := ResourceMetrics{
		CPU:    limitsCPU.String(),
		Memory: limitsMemory.String(),
		CPUVal: float64(limitsCPU.MilliValue()),
		MemVal: limitsMemory.Value(),
	}

	// Get usage from metrics API
	key := fmt.Sprintf("%s/%s", pod.Namespace, pod.Name)
	podMetrics, hasMetrics := podMetricsMap[key]

	usage := ResourceMetrics{}
	utilization := UtilizationMetrics{}
	if hasMetrics {
		usage = ResourceMetrics{
			CPU:    podMetrics.CPU.String(),
			Memory: podMetrics.Memory.String(),
			CPUVal: float64(podMetrics.CPU.MilliValue()),
			MemVal: podMetrics.Memory.Value(),
		}

		// Calculate utilization against requests (if available)
		if requests.CPUVal > 0 {
			utilization.CPUPercent = (usage.CPUVal / requests.CPUVal) * 100
		}
		if requests.MemVal > 0 {
			utilization.MemoryPercent = (float64(usage.MemVal) / float64(requests.MemVal)) * 100
		}

		if utilization.CPUPercent > 90 || utilization.MemoryPercent > 90 {
			utilization.Status = "critical"
		} else if utilization.CPUPercent > 75 || utilization.MemoryPercent > 75 {
			utilization.Status = "warning"
		} else {
			utilization.Status = "healthy"
		}
	}

	// Determine QOS class
	qosClass := string(pod.Status.QOSClass)
	if qosClass == "" {
		// Calculate QOS class if not set
		if limitsCPU.IsZero() && limitsMemory.IsZero() {
			if requestsCPU.IsZero() && requestsMemory.IsZero() {
				qosClass = "BestEffort"
			} else {
				qosClass = "Burstable"
			}
		} else {
			// Check if requests equal limits for all containers
			allGuaranteed := true
			for _, container := range pod.Spec.Containers {
				reqCPU := container.Resources.Requests[corev1.ResourceCPU]
				reqMem := container.Resources.Requests[corev1.ResourceMemory]
				limCPU := container.Resources.Limits[corev1.ResourceCPU]
				limMem := container.Resources.Limits[corev1.ResourceMemory]
				if !reqCPU.Equal(limCPU) || !reqMem.Equal(limMem) {
					allGuaranteed = false
					break
				}
			}
			if allGuaranteed {
				qosClass = "Guaranteed"
			} else {
				qosClass = "Burstable"
			}
		}
	}

	// Get start time
	startTime := ""
	if pod.Status.StartTime != nil {
		startTime = pod.Status.StartTime.Format(time.RFC3339)
	}

	// Get owner reference
	ownerRef := ""
	if len(pod.OwnerReferences) > 0 {
		owner := pod.OwnerReferences[0]
		ownerRef = fmt.Sprintf("%s/%s", owner.Kind, owner.Name)
	}

	// Get labels
	labels := pod.Labels
	if len(labels) == 0 {
		labels = nil
	}

	return PodSummary{
		Namespace:      pod.Namespace,
		Name:           pod.Name,
		Node:           pod.Spec.NodeName,
		Phase:          string(pod.Status.Phase),
		QOSClass:       qosClass,
		Ready:          podReady,
		RestartCount:   totalRestartCount,
		StartTime:      startTime,
		Containers:     containers,
		Requests:       requests,
		Limits:         limits,
		Usage:          usage,
		Utilization:    utilization,
		Labels:         labels,
		OwnerReference: ownerRef,
	}
}
