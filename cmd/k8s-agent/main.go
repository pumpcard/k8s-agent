package main

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"time"

	"k8s-agent/internal/clusterid"
	"k8s-agent/internal/collector"
	"k8s-agent/internal/exporter"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const (
	collectionInterval = 1 * time.Hour
)

func main() {
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.BuildConfigFromFlags("", os.Getenv("KUBECONFIG"))
		if err != nil {
			log.Error("kubernetes config", "error", err)
			os.Exit(1)
		}
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		log.Error("kubernetes client", "error", err)
		os.Exit(1)
	}
	var metricsClient *metricsclient.Clientset
	if mc, err := metricsclient.NewForConfig(cfg); err == nil {
		metricsClient = mc
	}

	exportCfg := exporter.ConfigFromEnv()
	clusterID := clusterid.FromKubeSystem(context.Background(), client)
	if clusterID == "" {
		clusterID = "unknown"
	}
	exportClient := exporter.NewClient(exportCfg)

	log.Info("k8s-agent started", "cluster", clusterID)

	for {
		ctx := context.Background()
		metrics := collector.Collect(ctx, client, clusterID, metricsClient)
		jsonData, err := json.Marshal(metrics)
		if err != nil {
			log.Error("marshal metrics", "error", err)
			time.Sleep(collectionInterval)
			continue
		}

		nodeCount := len(metrics.Nodes)
		totalPods := 0
		for i := range metrics.Nodes {
			totalPods += len(metrics.Nodes[i].Pods)
		}
		log.Info("payload", "bytes", len(jsonData), "nodes", nodeCount, "pods", totalPods)

		if exportCfg.Enabled {
			log.Info("exporting", "endpoint", exportCfg.Endpoint)
			if err := exportClient.Export(exportCfg.Endpoint, clusterID, jsonData); err != nil {
				log.Error("metrics export failed", "error", err)
			} else {
				log.Info("metrics export ok")
			}
		}

		time.Sleep(collectionInterval)
	}
}
