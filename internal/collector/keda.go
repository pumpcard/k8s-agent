package collector

import (
	"context"
	"log/slog"
	"os"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
)

var kedaLog = slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

type ScaledObjectTrigger struct {
	Type string `json:"type"`
	Name string `json:"name,omitempty"`
}

type ScaledObjectInfo struct {
	Namespace        string                `json:"namespace"`
	Name             string                `json:"name"`
	TargetKind       string                `json:"target_kind,omitempty"`
	TargetName       string                `json:"target_name,omitempty"`
	TargetAPIVersion string                `json:"target_api_version,omitempty"`
	MinReplicaCount  *int64                `json:"min_replica_count,omitempty"`
	MaxReplicaCount  *int64                `json:"max_replica_count,omitempty"`
	Triggers         []ScaledObjectTrigger `json:"triggers,omitempty"`
}

type KEDAMetrics struct {
	CollectedAt   string             `json:"collected_at"`
	ClusterID     string             `json:"cluster_id"`
	ScaledObjects []ScaledObjectInfo `json:"scaled_objects"`
}

var scaledObjectGVR = schema.GroupVersionResource{
	Group:    "keda.sh",
	Version:  "v1alpha1",
	Resource: "scaledobjects",
}

func CollectScaledObjects(ctx context.Context, dynClient dynamic.Interface, clusterID string) *KEDAMetrics {
	ts := time.Now().UTC().Format(time.RFC3339)
	metrics := &KEDAMetrics{
		CollectedAt:   ts,
		ClusterID:     clusterID,
		ScaledObjects: []ScaledObjectInfo{},
	}

	if dynClient == nil {
		return metrics
	}

	list, err := dynClient.Resource(scaledObjectGVR).List(ctx, metav1.ListOptions{})
	if err != nil {
		kedaLog.Warn("scaledobject_list_failed", "error", err)
		return metrics
	}

	for i := range list.Items {
		info := scaledObjectFromUnstructured(&list.Items[i])
		kedaLog.Debug("scaledobject_collected",
			"namespace", info.Namespace,
			"name", info.Name,
			"target", info.TargetKind+"/"+info.TargetName,
			"triggers", len(info.Triggers),
		)
		metrics.ScaledObjects = append(metrics.ScaledObjects, info)
	}

	kedaLog.Info("scaled_objects_collected", "count", len(metrics.ScaledObjects))
	return metrics
}

func scaledObjectFromUnstructured(obj *unstructured.Unstructured) ScaledObjectInfo {
	info := ScaledObjectInfo{
		Namespace: obj.GetNamespace(),
		Name:      obj.GetName(),
	}

	if kind, ok, _ := unstructured.NestedString(obj.Object, "spec", "scaleTargetRef", "kind"); ok {
		info.TargetKind = kind
	}
	if name, ok, _ := unstructured.NestedString(obj.Object, "spec", "scaleTargetRef", "name"); ok {
		info.TargetName = name
	}
	if apiVersion, ok, _ := unstructured.NestedString(obj.Object, "spec", "scaleTargetRef", "apiVersion"); ok {
		info.TargetAPIVersion = apiVersion
	}
	if min, ok, _ := unstructured.NestedInt64(obj.Object, "spec", "minReplicaCount"); ok {
		info.MinReplicaCount = &min
	}
	if max, ok, _ := unstructured.NestedInt64(obj.Object, "spec", "maxReplicaCount"); ok {
		info.MaxReplicaCount = &max
	}

	triggers, ok, _ := unstructured.NestedSlice(obj.Object, "spec", "triggers")
	if ok {
		for _, t := range triggers {
			triggerMap, ok := t.(map[string]interface{})
			if !ok {
				continue
			}
			trigger := ScaledObjectTrigger{}
			if typ, ok, _ := unstructured.NestedString(triggerMap, "type"); ok {
				trigger.Type = typ
			}
			if name, ok, _ := unstructured.NestedString(triggerMap, "name"); ok {
				trigger.Name = name
			}
			info.Triggers = append(info.Triggers, trigger)
		}
	}

	return info
}
