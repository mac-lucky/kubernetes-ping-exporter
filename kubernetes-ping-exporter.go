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
	"github.com/rs/zerolog"
	"github.com/rs/zerolog/log"
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

	// Metric collectors for easy access
	metricCollectors = []prometheus.Collector{
		pingRTTBest,
		pingRTTWorst,
		pingRTTMean,
		pingRTTStdDev,
		pingLossRatio,
		pingUp,
	}
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

// MetricsState maintains the current state of metrics
type MetricsState struct {
	activeTargets map[string]PodInfo
	mutex         sync.RWMutex
}

func NewMetricsState() *MetricsState {
	return &MetricsState{
		activeTargets: make(map[string]PodInfo),
	}
}

func (ms *MetricsState) updateActiveTargets(targets []PodInfo) {
	ms.mutex.Lock()
	defer ms.mutex.Unlock()

	// Create new map for current targets
	newTargets := make(map[string]PodInfo)
	for _, target := range targets {
		newTargets[target.IP] = target
	}

	// Replace the active targets with new ones
	ms.activeTargets = newTargets
}

func (ms *MetricsState) cleanupMetrics(source PodInfo) {
	ms.mutex.RLock()
	defer ms.mutex.RUnlock()

	logger := log.With().
		Str("source", source.IP).
		Int("activeTargets", len(ms.activeTargets)).
		Logger()

	// Delete all existing metrics
	for _, collector := range metricCollectors {
		if vec, ok := collector.(*prometheus.GaugeVec); ok {
			vec.Reset()
		}
	}

	// Reinitialize metrics only for active targets
	for _, target := range ms.activeTargets {
		labels := prometheus.Labels{
			"source":          source.IP,
			"destination":     target.IP,
			"source_nodename": source.NodeName,
			"dest_nodename":   target.NodeName,
			"source_podname":  source.PodName,
		}

		// Initialize with default values
		pingUp.With(labels).Set(0)
		pingLossRatio.With(labels).Set(1.0)
		pingRTTBest.With(labels).Set(0)
		pingRTTWorst.With(labels).Set(0)
		pingRTTMean.With(labels).Set(0)
		pingRTTStdDev.With(labels).Set(0)
	}

	logger.Info().Msg("metrics cleaned up and reinitialized")
}

func init() {
	// Configure zerolog
	zerolog.TimeFieldFormat = zerolog.TimeFormatUnix
	log.Logger = log.Output(zerolog.ConsoleWriter{
		Out:        os.Stdout,
		TimeFormat: time.RFC3339,
	})

	// Register metrics with Prometheus
	for _, collector := range metricCollectors {
		prometheus.MustRegister(collector)
	}
}

func getPodIPs(clientset *kubernetes.Clientset, namespace, currentPodIP string) ([]PodInfo, error) {
	logger := log.With().
		Str("namespace", namespace).
		Str("currentPodIP", currentPodIP).
		Logger()

	logger.Debug().Msg("fetching pod IPs")

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

	logger.Info().
		Int("podCount", len(podInfos)).
		Msg("found target pods")

	return podInfos, nil
}

func getAdditionalIPs(clientset *kubernetes.Clientset, namespace string) ([]PodInfo, error) {
	logger := log.With().Str("namespace", namespace).Logger()
	
	configMapName := os.Getenv("CONFIG_MAP_NAME")
	if configMapName == "" {
		configMapName = "ping-exporter-config"
	}

	logger.Debug().
		Str("configMap", configMapName).
		Msg("fetching additional IPs from config map")

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

	logger.Info().
		Int("externalTargets", len(podInfos)).
		Msg("found external targets")

	return podInfos, nil
}

