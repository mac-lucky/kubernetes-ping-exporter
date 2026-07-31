package main

import (
	"errors"
	"sync"
	"testing"
	"time"

	probing "github.com/prometheus-community/pro-bing"
	"github.com/prometheus/client_golang/prometheus/testutil"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestParseAdditionalIPTargets(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []targetInfo
	}{
		{
			name: "empty string yields no targets",
			raw:  "",
			want: nil,
		},
		{
			name: "single ip",
			raw:  "10.1.2.3",
			want: []targetInfo{{ip: "10.1.2.3", nodeName: "external", podName: "external"}},
		},
		{
			name: "multiple ips with surrounding whitespace",
			raw:  " 10.1.2.3 , 10.1.2.4,10.1.2.5 ",
			want: []targetInfo{
				{ip: "10.1.2.3", nodeName: "external", podName: "external"},
				{ip: "10.1.2.4", nodeName: "external", podName: "external"},
				{ip: "10.1.2.5", nodeName: "external", podName: "external"},
			},
		},
		{
			name: "blank entries between commas are skipped",
			raw:  "10.1.2.3,,  ,10.1.2.4,",
			want: []targetInfo{
				{ip: "10.1.2.3", nodeName: "external", podName: "external"},
				{ip: "10.1.2.4", nodeName: "external", podName: "external"},
			},
		},
		{
			name: "only whitespace and commas yields no targets",
			raw:  " , , ",
			want: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseAdditionalIPTargets(tt.raw)
			assertTargetsEqual(t, got, tt.want)
		})
	}
}

func TestSelectPeerPods(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ping-exporter-a"},
			Spec:       corev1.PodSpec{NodeName: "node-a"},
			Status:     corev1.PodStatus{PodIP: "10.0.0.1"},
		},
		{
			// This is the current pod itself: same IP as currentPodIP, must be excluded.
			ObjectMeta: metav1.ObjectMeta{Name: "ping-exporter-self"},
			Spec:       corev1.PodSpec{NodeName: "node-self"},
			Status:     corev1.PodStatus{PodIP: "10.0.0.2"},
		},
		{
			// No IP assigned yet (pod still starting): must be excluded.
			ObjectMeta: metav1.ObjectMeta{Name: "ping-exporter-pending"},
			Spec:       corev1.PodSpec{NodeName: "node-c"},
			Status:     corev1.PodStatus{PodIP: ""},
		},
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ping-exporter-b"},
			Spec:       corev1.PodSpec{NodeName: "node-b"},
			Status:     corev1.PodStatus{PodIP: "10.0.0.3"},
		},
	}

	got := selectPeerPods(pods, "10.0.0.2")
	want := []targetInfo{
		{ip: "10.0.0.1", nodeName: "node-a", podName: "ping-exporter-a"},
		{ip: "10.0.0.3", nodeName: "node-b", podName: "ping-exporter-b"},
	}
	assertTargetsEqual(t, got, want)
}

func TestSelectPeerPodsNoPeers(t *testing.T) {
	pods := []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{Name: "ping-exporter-self"},
			Status:     corev1.PodStatus{PodIP: "10.0.0.2"},
		},
	}

	got := selectPeerPods(pods, "10.0.0.2")
	if len(got) != 0 {
		t.Errorf("selectPeerPods() = %v, want no targets", got)
	}
}

func TestCombineTargets(t *testing.T) {
	podTargets := []targetInfo{{ip: "10.0.0.1", nodeName: "node-a", podName: "pod-a"}}
	additionalTargets := []targetInfo{{ip: "10.1.0.1", nodeName: "external", podName: "external"}}

	t.Run("merges pod and additional targets when resolution succeeds", func(t *testing.T) {
		got := combineTargets(podTargets, additionalTargets, nil)
		want := append(append([]targetInfo{}, podTargets...), additionalTargets...)
		assertTargetsEqual(t, got, want)
	})

	t.Run("falls back to pod targets when additional resolution fails", func(t *testing.T) {
		got := combineTargets(podTargets, additionalTargets, errors.New("configmap not found"))
		assertTargetsEqual(t, got, podTargets)
	})
}

func TestPingLabels(t *testing.T) {
	labels := pingLabels("10.0.0.1", "10.0.0.2", "node-a", "node-b", "pod-a")

	want := map[string]string{
		"source":          "10.0.0.1",
		"destination":     "10.0.0.2",
		"source_nodename": "node-a",
		"dest_nodename":   "node-b",
		"source_podname":  "pod-a",
	}
	for k, v := range want {
		if labels[k] != v {
			t.Errorf("pingLabels()[%q] = %q, want %q", k, labels[k], v)
		}
	}
	if len(labels) != len(want) {
		t.Errorf("pingLabels() has %d labels, want %d: %v", len(labels), len(want), labels)
	}
}

func TestPingMetricValues(t *testing.T) {
	tests := []struct {
		name          string
		stats         *probing.Statistics
		wantUp        float64
		wantLossRatio float64
	}{
		{
			name:          "all packets received",
			stats:         &probing.Statistics{PacketsRecv: 5, PacketLoss: 0},
			wantUp:        1,
			wantLossRatio: 0,
		},
		{
			name:          "partial loss with at least one reply",
			stats:         &probing.Statistics{PacketsRecv: 4, PacketLoss: 20},
			wantUp:        1,
			wantLossRatio: 0.2,
		},
		{
			name:          "no packets received is reported as fully down",
			stats:         &probing.Statistics{PacketsRecv: 0, PacketLoss: 40},
			wantUp:        0,
			wantLossRatio: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotUp, gotLossRatio := pingMetricValues(tt.stats)
			if gotUp != tt.wantUp {
				t.Errorf("pingMetricValues() up = %v, want %v", gotUp, tt.wantUp)
			}
			if gotLossRatio != tt.wantLossRatio {
				t.Errorf("pingMetricValues() lossRatio = %v, want %v", gotLossRatio, tt.wantLossRatio)
			}
		})
	}
}

