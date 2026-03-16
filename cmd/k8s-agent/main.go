package main

import (
	"context"
	"log/slog"
	"os"
	"strings"
	"time"

	"k8s-agent/internal/clusterid"
	"k8s-agent/internal/collector"
	"k8s-agent/internal/export"
	"k8s-agent/internal/pump"

	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	metricsclient "k8s.io/metrics/pkg/client/clientset/versioned"
)

const collectionInterval = 1 * time.Minute

func main() {
	logLevel := slog.LevelInfo
	if s := strings.TrimSpace(strings.ToLower(os.Getenv("LOG_LEVEL"))); s != "" {
		switch s {
		case "debug":
			logLevel = slog.LevelDebug
		case "info":
			logLevel = slog.LevelInfo
		case "warn", "warning":
			logLevel = slog.LevelWarn
		case "error":
			logLevel = slog.LevelError
		}
	}
	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: logLevel}))

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
	pumpCfg := pump.ConfigFromEnv()
	clusterID := clusterid.FromKubeSystem(context.Background(), client)
	if clusterID == "" {
		clusterID = "unknown"
	}
	pumpClient := pump.NewClient(pumpCfg)

	log.Info("k8s-agent started", "cluster", clusterID)

	for {
		ctx := context.Background()

		karpenterMetrics := collector.CollectKarpenter(ctx, client, clusterID)
		log.Info("karpenter collection", "events", len(karpenterMetrics.Events))

		exported, err := export.RunCycle(ctx, log, client, clusterID, metricsClient, pumpCfg, pumpClient)
		if err != nil {
			log.Error("metrics export failed", "error", err)
		} else if exported {
			log.Info("metrics export ok")
		}
		time.Sleep(collectionInterval)
	}
}
