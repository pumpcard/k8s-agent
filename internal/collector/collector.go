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
	HPAs        *HPAMetrics       `json:"hpas,omitempty"`
	KEDA        *KEDAMetrics      `json:"keda,omitempty"`
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
	Name         string           `json:"name"`
	Image        string           `json:"image"`
	Ready        bool             `json:"ready"`
	RestartCount int              `json:"restart_count"`
	State        string           `json:"state"` // running | waiting | terminated | pending
	Reason       *string          `json:"reason,omitempty"`
	Message      *string          `json:"message,omitempty"`
	LastState    *string          `json:"last_state,omitempty"`    // terminated | nil (only set when last termination info exists)
	LastReason   *string          `json:"last_reason,omitempty"`   // e.g. "OOMKilled", "Error"
	LastExitCode *int32           `json:"last_exit_code,omitempty"`
	LastMessage  *string          `json:"last_message,omitempty"`
	Requests     ResourceMetrics  `json:"requests"`
	Limits       ResourceMetrics  `json:"limits"`
	Usage        *ResourceMetrics `json:"usage,omitempty"` // nil when metrics-server has no sample for this container
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
	InitContainers []ContainerInfo   `json:"init_containers,omitempty"`
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

// resourceMetricsFromResourceList reads a single container's request or limit list.
// A resource absent from the list yields a zero quantity (no request/limit set).
func resourceMetricsFromResourceList(resourceList corev1.ResourceList) ResourceMetrics {
	return resourceMetricsFromQuantities(resourceList[corev1.ResourceCPU], resourceList[corev1.ResourceMemory])
}

// resourceMetricsFromMilliBytes turns a (cpu millicores, memory bytes) pair — a usage sample, or a sum
// of several containers' requests/limits — into the API's ResourceMetrics shape.
func resourceMetricsFromMilliBytes(cpuMillicores, memoryBytes int64) ResourceMetrics {
	return resourceMetricsFromQuantities(
		*resource.NewMilliQuantity(cpuMillicores, resource.DecimalSI),
		*resource.NewQuantity(memoryBytes, resource.BinarySI),
	)
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

// controllerOwner returns the controlling owner reference, or the first owner if none is marked controller.
func controllerOwner(owners []metav1.OwnerReference) *metav1.OwnerReference {
	for i := range owners {
		if owners[i].Controller != nil && *owners[i].Controller {
			return &owners[i]
		}
	}
	if len(owners) == 0 {
		return nil
	}
	return &owners[0]
}

// replicaSetToDeployment maps "namespace/replicasetName" -> deployment name when the RS is owned by a Deployment.
func replicaSetToDeploymentMap(ctx context.Context, client kubernetes.Interface) map[string]string {
	out := make(map[string]string)
	rsList, err := client.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		collectorLog.Warn("replicasets_list_failed", "error", err)
		return out
	}
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		key := rs.Namespace + "/" + rs.Name
		var deployName string
		for j := range rs.OwnerReferences {
			ref := rs.OwnerReferences[j]
			if ref.Kind != "Deployment" {
				continue
			}
			if ref.Controller != nil && *ref.Controller {
				deployName = ref.Name
				break
			}
			if deployName == "" {
				deployName = ref.Name
			}
		}
		if deployName != "" {
			out[key] = deployName
		}
	}
	return out
}

// podWorkloadOwner reports the logical workload owner: Deployment for pods under a Deployment (via ReplicaSet),
// otherwise the controller owner kind/name (e.g. StatefulSet, DaemonSet, Job).
func podWorkloadOwner(owners []metav1.OwnerReference, namespace string, rsToDeploy map[string]string) *string {
	ctrl := controllerOwner(owners)
	if ctrl == nil {
		return nil
	}
	if ctrl.Kind == "ReplicaSet" {
		if deployName := rsToDeploy[namespace+"/"+ctrl.Name]; deployName != "" {
			s := "Deployment/" + deployName
			return &s
		}
	}
	s := ctrl.Kind + "/" + ctrl.Name
	return &s
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
		if last := status.LastTerminationState.Terminated; last != nil {
			state := "terminated"
			containerInfo.LastState = &state
			containerInfo.LastReason = &last.Reason
			containerInfo.LastExitCode = &last.ExitCode
			if last.Message != "" {
				containerInfo.LastMessage = &last.Message
			}
		}
	}
	return containerInfo
}

