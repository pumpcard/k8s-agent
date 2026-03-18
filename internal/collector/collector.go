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
	"k8s-agent/internal/clusterid"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

var collectorLog = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

type ClusterMetricsPayload struct {
	ClusterID   string            `json:"cluster_id"`
	ClusterName string            `json:"cluster_name,omitempty"`
	Timestamp   string            `json:"timestamp"`
	Nodes       []NodeMetrics     `json:"nodes"`
	AccountID   string            `json:"account_id"` // Cloud account ID (AWS account, GCP project, or Azure subscription)
	Karpenter   *KarpenterMetrics `json:"karpenter,omitempty"`
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
	Name           string          `json:"name"`
	Architecture   string          `json:"architecture"`
	KubeletVersion string          `json:"kubelet_version"`
	OSImage        string          `json:"os_image"`
	Provider       string          `json:"provider,omitempty"`
	InstanceType   string          `json:"instance_type,omitempty"`
	InstanceID     string          `json:"instance_id,omitempty"`
	Zone           string          `json:"zone,omitempty"`
	Region         string          `json:"region,omitempty"`
	ProjectID      string          `json:"project_id,omitempty"`
	CapacityType   string          `json:"capacity_type,omitempty"`
	NodePoolName   string          `json:"node_pool_name,omitempty"`
	NodeClaimName  string          `json:"node_claim_name,omitempty"`
	Capacity       ResourceMetrics `json:"capacity"`
	Allocatable    ResourceMetrics `json:"allocatable"`
	Usage          ResourceMetrics `json:"usage"`
	Conditions     []NodeCondition `json:"conditions"`
	Pods           []PodSummary    `json:"pods"`
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
	Namespace      string            `json:"namespace"`
	Name           string            `json:"name"`
	Node           string            `json:"node"`
	Phase          string            `json:"phase"`
	QOSClass       string            `json:"qos_class"`
	Ready          bool              `json:"ready"`
	RestartCount   int               `json:"restart_count"`
	StartTime      *string           `json:"start_time,omitempty"`
	Containers     []ContainerInfo   `json:"containers"`
	Requests       ResourceMetrics   `json:"requests"`
	Limits         ResourceMetrics   `json:"limits"`
	Usage          *ResourceMetrics  `json:"usage,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	OwnerReference *string           `json:"owner_reference,omitempty"`
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

func Collect(ctx context.Context, client *kubernetes.Clientset, clusterID string, metricsClient *metricsclient.Clientset) ClusterMetricsPayload {
	ts := time.Now().UTC().Format(time.RFC3339)
	empty := ClusterMetricsPayload{
		Timestamp: ts,
		Nodes:     []NodeMetrics{},
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
	for _, pod := range pods.Items {
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
		}
		podsByNode[pod.Spec.NodeName] = append(podsByNode[pod.Spec.NodeName], podSummary)
	}

	collectorLog.Debug("pods_collected", "count", len(pods.Items))

	clusterAccountID := ""
	for _, node := range nodes.Items {
		if id := cloud.AccountID(node.Spec.ProviderID); id != "" {
			collectorLog.Info("account_id_from_provider_id", "node", node.Name, "account_id", id)
			clusterAccountID = id
			break
		}
	}
	if clusterAccountID == "" && len(nodes.Items) > 0 {
		id, source := cloud.ResolveAccountID(ctx, nodes.Items[0].Spec.ProviderID)
		if id != "" {
			collectorLog.Info("account_id_resolved", "source", source, "account_id", id)
			clusterAccountID = id
		} else {
			collectorLog.Warn("account_id_empty", "hint", "set EKS_ACCOUNT_ID env var, configure IRSA (AWS_ROLE_ARN), set IMDS hop limit >= 2, or ensure providerID contains account info (GCP/Azure)")
		}
	}

	zeroQuantity := resource.MustParse("0")
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
		if nodeUsage, ok := nodeUsageMap[node.Name]; ok {
			usageCPU = *resource.NewMilliQuantity(nodeUsage.cpuMilli, resource.DecimalSI)
			usageMem = *resource.NewQuantity(nodeUsage.memBytes, resource.BinarySI)
		}
		nodeList = append(nodeList, NodeMetrics{
			Name:          node.Name,
			Architecture:  nodeInfo.Architecture,
			KubeletVersion: nodeInfo.KubeletVersion,
			OSImage:       nodeInfo.OSImage,
			Provider:      provider,
			InstanceType:  instanceType,
			InstanceID:    instanceID,
			Zone:          zone,
			Region:        region,
			ProjectID:     cloud.ProjectID(node.Spec.ProviderID),
			CapacityType:  node.Labels["karpenter.sh/capacity-type"],
			NodePoolName:  node.Labels["karpenter.sh/nodepool"],
			NodeClaimName: node.Labels["karpenter.sh/nodeclaim"],
			Capacity:      resourceMetricsFromQuantities(capacityCPU, capacityMemory),
			Allocatable:   resourceMetricsFromQuantities(allocatableCPU, allocatableMemory),
			Usage:         resourceMetricsFromQuantities(usageCPU, usageMem),
			Conditions:    nodeConditionsFromK8s(node.Status.Conditions),
			Pods:          nodePods,
		})
	}

	payload := ClusterMetricsPayload{
		ClusterID:   clusterID,
		ClusterName: clusterid.ResolveName(nodes.Items),
		Timestamp:   ts,
		Nodes:       nodeList,
		AccountID:   clusterAccountID,
	}
	for _, node := range payload.Nodes {
		collectorLog.Debug("payload_node",
			"node", node.Name,
			"pods", len(node.Pods),
			"usage_cpu", node.Usage.CPU,
			"usage_memory", node.Usage.Memory)
	}
	collectorLog.Debug("collect_done", "nodes", len(payload.Nodes))
	return payload
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
