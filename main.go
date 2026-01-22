package main

import (
	"context"
	"fmt"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

func main() {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		panic(err)
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		panic(err)
	}
	for {
		ctx := context.Background()
		nodes, err := client.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
		if err != nil {
			panic(err)
		}
		fmt.Println("nodes")
		for _, n := range nodes.Items {
			fmt.Println(
				n.Name,
				n.Status.Allocatable.Cpu().String(),
				n.Status.Allocatable.Memory().String(),
			)
		}
		pods, err := client.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
		if err != nil {
			panic(err)
		}
		fmt.Println("pods")
		for _, p := range pods.Items {
			fmt.Println(
				p.Namespace,
				p.Name,
				p.Spec.NodeName,
			)
		}
		fmt.Println("----")
		time.Sleep(3 * time.Second)
	}
}