func TestUpdateMetrics(t *testing.T) {
	target := targetInfo{ip: "10.9.9.2", nodeName: "node-b", podName: "pod-b"}
	labels := pingLabels("10.9.9.1", target.ip, "node-a", target.nodeName, "pod-a")

	upStats := &probing.Statistics{
		PacketsRecv: 4,
		PacketLoss:  20,
		MinRtt:      1 * time.Millisecond,
		MaxRtt:      5 * time.Millisecond,
		AvgRtt:      3 * time.Millisecond,
		StdDevRtt:   2 * time.Millisecond,
	}
	updateMetrics("10.9.9.1", "node-a", "pod-a", target, upStats)

	if got := testutil.ToFloat64(pingUp.With(labels)); got != 1 {
		t.Errorf("ping_up = %v, want 1", got)
	}
	if got := testutil.ToFloat64(pingLossRatio.With(labels)); got != 0.2 {
		t.Errorf("ping_loss_ratio = %v, want 0.2", got)
	}
	if got := testutil.ToFloat64(pingRTTBest.With(labels)); got != upStats.MinRtt.Seconds() {
		t.Errorf("ping_rtt_best_seconds = %v, want %v", got, upStats.MinRtt.Seconds())
	}
	if got := testutil.ToFloat64(pingRTTWorst.With(labels)); got != upStats.MaxRtt.Seconds() {
		t.Errorf("ping_rtt_worst_seconds = %v, want %v", got, upStats.MaxRtt.Seconds())
	}
	if got := testutil.ToFloat64(pingRTTMean.With(labels)); got != upStats.AvgRtt.Seconds() {
		t.Errorf("ping_rtt_mean_seconds = %v, want %v", got, upStats.AvgRtt.Seconds())
	}
	if got := testutil.ToFloat64(pingRTTStdDev.With(labels)); got != upStats.StdDevRtt.Seconds() {
		t.Errorf("ping_rtt_std_deviation_seconds = %v, want %v", got, upStats.StdDevRtt.Seconds())
	}

	// Target goes down: ping_up/ping_loss_ratio flip, RTT series are removed
	// rather than left at their last (now meaningless) values.
	downStats := &probing.Statistics{PacketsRecv: 0, PacketLoss: 100}
	updateMetrics("10.9.9.1", "node-a", "pod-a", target, downStats)

	if got := testutil.ToFloat64(pingUp.With(labels)); got != 0 {
		t.Errorf("ping_up after outage = %v, want 0", got)
	}
	if got := testutil.ToFloat64(pingLossRatio.With(labels)); got != 1 {
		t.Errorf("ping_loss_ratio after outage = %v, want 1", got)
	}
	if pingRTTBest.Delete(labels) {
		t.Error("ping_rtt_best_seconds should already have been deleted when the target went down")
	}
	if pingRTTWorst.Delete(labels) {
		t.Error("ping_rtt_worst_seconds should already have been deleted when the target went down")
	}
	if pingRTTMean.Delete(labels) {
		t.Error("ping_rtt_mean_seconds should already have been deleted when the target went down")
	}
	if pingRTTStdDev.Delete(labels) {
		t.Error("ping_rtt_std_deviation_seconds should already have been deleted when the target went down")
	}

	// Clean up the gauges this test set so other tests never observe them.
	pingUp.Delete(labels)
	pingLossRatio.Delete(labels)
}

func TestCleanupObsoleteMetrics(t *testing.T) {
	const sourceIP, sourceNode, sourcePod = "10.9.9.1", "node-a", "pod-a"

	current := targetInfo{ip: "10.9.9.10", nodeName: "node-b", podName: "pod-b"}
	obsolete := targetInfo{ip: "10.9.9.11", nodeName: "node-c", podName: "pod-c"}

	currentLabels := pingLabels(sourceIP, current.ip, sourceNode, current.nodeName, sourcePod)
	obsoleteLabels := pingLabels(sourceIP, obsolete.ip, sourceNode, obsolete.nodeName, sourcePod)

	// Seed metrics for both targets as if a previous scrape had populated them.
	pingUp.With(currentLabels).Set(1)
	pingUp.With(obsoleteLabels).Set(1)

	var previousTargets sync.Map
	previousTargets.Store(current.ip, current.nodeName)
	previousTargets.Store(obsolete.ip, obsolete.nodeName)

	cleanupObsoleteMetrics(&previousTargets, []targetInfo{current}, sourceIP, sourceNode, sourcePod)

	if _, ok := previousTargets.Load(obsolete.ip); ok {
		t.Error("obsolete target should have been removed from previousTargets")
	}
	if _, ok := previousTargets.Load(current.ip); !ok {
		t.Error("current target should still be present in previousTargets")
	}

	if pingUp.Delete(obsoleteLabels) {
		t.Error("ping_up for the obsolete target should already have been deleted")
	}
	if !pingUp.Delete(currentLabels) {
		t.Error("ping_up for the still-current target should not have been deleted")
	}
}

// assertTargetsEqual compares two targetInfo slices for equality, treating a
// nil slice and an empty slice as equal.
func assertTargetsEqual(t *testing.T, got, want []targetInfo) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("got %d targets, want %d: got=%v want=%v", len(got), len(want), got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("target[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}