func pingTarget(target string) (*PingResult, error) {
	logger := log.With().Str("target", target).Logger()
	logger.Debug().Msg("starting ping sequence")

	pinger, err := probing.NewPinger(target)
	if err != nil {
		return nil, err
	}
	
	pinger.Count = 10
	pinger.Timeout = time.Second * 4
	pinger.SetPrivileged(true)

	err = pinger.Run()
	if err != nil {
		logger.Error().Err(err).Msg("ping failed")
		return &PingResult{Up: 0, Loss: 1.0}, nil
	}

	stats := pinger.Statistics()
	if stats.PacketsRecv == 0 {
		logger.Warn().Msg("no packets received")
		return &PingResult{Up: 0, Loss: 1.0}, nil
	}

	result := &PingResult{
		Up:       1,
		Loss:     float64(stats.PacketLoss) / 100,
		Best:     float64(stats.MinRtt) / float64(time.Second),
		Worst:    float64(stats.MaxRtt) / float64(time.Second),
		Mean:     float64(stats.AvgRtt) / float64(time.Second),
		StdDev:   float64(stats.StdDevRtt) / float64(time.Second),
		HasStats: true,
	}

	logger.Debug().
		Float64("loss", result.Loss).
		Float64("mean", result.Mean).
		Float64("best", result.Best).
		Float64("worst", result.Worst).
		Msg("ping statistics")

	return result, nil
}

func updateMetrics(source, target PodInfo, result *PingResult) {
	logger := log.With().
		Str("source", source.IP).
		Str("target", target.IP).
		Logger()

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
		logger.Debug().Msg("updated all metrics")
	} else {
		logger.Debug().Msg("updated reachability metrics only")
	}
}

func main() {
	log.Info().Msg("starting ping exporter")

	// Get Kubernetes configuration
	config, err := rest.InClusterConfig()
	if err != nil {
		log.Fatal().Err(err).Msg("failed to get cluster config")
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		log.Fatal().Err(err).Msg("failed to create Kubernetes client")
	}

	// Get environment variables
	sourcePodIP := os.Getenv("POD_IP")
	sourceNodeName := os.Getenv("NODE_NAME")
	sourcePodName := os.Getenv("MY_POD_NAME")
	namespace := os.Getenv("MY_POD_NAMESPACE")

	if sourcePodIP == "" || sourceNodeName == "" || sourcePodName == "" || namespace == "" {
		log.Fatal().Msg("required environment variables not set")
	}

	log.Info().
		Str("podIP", sourcePodIP).
		Str("nodeName", sourceNodeName).
		Str("podName", sourcePodName).
		Str("namespace", namespace).
		Msg("initialized with pod info")

	sourcePod := PodInfo{
		IP:       sourcePodIP,
		NodeName: sourceNodeName,
		PodName:  sourcePodName,
	}

	// Initialize metrics state
	metricsState := NewMetricsState()

	// Start Prometheus HTTP server
	go func() {
		http.Handle("/metrics", promhttp.Handler())
		server := &http.Server{Addr: ":9107"}
		log.Info().Msg("starting metrics server on :9107")
		if err := server.ListenAndServe(); err != nil {
			log.Fatal().Err(err).Msg("HTTP server error")
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
		case sig := <-sigChan:
			log.Info().
				Str("signal", sig.String()).
				Msg("received termination signal, shutting down")
			return
		case <-ticker.C:
			cycleStart := time.Now()
			log.Debug().Msg("starting new ping cycle")

			podTargets, err := getPodIPs(clientset, namespace, sourcePodIP)
			if err != nil {
				log.Error().Err(err).Msg("error getting pod IPs")
				continue
			}

			additionalTargets, err := getAdditionalIPs(clientset, namespace)
			if err != nil {
				log.Error().Err(err).Msg("error getting additional IPs")
				continue
			}

			// Combine all targets
			allTargets := append(podTargets, additionalTargets...)

			// Update active targets and clean up metrics
			metricsState.updateActiveTargets(allTargets)
			metricsState.cleanupMetrics(sourcePod)

			var wg sync.WaitGroup
			for _, target := range allTargets {
				wg.Add(1)
				go func(target PodInfo) {
					defer wg.Done()
					result, err := pingTarget(target.IP)
					if err != nil {
						log.Error().
							Err(err).
							Str("target", target.IP).
							Msg("error pinging target")
						return
					}
					result.Target = target
					updateMetrics(sourcePod, target, result)
				}(target)
			}

			wg.Wait()
			log.Info().
				Dur("duration", time.Since(cycleStart)).
				Int("totalTargets", len(allTargets)).
				Msg("ping cycle completed")
		}
	}
}