// buildContainerInfos builds one ContainerInfo per container spec (regular or init — the shape is the
// same for both), filling in its requests/limits from the spec and its usage, if metrics-server reported
// any, from containerUsage. Returns nil for an empty containerSpecs (so an empty InitContainers list
// stays the omitempty zero value instead of an allocated empty slice).
func buildContainerInfos(
	containerSpecs []corev1.Container,
	statuses []corev1.ContainerStatus,
	containerUsage map[string]resourceUsage,
) []ContainerInfo {
	if len(containerSpecs) == 0 {
		return nil
	}
	statusByName := make(map[string]*corev1.ContainerStatus, len(statuses))
	for index := range statuses {
		statusByName[statuses[index].Name] = &statuses[index]
	}
	infos := make([]ContainerInfo, 0, len(containerSpecs))
	for _, spec := range containerSpecs {
		info := containerInfoFromStatus(spec, statusByName[spec.Name])
		info.Requests = resourceMetricsFromResourceList(spec.Resources.Requests)
		info.Limits = resourceMetricsFromResourceList(spec.Resources.Limits)
		if usage, ok := containerUsage[spec.Name]; ok {
			usageMetrics := resourceMetricsFromMilliBytes(usage.cpuMilli, usage.memBytes)
			info.Usage = &usageMetrics
		}
		infos = append(infos, info)
	}
	return infos
}

