from kubernetes import client, config
from prometheus_client import start_http_server, Gauge
import ping3
import time
import os
from statistics import mean, stdev
from concurrent.futures import ThreadPoolExecutor
import threading
import signal

# Initialize Prometheus metrics
ping_rtt_best = Gauge('ping_rtt_best_seconds', 'Best round trip time', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname'])
ping_rtt_worst = Gauge('ping_rtt_worst_seconds', 'Worst round trip time', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname'])
ping_rtt_mean = Gauge('ping_rtt_mean_seconds', 'Mean round trip time', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname'])
ping_rtt_std_dev = Gauge('ping_rtt_std_deviation_seconds', 'Standard deviation of RTT', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname'])
ping_loss_ratio = Gauge('ping_loss_ratio', 'Packet loss ratio', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname'])
ping_up = Gauge('ping_up', 'Target reachability status (1=up, 0=down)', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname'])

shutdown_event = threading.Event()

def signal_handler(signum, frame):
    print(f"Received termination signal (signum: {signum}). Exiting...")
    shutdown_event.set()

signal.signal(signal.SIGTERM, signal_handler)
signal.signal(signal.SIGINT, signal_handler)

# Load in-cluster config once
config.load_incluster_config()
v1_api = client.CoreV1Api()

def get_pod_ips(v1, namespace, current_pod_ip):
    try:
        # Use passed-in API client 'v1'
        pods = v1.list_namespaced_pod(
            namespace=namespace,
            label_selector='app=ping-exporter'
        )
        pod_info = [
            (pod.status.pod_ip, pod.spec.node_name, pod.metadata.name)
            for pod in pods.items
            if pod.status.pod_ip and pod.status.pod_ip != current_pod_ip
        ]
        print(f"Found {len(pod_info)} pods in the DaemonSet")
        return pod_info
    except client.exceptions.ApiException as e:
        print(f"API error getting pod IPs: {e}")
        return []

def get_additional_ips(v1, namespace):
    try:
        # Use passed-in API client 'v1'
        config_map_name = os.environ.get('CONFIG_MAP_NAME', 'ping-exporter-config')
        config_map = v1.read_namespaced_config_map(config_map_name, namespace)
        additional_ips = config_map.data.get('additional_ips', '')
        print(f"Additional IPs from ConfigMap: {additional_ips}")
        return [ip.strip() for ip in additional_ips.split(',') if ip.strip()]
    except client.exceptions.ApiException as e:
        print(f"API error reading ConfigMap: {e}")
        return []

def ping_target(target_ip, count=10):
    print(f"Starting ping sequence for {target_ip} ({count} attempts)")
    try:
        rtts = [
            rtt for rtt in (
                ping3.ping(target_ip, timeout=4) for _ in range(count)
            ) if rtt is not None
        ]
    except Exception as e:
        print(f"Error pinging {target_ip}: {e}")
        return {'up': 0, 'loss_ratio': 1.0}
    if rtts:
        results = {
            'best': min(rtts),
            'worst': max(rtts),
            'mean': mean(rtts),
            'std_dev': stdev(rtts) if len(rtts) > 1 else 0,
            'loss_ratio': 1 - (len(rtts) / count),
            'up': 1
        }
        print(f"Ping results for {target_ip}: {results}")
        return results
    else:
        print(f"No successful pings for {target_ip}")
        return {'up': 0, 'loss_ratio': 1.0}

def update_metrics(source_ip, target_ip, source_nodename, dest_nodename, source_podname, results):
    labels = {
        'source': source_ip,
        'destination': target_ip,
        'source_nodename': source_nodename,
        'dest_nodename': dest_nodename,
        'source_podname': source_podname
    }
    ping_up.labels(**labels).set(results['up'])
    ping_loss_ratio.labels(**labels).set(results['loss_ratio'])
    
    if results['up']:
        ping_rtt_best.labels(**labels).set(results['best'])
        ping_rtt_worst.labels(**labels).set(results['worst'])
        ping_rtt_mean.labels(**labels).set(results['mean'])
        ping_rtt_std_dev.labels(**labels).set(results['std_dev'])
    else:
        # Set RTT metrics to "unknown" state by removing them
        ping_rtt_best.remove(*labels.values())
        ping_rtt_worst.remove(*labels.values())
        ping_rtt_mean.remove(*labels.values())
        ping_rtt_std_dev.remove(*labels.values())

def ping_and_update_metrics(source_ip, target_info, source_nodename, source_podname):
    target_ip, dest_nodename, dest_podname = target_info
    print(f"Thread {threading.current_thread().name} pinging {target_ip}")
    results = ping_target(target_ip)
    if results:
        update_metrics(source_ip, target_ip, source_nodename, dest_nodename, source_podname, results)

def main():
    # Start Prometheus HTTP server
    start_http_server(9107)
    print("Started Prometheus HTTP server on port 9107")
    
    # Get current pod's IP
    source_ip = os.environ.get('POD_IP')
    source_nodename = os.environ.get('NODE_NAME', 'unknown')
    source_podname = os.environ.get('MY_POD_NAME', 'unknown')
    namespace = os.environ.get('MY_POD_NAMESPACE')  # Add this line
    print(f"Source IP: {source_ip}, Node: {source_nodename}, Pod: {source_podname}")

    # Cache target IPs
    target_info_cache = []
    cache_refresh_interval = 60  # Refresh every 60 seconds
    last_cache_time = 0

    # Limit the number of worker threads
    max_workers = min(16, os.cpu_count() * 2)

    # Initialize cycle counter
    cycle_count = 0

    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        while not shutdown_event.is_set():
            current_time = time.time()
            # Do not cache for the first 3 cycles
            if cycle_count < 3 or current_time - last_cache_time > cache_refresh_interval:
                # Refresh targets
                pod_targets = get_pod_ips(v1_api, namespace, source_ip)
                additional_targets = [
                    (ip, 'external', 'external') for ip in get_additional_ips(v1_api, namespace)
                ]
                target_info_cache = pod_targets + additional_targets
                last_cache_time = current_time
            cycle_count += 1
            print("\n--- Starting new ping cycle ---")
            print(f"Total targets to ping: {len(target_info_cache)}")

            # Submit all ping tasks to the thread pool
            futures = [
                executor.submit(
                    ping_and_update_metrics,
                    source_ip,
                    target,
                    source_nodename,
                    source_podname
                ) for target in target_info_cache
            ]

            # Wait for all pings to complete
            for future in futures:
                if shutdown_event.is_set():
                    break
                try:
                    future.result()
                except Exception as e:
                    print(f"Error in ping thread: {e}")

            # Wait for 15 seconds or exit if shutting down
            if shutdown_event.wait(15):
                break

    print("Shutting down...")

if __name__ == '__main__':
    main()