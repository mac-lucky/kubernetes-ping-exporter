package main

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

var (
	pingRTTBest = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ping_rtt_best_seconds",
			Help: "Best round trip time",
		},
		[]string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
	)

	pingRTTWorst = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ping_rtt_worst_seconds",
			Help: "Worst round trip time",
		},
		[]string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
	)

	pingRTTMean = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ping_rtt_mean_seconds",
			Help: "Mean round trip time",
		},
		[]string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
	)

	pingRTTStdDev = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ping_rtt_std_deviation_seconds",
			Help: "Standard deviation of RTT",
		},
		[]string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
	)

	pingLossRatio = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ping_loss_ratio",
			Help: "Packet loss ratio",
		},
		[]string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
	)

	pingUp = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name: "ping_up",
			Help: "Target reachability status (1=up, 0=down)",
		},
		[]string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
	)
)

type PodInfo struct {
	IP       string
	NodeName string
	PodName  string
}

type PingResult struct {
	Up       float64
	Loss     float64
	Best     float64
	Worst    float64
	Mean     float64
	StdDev   float64
	Target   PodInfo
	HasStats bool
}

func init() {
	// Register metrics with Prometheus
	prometheus.MustRegister(pingRTTBest)
	prometheus.MustRegister(pingRTTWorst)
	prometheus.MustRegister(pingRTTMean)
	prometheus.MustRegister(pingRTTStdDev)
	prometheus.MustRegister(pingLossRatio)
	prometheus.MustRegister(pingUp)
}

func getPodIPs(clientset *kubernetes.Clientset, namespace, currentPodIP string) ([]PodInfo, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=ping-exporter",
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list pods: %v", err)
	}

	var podInfos []PodInfo
	for _, pod := range pods.Items {
		if pod.Status.PodIP != "" && pod.Status.PodIP != currentPodIP {
			podInfos = append(podInfos, PodInfo{
				IP:       pod.Status.PodIP,
				NodeName: pod.Spec.NodeName,
				PodName:  pod.Name,
			})
		}
	}
	return podInfos, nil
}

func getAdditionalIPs(clientset *kubernetes.Clientset, namespace string) ([]PodInfo, error) {
	configMapName := os.Getenv("CONFIG_MAP_NAME")
	if configMapName == "" {
		configMapName = "ping-exporter-config"
	}

	configMap, err := clientset.CoreV1().ConfigMaps(namespace).Get(context.Background(), configMapName, metav1.GetOptions{})
	if err != nil {
		return nil, fmt.Errorf("failed to get ConfigMap: %v", err)
	}

	additionalIPs := strings.Split(configMap.Data["additional_ips"], ",")
	var podInfos []PodInfo
	for _, ip := range additionalIPs {
		ip = strings.TrimSpace(ip)
		if ip != "" {
			podInfos = append(podInfos, PodInfo{
				IP:       ip,
				NodeName: "external",
				PodName:  "external",
			})
		}
	}
	return podInfos, nil
}

func pingTarget(target string) (*PingResult, error) {
	pinger, err := probing.NewPinger(target)
	if err != nil {
		return nil, err
	}
	
	pinger.Count = 10
	pinger.Timeout = time.Second * 4
	pinger.SetPrivileged(true)

	err = pinger.Run()
	if err != nil {
		return &PingResult{Up: 0, Loss: 1.0}, nil
	}

	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		return &PingResult{Up: 0, Loss: 1.0}, nil
	}

	return &PingResult{
		Up:       1,
		Loss:     float64(stats.PacketLoss) / 100,
		Best:     float64(stats.MinRtt) / float64(time.Second),
		Worst:    float64(stats.MaxRtt) / float64(time.Second),
		Mean:     float64(stats.AvgRtt) / float64(time.Second),
		StdDev:   float64(stats.StdDevRtt) / float64(time.Second),
		HasStats: true,
	}, nil
}

func updateMetrics(source, target PodInfo, result *PingResult) {
	labels := prometheus.Labels{
		"source":          source.IP,
		"destination":     target.IP,
		"source_nodename": source.NodeName,
		"dest_nodename":   target.NodeName,
		"source_podname":  source.PodName,
	}

	pingUp.With(labels).Set(result.Up)
	pingLossRatio.With(labels).Set(result.Loss)

	if result.HasStats {
		pingRTTBest.With(labels).Set(result.Best)
		pingRTTWorst.With(labels).Set(result.Worst)
		pingRTTMean.With(labels).Set(result.Mean)
		pingRTTStdDev.With(labels).Set(result.StdDev)
	}
}

func main() {
	// Get Kubernetes configuration
	config, err := rest.InClusterConfig()
	if err != nil {
		fmt.Printf("Failed to get cluster config: %v\n", err)
		os.Exit(1)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		fmt.Printf("Failed to create Kubernetes client: %v\n", err)
		os.Exit(1)
	}

	// Get environment variables
	sourcePodIP := os.Getenv("POD_IP")
	sourceNodeName := os.Getenv("NODE_NAME")
	sourcePodName := os.Getenv("MY_POD_NAME")
	namespace := os.Getenv("MY_POD_NAMESPACE")

	if sourcePodIP == "" || sourceNodeName == "" || sourcePodName == "" || namespace == "" {
		fmt.Println("Required environment variables not set")
		os.Exit(1)
	}

	sourcePod := PodInfo{
		IP:       sourcePodIP,
		NodeName: sourceNodeName,
		PodName:  sourcePodName,
	}

	// Start Prometheus HTTP server
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		server := &http.Server{Addr: ":9107"}
		if err := server.ListenAndServe(); err != nil {
			fmt.Printf("HTTP server error: %v\n", err)
			os.Exit(1)
		}
	}()

	// Set up signal handling
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	// Main loop
	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-sigChan:
			fmt.Println("Received termination signal, shutting down...")
			return
		case <-ticker.C:
			podTargets, err := getPodIPs(clientset, namespace, sourcePodIP)
			if err != nil {
				fmt.Printf("Error getting pod IPs: %v\n", err)
				continue
			}

			additionalTargets, err := getAdditionalIPs(clientset, namespace)
			if err != nil {
				fmt.Printf("Error getting additional IPs: %v\n", err)
				continue
			}

			targets := append(podTargets, additionalTargets...)
			var wg sync.WaitGroup

			for _, target := range targets {
				wg.Add(1)
				go func(target PodInfo) {
					defer wg.Done()
					result, err := pingTarget(target.IP)
					if err != nil {
						fmt.Printf("Error pinging %s: %v\n", target.IP, err)
						return
					}
					result.Target = target
					updateMetrics(sourcePod, target, result)
				}(target)
			}

			wg.Wait()
		}
	}
}