// sumContainerResourceMetrics sums each container's already-built Requests/Limits into pod-level totals,
// so the pod-level numbers are always consistent with the per-container ones — one computation over the
// container specs, not two separate ones.
func sumContainerResourceMetrics(containers []ContainerInfo) (requests, limits ResourceMetrics) {
	var reqCPU, reqMem, limCPU, limMem int64
	for _, c := range containers {
		reqCPU += c.Requests.CPUMillicores
		reqMem += c.Requests.MemoryBytes
		limCPU += c.Limits.CPUMillicores
		limMem += c.Limits.MemoryBytes
	}
	return resourceMetricsFromMilliBytes(reqCPU, reqMem), resourceMetricsFromMilliBytes(limCPU, limMem)
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

func Collect(ctx context.Context, client kubernetes.Interface, clusterID string, metricsClient metricsclient.Interface) ClusterMetricsPayload {
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

	rsToDeploy := replicaSetToDeploymentMap(ctx, client)

	nodeUsageMap := make(map[string]resourceUsage)
	// podContainerUsageMap: "namespace/pod" -> container name -> usage. metrics-server reports
	// per-container usage already (podMetrics.Containers); keep it per-container instead of summing
	// it away, so a sidecar's usage doesn't hide inside the app container's number.
	podContainerUsageMap := make(map[string]map[string]resourceUsage)
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
		}
		podMetricsList, err := metricsClient.MetricsV1beta1().PodMetricses("").List(ctx, metav1.ListOptions{})
		if err != nil {
			collectorLog.Warn("pod_metrics_list_failed", "error", err)
		} else {
			for index := range podMetricsList.Items {
				podMetrics := &podMetricsList.Items[index]
				podKey := podMetrics.Namespace + "/" + podMetrics.Name
				containerUsage := make(map[string]resourceUsage, len(podMetrics.Containers))
				for containerIdx := range podMetrics.Containers {
					containerMetrics := &podMetrics.Containers[containerIdx]
					containerUsage[containerMetrics.Name] = resourceUsage{
						cpuMilli: quantityToMilli(containerMetrics.Usage[corev1.ResourceCPU]),
						memBytes: quantityToBytes(containerMetrics.Usage[corev1.ResourceMemory]),
					}
				}
				podContainerUsageMap[podKey] = containerUsage
			}
			for podKey, containers := range podContainerUsageMap {
				var cpuMilli, memBytes int64
				for _, usage := range containers {
					cpuMilli += usage.cpuMilli
					memBytes += usage.memBytes
				}
				collectorLog.Debug("pod_usage", "pod", podKey, "cpu_millicores", cpuMilli, "memory_bytes", memBytes)
			}
		}
	} else {
		collectorLog.Debug("read_from", "source", "metrics.k8s.io", "msg", "skipped (no client); usage=0")
	}

	podsByNode := make(map[string][]PodSummary)
	for _, pod := range pods.Items {
		podKey := pod.Namespace + "/" + pod.Name
		containerUsage := podContainerUsageMap[podKey]

		restartCount := 0
		for index := range pod.Status.ContainerStatuses {
			restartCount += int(pod.Status.ContainerStatuses[index].RestartCount)
		}
		containers := buildContainerInfos(pod.Spec.Containers, pod.Status.ContainerStatuses, containerUsage)
		if containers == nil {
			containers = []ContainerInfo{} // Containers has no `omitempty`: always serialize as [], never null.
		}
		initContainers := buildContainerInfos(pod.Spec.InitContainers, pod.Status.InitContainerStatuses, containerUsage)
		podRequests, podLimits := sumContainerResourceMetrics(containers)

		var startTime *string
		if pod.Status.StartTime != nil {
			formattedStartTime := pod.Status.StartTime.Format(time.RFC3339)
			startTime = &formattedStartTime
		}
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
			InitContainers: initContainers,
			Requests:       podRequests,
			Limits:         podLimits,
			Usage:          nil,
			Labels:         labels,
			OwnerReference: podWorkloadOwner(pod.OwnerReferences, pod.Namespace, rsToDeploy),
		}
		// Pod-level usage stays the sum across every container metrics-server reports for this pod —
		// including a restartable (native sidecar) init container, exactly like before per-container data
		// existed (the old code summed podMetrics.Containers unconditionally). Summing only pod.Spec.Containers
		// here would silently drop a running native sidecar's usage from the pod total.
		if len(containerUsage) > 0 {
			var cpuMilli, memBytes int64
			for _, usage := range containerUsage {
				cpuMilli += usage.cpuMilli
				memBytes += usage.memBytes
			}
			podUsage := resourceMetricsFromMilliBytes(cpuMilli, memBytes)
			podSummary.Usage = &podUsage
		}
		podsByNode[pod.Spec.NodeName] = append(podsByNode[pod.Spec.NodeName], podSummary)
	}

	collectorLog.Debug("pods_collected", "count", len(pods.Items))

	clusterAccountID := ""
	for _, node := range nodes.Items {
		if id := cloud.AccountID(node.Spec.ProviderID); id != "" {
			clusterAccountID = id
			break
		}
	}
	if clusterAccountID == "" && len(nodes.Items) > 0 {
		clusterAccountID, _ = cloud.ResolveAccountID(ctx, nodes.Items[0].Spec.ProviderID)
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
			Name:           node.Name,
			Architecture:   nodeInfo.Architecture,
			KubeletVersion: nodeInfo.KubeletVersion,
			OSImage:        nodeInfo.OSImage,
			Provider:       provider,
			InstanceType:   instanceType,
			InstanceID:     instanceID,
			Zone:           zone,
			Region:         region,
			ProjectID:      cloud.ProjectID(node.Spec.ProviderID),
			CapacityType:   node.Labels["karpenter.sh/capacity-type"],
			NodePoolName:   node.Labels["karpenter.sh/nodepool"],
			NodeClaimName:  node.Labels["karpenter.sh/nodeclaim"],
			Capacity:       resourceMetricsFromQuantities(capacityCPU, capacityMemory),
			Allocatable:    resourceMetricsFromQuantities(allocatableCPU, allocatableMemory),
			Usage:          resourceMetricsFromQuantities(usageCPU, usageMem),
			Conditions:     nodeConditionsFromK8s(node.Status.Conditions),
			Pods:           nodePods,
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
