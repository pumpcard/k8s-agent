package clusterid

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const kubeSystemNamespace = "kube-system"

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
