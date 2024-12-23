package main

import (
    "context"
    "fmt"
    "log"
    "net/http"
    "os"
    "strings"
    "sync"
    "time"

    probing "github.com/prometheus-community/pro-bing"
    "github.com/prometheus/client_golang/prometheus"
    "github.com/prometheus/client_golang/prometheus/promhttp"
    metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
    "k8s.io/client-go/kubernetes"
    "k8s.io/client-go/rest"
)

const (
    namespace           = "ping"
    checkIntervalStr    = "CHECK_INTERVAL_SECONDS"
    defaultInterval     = 15
    defaultConfigMap    = "ping-exporter-config"
    defaultMetricsPort = 9107
)

var (
    rttBest = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: namespace,
            Name:      "rtt_best_seconds",
            Help:      "Best round trip time in seconds",
        },
        []string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
    )

    rttWorst = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: namespace,
            Name:      "rtt_worst_seconds",
            Help:      "Worst round trip time in seconds",
        },
        []string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
    )

    rttMean = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: namespace,
            Name:      "rtt_mean_seconds",
            Help:      "Mean round trip time in seconds",
        },
        []string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
    )

    rttStdDev = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: namespace,
            Name:      "rtt_std_deviation_seconds",
            Help:      "Standard deviation of round trip time in seconds",
        },
        []string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
    )

    lossRatio = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: namespace,
            Name:      "loss_ratio",
            Help:      "Packet loss ratio (0-1)",
        },
        []string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
    )

    targetUp = prometheus.NewGaugeVec(
        prometheus.GaugeOpts{
            Namespace: namespace,
            Name:      "up",
            Help:      "Target reachability status (1=up, 0=down)",
        },
        []string{"source", "destination", "source_nodename", "dest_nodename", "source_podname"},
    )
)

type PingExporter struct {
    podIP        string
    nodeName     string
    podName      string
    namespace    string
    configMap    string
    clientset    *kubernetes.Clientset
    interval     time.Duration
    mutex        sync.RWMutex
    targets      map[string]bool  // Changed from slice to map for tracking active targets
    lastSeen     map[string]time.Time  // Track when each target was last seen
    nodeNames    map[string]string  // Map IP to node name
}

func init() {
    prometheus.MustRegister(rttBest)
    prometheus.MustRegister(rttWorst)
    prometheus.MustRegister(rttMean)
    prometheus.MustRegister(rttStdDev)
    prometheus.MustRegister(lossRatio)
    prometheus.MustRegister(targetUp)
}

func NewPingExporter() (*PingExporter, error) {
    podIP := os.Getenv("POD_IP")
    nodeName := os.Getenv("NODE_NAME")
    podName := os.Getenv("MY_POD_NAME")
    namespace := os.Getenv("MY_POD_NAMESPACE")
    configMap := os.Getenv("CONFIG_MAP_NAME")

    if podIP == "" || nodeName == "" || podName == "" || namespace == "" {
        return nil, fmt.Errorf("required environment variables not set")
    }

    if configMap == "" {
        configMap = defaultConfigMap
    }

    config, err := rest.InClusterConfig()
    if err != nil {
        return nil, fmt.Errorf("failed to get cluster config: %v", err)
    }

    clientset, err := kubernetes.NewForConfig(config)
    if err != nil {
        return nil, fmt.Errorf("failed to create kubernetes client: %v", err)
    }

    interval := defaultInterval
    if intervalStr := os.Getenv(checkIntervalStr); intervalStr != "" {
        if i, err := time.ParseDuration(intervalStr + "s"); err == nil {
            interval = int(i.Seconds())
        }
    }

    return &PingExporter{
        podIP:     podIP,
        nodeName:  nodeName,
        podName:   podName,
        namespace: namespace,
        configMap: configMap,
        clientset: clientset,
        interval:  time.Duration(interval) * time.Second,
        targets:   make(map[string]bool),
        lastSeen:  make(map[string]time.Time),
        nodeNames: make(map[string]string),
    }, nil
}

func (pe *PingExporter) cleanupOldMetrics() {
    log.Printf("Starting metrics cleanup...")
    pe.mutex.Lock()
    defer pe.mutex.Unlock()

    now := time.Now()
    staleThreshold := 3 * pe.interval // Consider a target stale after missing 3 intervals
    staleCount := 0

    // Check for stale targets
    for target, lastSeen := range pe.lastSeen {
        if now.Sub(lastSeen) > staleThreshold {
            log.Printf("Found stale target %s (last seen: %v)", target, lastSeen)
            // Delete metrics for stale target
            labels := prometheus.Labels{
                "source":          pe.podIP,
                "destination":     target,
                "source_nodename": pe.nodeName,
                "dest_nodename":   "unknown",
                "source_podname":  pe.podName,
            }

            rttBest.Delete(labels)
            rttWorst.Delete(labels)
            rttMean.Delete(labels)
            rttStdDev.Delete(labels)
            lossRatio.Delete(labels)
            targetUp.Delete(labels)

            // Remove target from tracking
            delete(pe.targets, target)
            delete(pe.lastSeen, target)

            log.Printf("Cleaned up metrics for stale target: %s", target)
            staleCount++
        }
    }
    
    log.Printf("Metrics cleanup complete. Removed %d stale targets", staleCount)
}

