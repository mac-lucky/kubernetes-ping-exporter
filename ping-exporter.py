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

terminate = False

def signal_handler(sig, frame):
    global terminate
    print("Termination signal received, exiting...")
    terminate = True

# Register signal handler
signal.signal(signal.SIGINT, signal_handler)
signal.signal(signal.SIGTERM, signal_handler)

def get_pod_ips():
    try:
        # Load in-cluster config
        config.load_incluster_config()
        v1 = client.CoreV1Api()
        
        # Get current namespace and pod name from environment variables
        namespace = os.environ.get('MY_POD_NAMESPACE')
        current_pod_ip = os.environ.get('POD_IP')
        current_pod_name = os.environ.get('MY_POD_NAME')
        
        # Get pods from the same DaemonSet
        pods = v1.list_namespaced_pod(namespace=namespace,
                                    label_selector='app=ping-exporter')
        
        pod_info = [(pod.status.pod_ip, pod.spec.node_name, pod.metadata.name) for pod in pods.items if pod.status.pod_ip]
        
        print(f"Found {len(pod_info)} pods in the DaemonSet")
        print(f"Filtered pod IPs (excluding current pod): {pod_info}")
        # Filter out current pod's IP
        return [(ip, node, name) for ip, node, name in pod_info if ip != current_pod_ip]
    except Exception as e:
        print(f"Error getting pod IPs: {e}")
        return []

def get_additional_ips():
    try:
        config.load_incluster_config()
        v1 = client.CoreV1Api()
        
        namespace = os.environ.get('MY_POD_NAMESPACE')
        config_map_name = os.environ.get('CONFIG_MAP_NAME', 'ping-exporter-config')
        
        config_map = v1.read_namespaced_config_map(config_map_name, namespace)
        additional_ips = config_map.data.get('additional_ips', '')
        
        print(f"Additional IPs from ConfigMap: {additional_ips}")
        return [ip.strip() for ip in additional_ips.split(',') if ip.strip()]
    except Exception as e:
        print(f"Error reading ConfigMap: {e}")
        return []

def ping_target(target_ip, count=10):
    print(f"Starting ping sequence for {target_ip} ({count} attempts)")
    rtts = []
    for _ in range(count):
        try:
            rtt = ping3.ping(target_ip, timeout=4)
            if rtt is not None:
                rtts.append(rtt)
        except Exception as e:
            print(f"Error pinging {target_ip}: {e}")
            
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
    print(f"Source IP: {source_ip}, Node: {source_nodename}, Pod: {source_podname}")
    
    # Create thread pool
    max_workers = min(32, os.cpu_count() * 4)  # Limit maximum number of threads
    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        while not terminate:
            print("\n--- Starting new ping cycle ---")
            # Get IPs and nodenames of other pods in the DaemonSet
            target_info = get_pod_ips()
            
            # Add additional IPs from ConfigMap if configured
            additional_ips = get_additional_ips()
            target_info.extend([(ip, 'external', 'external') for ip in additional_ips])
            
            print(f"Total targets to ping: {len(target_info)}")
            
            # Submit all ping tasks to the thread pool
            futures = [
                executor.submit(
                    ping_and_update_metrics,
                    source_ip,
                    target,
                    source_nodename,
                    source_podname
                ) for target in target_info
            ]
            
            # Wait for all pings to complete
            for future in futures:
                try:
                    future.result()
                except Exception as e:
                    print(f"Error in ping thread: {e}")
            
            time.sleep(15)  # Wait before next round of pings

    print("Exiting main loop, shutting down...")

if __name__ == '__main__':
    main()