package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"k8s-agent/internal/collector"
	"k8s-agent/internal/exporter"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
)

const (
	collectionInterval = 15 * time.Minute
)

func main() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		// Fall back to local kubeconfig for dev (e.g. KUBECONFIG or ~/.kube/config)
		cfg, err = clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
		if err != nil {
			fmt.Fprintf(os.Stderr, "kubernetes config: %v\n", err)
			os.Exit(1)
		}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kubernetes client: %v\n", err)
		os.Exit(1)
	}

	exportCfg := exporter.ConfigFromEnv()
	exportClient := exporter.NewClient(exportCfg)

	fmt.Fprintf(os.Stderr, "k8s-agent started (cluster=%s)\n", exportCfg.ClusterID)

	for {
		ctx := context.Background()
		metrics := collector.Collect(ctx, client, exportCfg.ClusterID, exportCfg.CustomerID)
		jsonData, err := json.Marshal(metrics)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal metrics: %v\n", err)
			time.Sleep(collectionInterval)
			continue
		}

		if exportCfg.Enabled {
			nodeCount := len(metrics.Nodes)
			fmt.Fprintf(os.Stderr, "sending payload: %d bytes, %d nodes", len(jsonData), nodeCount)
			if nodeCount > 0 {
				n := &metrics.Nodes[0]
				fmt.Fprintf(os.Stderr, " | first node: name=%s provider=%s instance_type=%s zone=%s region=%s",
					n.Name, n.Provider, n.InstanceType, n.Zone, n.Region)
			}
			fmt.Fprintf(os.Stderr, "\n")
			if err := exportClient.Export(exportCfg.Endpoint, exportCfg.ClusterID, exportCfg.CustomerID, jsonData); err != nil {
				fmt.Fprintf(os.Stderr, "metrics export: %v\n", err)
			} else {
				fmt.Fprintf(os.Stderr, "metrics export ok\n")
			}
		}

		time.Sleep(collectionInterval)
	}
}
