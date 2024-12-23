package main

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
	"strconv"

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

type targetInfo struct {
	ip        string
	nodeName  string
	podName   string
}

func init() {
	prometheus.MustRegister(pingRTTBest)
	prometheus.MustRegister(pingRTTWorst)
	prometheus.MustRegister(pingRTTMean)
	prometheus.MustRegister(pingRTTStdDev)
	prometheus.MustRegister(pingLossRatio)
	prometheus.MustRegister(pingUp)
}

func getPodIPs(clientset *kubernetes.Clientset, namespace, currentPodIP string) ([]targetInfo, error) {
	pods, err := clientset.CoreV1().Pods(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: "app=ping-exporter",
	})
	if err != nil {
		return nil, err
	}

	var targets []targetInfo
	for _, pod := range pods.Items {
		if pod.Status.PodIP != "" && pod.Status.PodIP != currentPodIP {
			targets = append(targets, targetInfo{
				ip:       pod.Status.PodIP,
				nodeName: pod.Spec.NodeName,
				podName:  pod.Name,
			})
		}
	}
	return targets, nil
}

func getAdditionalIPs(clientset *kubernetes.Clientset, namespace string) ([]targetInfo, error) {
	configMapName := os.Getenv("CONFIG_MAP_NAME")
	if configMapName == "" {
		configMapName = "ping-exporter-config"
	}

	configMap, err := clientset.CoreV1().ConfigMaps(namespace).Get(context.Background(), configMapName, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}

	var targets []targetInfo
	if ips, ok := configMap.Data["additional_ips"]; ok {
		for _, ip := range strings.Split(ips, ",") {
			ip = strings.TrimSpace(ip)
			if ip != "" {
				targets = append(targets, targetInfo{
					ip:       ip,
					nodeName: "external",
					podName:  "external",
				})
			}
		}
	}
	return targets, nil
}

func getAllTargets(clientset *kubernetes.Clientset, namespace, sourceIP string) ([]targetInfo, error) {
    podTargets, err := getPodIPs(clientset, namespace, sourceIP)
    if err != nil {
        return nil, fmt.Errorf("error getting pod IPs: %v", err)
    }

    additionalTargets, err := getAdditionalIPs(clientset, namespace)
    if err != nil {
        log.Printf("Warning: error getting additional IPs: %v", err)
        // Continue with just pod targets if additional IPs fail
    }

    return append(podTargets, additionalTargets...), nil
}

func pingTarget(target string) (*probing.Statistics, error) {
	pinger, err := probing.NewPinger(target)
	if err != nil {
		return nil, err
	}
	pinger.Count = 10
	pinger.Timeout = time.Second * 4
	pinger.SetPrivileged(true)
	err = pinger.Run()
	if err != nil {
		return nil, err
	}
	return pinger.Statistics(), nil
}

func updateMetrics(sourceIP, sourceNode, sourcePod string, target targetInfo, stats *probing.Statistics) {
	labels := prometheus.Labels{
		"source":          sourceIP,
		"destination":     target.ip,
		"source_nodename": sourceNode,
		"dest_nodename":   target.nodeName,
		"source_podname":  sourcePod,
	}

	if stats.PacketsRecv > 0 {
		pingUp.With(labels).Set(1)
		pingLossRatio.With(labels).Set(float64(stats.PacketLoss) / 100)
		pingRTTBest.With(labels).Set(float64(stats.MinRtt) / float64(time.Second))
		pingRTTWorst.With(labels).Set(float64(stats.MaxRtt) / float64(time.Second))
		pingRTTMean.With(labels).Set(float64(stats.AvgRtt) / float64(time.Second))
		pingRTTStdDev.With(labels).Set(float64(stats.StdDevRtt) / float64(time.Second))
	} else {
		pingUp.With(labels).Set(0)
		pingLossRatio.With(labels).Set(1)
		pingRTTBest.Delete(labels)
		pingRTTWorst.Delete(labels)
		pingRTTMean.Delete(labels)
		pingRTTStdDev.Delete(labels)
	}
}

func main() {
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatalf("Failed to get cluster config: %v", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatalf("Failed to create client: %v", err)
	}

	sourceIP := os.Getenv("POD_IP")
	sourceNode := os.Getenv("NODE_NAME")
	sourcePod := os.Getenv("MY_POD_NAME")
	namespace := os.Getenv("MY_POD_NAMESPACE")

	if sourceNode == "" {
		sourceNode = "unknown"
	}
	if sourcePod == "" {
		sourcePod = "unknown"
	}

	go func() {
		http.Handle("/metrics", promhttp.Handler())
		log.Fatal(http.ListenAndServe(":9107", nil))
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGTERM, syscall.SIGINT)

	intervalSeconds, err := strconv.Atoi(os.Getenv("CHECK_INTERVAL_SECONDS"))
	if err != nil || intervalSeconds <= 0 {
		intervalSeconds = 15
	}
	ticker := time.NewTicker(time.Duration(intervalSeconds) * time.Second)
	defer ticker.Stop()

	var previousTargets sync.Map
	var loopCounter int
    var cachedTargets []targetInfo

	for {
		select {
		case <-sigChan:
			log.Println("Shutting down...")
			return
		case <-ticker.C:
			loopCounter++
            var targets []targetInfo
            var err error

            if loopCounter%4 == 1 {
                log.Println("Refreshing targets from Kubernetes API")
                cachedTargets, err = getAllTargets(clientset, namespace, sourceIP)
                if err != nil {
                    log.Printf("Error refreshing targets: %v", err)
                    continue
                }
            } else {
                log.Printf("Using cached targets (refresh in %d iterations)", 4-(loopCounter%4))
            }
            
            targets = cachedTargets

			var wg sync.WaitGroup
			for _, target := range targets {
				wg.Add(1)
				go func(t targetInfo) {
					defer wg.Done()
					log.Printf("Pinging target: %s from source IP: %s", t.ip, sourceIP)
					stats, err := pingTarget(t.ip)
					if err != nil {
						log.Printf("Error pinging %s: %v", t.ip, err)
						return
					}
					log.Printf("Ping result for target %s: Packets received: %d, Packet Loss: %0.2f%%", t.ip, stats.PacketsRecv, stats.PacketLoss)
					updateMetrics(sourceIP, sourceNode, sourcePod, t, stats)
					previousTargets.Store(t.ip, t.nodeName)
				}(target)
			}
			wg.Wait()

			// Clean up obsolete metrics
			previousTargets.Range(func(key, value interface{}) bool {
				targetIP := key.(string)
				found := false
				for _, t := range targets {
					if t.ip == targetIP {
						found = true
						break
					}
				}
				if !found {
					log.Printf("Removing obsolete target from metrics: %s", targetIP)
					previousTargets.Delete(targetIP)
					nodeName, _ := value.(string)
					labels := prometheus.Labels{
						"source":          sourceIP,
						"destination":     targetIP,
						"source_nodename": sourceNode,
						"dest_nodename":   nodeName,
						"source_podname":  sourcePod,
					}
					pingUp.Delete(labels)
					pingLossRatio.Delete(labels)
					pingRTTBest.Delete(labels)
					pingRTTWorst.Delete(labels)
					pingRTTMean.Delete(labels)
					pingRTTStdDev.Delete(labels)
				}
				return true
			})
		}
	}
}