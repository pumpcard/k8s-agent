package collector

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"time"

	autoscalingv2 "k8s.io/api/autoscaling/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

var hpaLog = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

type HPAMetricTarget struct {
	Type               string `json:"type"`
	Name               string `json:"name,omitempty"`
	TargetUtilization  *int32 `json:"target_utilization,omitempty"`
	TargetAverageValue string `json:"target_average_value,omitempty"`
	TargetValue        string `json:"target_value,omitempty"`
}

type HPAInfo struct {
	Namespace       string            `json:"namespace"`
	Name            string            `json:"name"`
	ResourceVersion string            `json:"resource_version"`
	TargetKind      string            `json:"target_kind"`
	TargetName      string            `json:"target_name"`
	MinReplicas     *int32            `json:"min_replicas,omitempty"`
	MaxReplicas     int32             `json:"max_replicas"`
	CurrentReplicas int32             `json:"current_replicas"`
	DesiredReplicas int32             `json:"desired_replicas"`
	MetricTargets   []HPAMetricTarget `json:"metric_targets,omitempty"`
}

type HPAMetrics struct {
	CollectedAt string    `json:"collected_at"`
	ClusterID   string    `json:"cluster_id"`
	HPAs        []HPAInfo `json:"hpas"`
}

func CollectHPAs(ctx context.Context, client kubernetes.Interface, clusterID string) *HPAMetrics {
	ts := time.Now().UTC().Format(time.RFC3339)
	metrics := &HPAMetrics{
		CollectedAt: ts,
		ClusterID:   clusterID,
		HPAs:        []HPAInfo{},
	}

	hpaList, err := client.AutoscalingV2().HorizontalPodAutoscalers("").List(ctx, metav1.ListOptions{})
	if err != nil {
		hpaLog.Warn("hpa_list_failed", "error", err)
		return metrics
	}

	for i := range hpaList.Items {
		hpa := &hpaList.Items[i]
		info := HPAInfo{
			Namespace:       hpa.Namespace,
			Name:            hpa.Name,
			ResourceVersion: hpa.ResourceVersion,
			TargetKind:      hpa.Spec.ScaleTargetRef.Kind,
			TargetName:      hpa.Spec.ScaleTargetRef.Name,
			MinReplicas:     hpa.Spec.MinReplicas,
			MaxReplicas:     hpa.Spec.MaxReplicas,
			CurrentReplicas: hpa.Status.CurrentReplicas,
			DesiredReplicas: hpa.Status.DesiredReplicas,
			MetricTargets:   extractMetricTargets(hpa.Spec.Metrics),
		}

		hpaLog.Debug("hpa_collected",
			"namespace", info.Namespace,
			"name", info.Name,
			"target", info.TargetKind+"/"+info.TargetName,
			"min", info.MinReplicas,
			"max", info.MaxReplicas,
		)
		metrics.HPAs = append(metrics.HPAs, info)
	}

	hpaLog.Info("hpas_collected", "count", len(metrics.HPAs))
	return metrics
}

func extractMetricTargets(specs []autoscalingv2.MetricSpec) []HPAMetricTarget {
	out := make([]HPAMetricTarget, 0, len(specs))
	for i := range specs {
		spec := &specs[i]
		mt := HPAMetricTarget{Type: string(spec.Type)}

		switch spec.Type {
		case autoscalingv2.ResourceMetricSourceType:
			if spec.Resource != nil {
				mt.Name = string(spec.Resource.Name)
				mt.TargetUtilization = spec.Resource.Target.AverageUtilization
				if spec.Resource.Target.AverageValue != nil {
					mt.TargetAverageValue = spec.Resource.Target.AverageValue.String()
				}
			}
		case autoscalingv2.PodsMetricSourceType:
			if spec.Pods != nil {
				mt.Name = spec.Pods.Metric.Name
				if spec.Pods.Target.AverageValue != nil {
					mt.TargetAverageValue = spec.Pods.Target.AverageValue.String()
				}
			}
		case autoscalingv2.ObjectMetricSourceType:
			if spec.Object != nil {
				mt.Name = spec.Object.Metric.Name
				if spec.Object.Target.Value != nil {
					mt.TargetValue = spec.Object.Target.Value.String()
				}
				if spec.Object.Target.AverageValue != nil {
					mt.TargetAverageValue = spec.Object.Target.AverageValue.String()
				}
			}
		case autoscalingv2.ExternalMetricSourceType:
			if spec.External != nil {
				mt.Name = spec.External.Metric.Name
				if spec.External.Target.Value != nil {
					mt.TargetValue = spec.External.Target.Value.String()
				}
				if spec.External.Target.AverageValue != nil {
					mt.TargetAverageValue = spec.External.Target.AverageValue.String()
				}
			}
		default:
			mt.Name = fmt.Sprintf("unknown-%s", spec.Type)
		}

		out = append(out, mt)
	}
	return out
}
