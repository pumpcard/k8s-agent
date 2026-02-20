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

const defaultInterval = 15 * time.Minute

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

	for {
		ctx := context.Background()
		metrics := collector.Collect(ctx, client, exportCfg.ClusterID, exportCfg.CustomerID)
		jsonData, err := json.Marshal(metrics)
		if err != nil {
			fmt.Fprintf(os.Stderr, "marshal metrics: %v\n", err)
			time.Sleep(defaultInterval)
			continue
		}

		if exportCfg.Enabled {
			if err := exportClient.Export(exportCfg.Endpoint, exportCfg.ClusterID, exportCfg.CustomerID, jsonData); err != nil {
				fmt.Fprintf(os.Stderr, "metrics export: %v\n", err)
			}
		}

		time.Sleep(defaultInterval)
	}
}