func (pe *PingExporter) updateTargets() error {
    log.Printf("Updating targets...")
    
    // Get pods with ping-exporter label
    pods, err := pe.clientset.CoreV1().Pods(pe.namespace).List(context.Background(), metav1.ListOptions{
        LabelSelector: "app=ping-exporter",
    })
    if err != nil {
        return fmt.Errorf("failed to list pods: %v", err)
    }
    log.Printf("Found %d pods with ping-exporter label", len(pods.Items))

    // Get additional IPs from ConfigMap
    cm, err := pe.clientset.CoreV1().ConfigMaps(pe.namespace).Get(context.Background(), pe.configMap, metav1.GetOptions{})
    if err != nil {
        log.Printf("Warning: failed to get ConfigMap %s: %v", pe.configMap, err)
    } else {
        log.Printf("Successfully retrieved ConfigMap %s", pe.configMap)
    }

    pe.mutex.Lock()
    defer pe.mutex.Unlock()

    oldTargets := len(pe.targets)
    // Reset current targets but keep the map
    for k := range pe.targets {
        pe.targets[k] = false
    }
    
    // Add pod IPs and update node name mapping
    now := time.Now()
    for _, pod := range pods.Items {
        if pod.Status.PodIP != pe.podIP {
            pe.targets[pod.Status.PodIP] = true
            pe.lastSeen[pod.Status.PodIP] = now
            pe.nodeNames[pod.Status.PodIP] = pod.Spec.NodeName
            log.Printf("Added pod target: IP=%s, Node=%s", pod.Status.PodIP, pod.Spec.NodeName)
        }
    }

    // Add IPs from ConfigMap
    if cm != nil {
        if ips, ok := cm.Data["additional_ips"]; ok {
            for _, ip := range strings.Split(ips, ",") {
                ip = strings.TrimSpace(ip)
                if ip != "" {
                    pe.targets[ip] = true
                    pe.lastSeen[ip] = now
                    log.Printf("Added ConfigMap target: IP=%s (external)", ip)
                    // External IPs won't have node names, they remain "unknown"
                }
            }
        }
    }

    log.Printf("Target update complete. Previous targets: %d, Current targets: %d", oldTargets, len(pe.targets))
    return nil
}

func (pe *PingExporter) pingTarget(target string) {
    log.Printf("Starting ping for target %s", target)
    
    pinger, err := probing.NewPinger(target)
    if err != nil {
        log.Printf("Error creating pinger for %s: %v", target, err)
        return
    }

    pinger.Count = 5
    pinger.Timeout = time.Second * 5
    pinger.SetPrivileged(true)

    log.Printf("Executing ping to %s (count=%d, timeout=%v)", target, pinger.Count, pinger.Timeout)
    err = pinger.Run()
    if err != nil {
        log.Printf("Error pinging %s: %v", target, err)
        targetUp.WithLabelValues(pe.podIP, target, pe.nodeName, "unknown", pe.podName).Set(0)
        return
    }

    stats := pinger.Statistics()
    log.Printf("Ping statistics for %s: sent=%d, recv=%d, loss=%v%%, min=%v, avg=%v, max=%v",
        target, stats.PacketsSent, stats.PacketsRecv, stats.PacketLoss,
        stats.MinRtt, stats.AvgRtt, stats.MaxRtt)

    pe.mutex.RLock()
    nodeName := pe.nodeNames[target]
    if nodeName == "" {
        nodeName = "unknown"
    }
    pe.mutex.RUnlock()

    log.Printf("Target %s maps to node %s", target, nodeName)

    labels := prometheus.Labels{
        "source":          pe.podIP,
        "destination":     target,
        "source_nodename": pe.nodeName,
        "dest_nodename":   nodeName,
        "source_podname":  pe.podName,
    }

    rttBest.With(labels).Set(float64(stats.MinRtt.Seconds()))
    rttWorst.With(labels).Set(float64(stats.MaxRtt.Seconds()))
    rttMean.With(labels).Set(float64(stats.AvgRtt.Seconds()))
    rttStdDev.With(labels).Set(float64(stats.StdDevRtt.Seconds()))
    lossRatio.With(labels).Set(float64(stats.PacketLoss) / 100.0)
    targetUp.With(labels).Set(1)
}

func (pe *PingExporter) startMetricsServer() {
    http.Handle("/metrics", promhttp.Handler())
    port := fmt.Sprintf(":%d", defaultMetricsPort)
    log.Printf("Starting metrics server on %s", port)
    if err := http.ListenAndServe(port, nil); err != nil {
        log.Fatalf("Error starting metrics server: %v", err)
    }
}

func (pe *PingExporter) Start() {
    // Start metrics server in a goroutine
    go pe.startMetricsServer()

    ticker := time.NewTicker(pe.interval)
    defer ticker.Stop()

    cleanupTicker := time.NewTicker(pe.interval)
    defer cleanupTicker.Stop()

    for {
        select {
        case <-ticker.C:
            if err := pe.updateTargets(); err != nil {
                log.Printf("Error updating targets: %v", err)
            }

            pe.mutex.RLock()
            for target := range pe.targets {
                go pe.pingTarget(target)
            }
            pe.mutex.RUnlock()

        case <-cleanupTicker.C:
            pe.cleanupOldMetrics()
        }
    }
}

func main() {
    exporter, err := NewPingExporter()
    if err != nil {
        log.Fatalf("Failed to create ping exporter: %v", err)
    }

    log.Printf("Starting Kubernetes Ping Exporter (Pod IP: %s, Node: %s)", exporter.podIP, exporter.nodeName)
    exporter.Start()
}