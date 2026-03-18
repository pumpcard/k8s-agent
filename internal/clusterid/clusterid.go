package clusterid

import (
	"context"
	"os"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const kubeSystemNamespace = "kube-system"

// EKS node label keys that may contain the cluster name.
var eksClusterNameLabels = []string{
	"alpha.eksctl.io/cluster-name",
}

// eksClusterLabelPrefix is scanned as a key prefix; the cluster name is
// encoded in the label key itself (e.g. "eks.amazonaws.com/cluster/<name>").
const eksClusterLabelPrefix = "eks.amazonaws.com/cluster/"

// FromKubeSystem returns a stable cluster identifier by reading the UID of the
// kube-system namespace. This is unique per cluster and does not require
// cloud-provider APIs. Returns empty string on error.
func FromKubeSystem(ctx context.Context, client kubernetes.Interface) string {
	ns, err := client.CoreV1().Namespaces().Get(ctx, kubeSystemNamespace, metav1.GetOptions{})
	if err != nil {
		return ""
	}
	return string(ns.UID)
}

// ResolveName returns a human-readable cluster name.
// Priority: CLUSTER_NAME env var > EKS node labels > empty string.
func ResolveName(nodes []corev1.Node) string {
	if name := strings.TrimSpace(os.Getenv("CLUSTER_NAME")); name != "" {
		return name
	}
	for i := range nodes {
		if name := clusterNameFromLabels(nodes[i].Labels); name != "" {
			return name
		}
	}
	return ""
}

func clusterNameFromLabels(labels map[string]string) string {
	for _, key := range eksClusterNameLabels {
		if v := labels[key]; v != "" {
			return v
		}
	}
	for key := range labels {
		if strings.HasPrefix(key, eksClusterLabelPrefix) {
			if name := strings.TrimPrefix(key, eksClusterLabelPrefix); name != "" {
				return name
			}
		}
	}
	return ""
}
