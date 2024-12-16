import logging
from kubernetes import client, config, watch
from prometheus_client import start_http_server, Gauge
import ping3
import time
import os
from statistics import mean, stdev
from concurrent.futures import ThreadPoolExecutor
import threading
from typing import List, Tuple, Dict
import multiprocessing

# Configure logging
logging.basicConfig(
    level=logging.INFO,
    format='%(asctime)s - %(levelname)s - %(message)s'
)
logger = logging.getLogger(__name__)

# Initialize Prometheus metrics
METRICS = {
    'ping_rtt_best': Gauge('ping_rtt_best_seconds', 'Best round trip time', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname']),
    'ping_rtt_worst': Gauge('ping_rtt_worst_seconds', 'Worst round trip time', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname']),
    'ping_rtt_mean': Gauge('ping_rtt_mean_seconds', 'Mean round trip time', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname']),
    'ping_rtt_std_dev': Gauge('ping_rtt_std_deviation_seconds', 'Standard deviation of RTT', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname']),
    'ping_loss_ratio': Gauge('ping_loss_ratio', 'Packet loss ratio', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname']),
    'ping_up': Gauge('ping_up', 'Target reachability status (1=up, 0=down)', ['source', 'destination', 'source_nodename', 'dest_nodename', 'source_podname'])
}

def get_pod_ips() -> List[Tuple[str, str, str]]:
    try:
        config.load_incluster_config()
        v1 = client.CoreV1Api()
        
        namespace = os.environ.get('MY_POD_NAMESPACE')
        current_pod_ip = os.environ.get('POD_IP')
        
        pods = v1.list_namespaced_pod(
            namespace=namespace,
            label_selector='app=ping-exporter',
            timeout_seconds=10
        )
        
        pod_info = [
            (pod.status.pod_ip, pod.spec.node_name, pod.metadata.name)
            for pod in pods.items
            if pod.status.pod_ip and pod.status.pod_ip != current_pod_ip
        ]
        
        logger.info(f"Found {len(pod_info)} pods in the DaemonSet")
        return pod_info
    except Exception as e:
        logger.error(f"Error getting pod IPs: {e}")
        return []

def get_additional_ips() -> List[str]:
    try:
        config.load_incluster_config()
        v1 = client.CoreV1Api()
        
        namespace = os.environ.get('MY_POD_NAMESPACE')
        config_map_name = os.environ.get('CONFIG_MAP_NAME', 'ping-exporter-config')
        
        config_map = v1.read_namespaced_config_map(config_map_name, namespace)
        additional_ips = config_map.data.get('additional_ips', '')
        
        logger.info(f"Additional IPs from ConfigMap: {additional_ips}")
        return [ip.strip() for ip in additional_ips.split(',') if ip.strip()]
    except Exception as e:
        logger.error(f"Error reading ConfigMap: {e}")
        return []

def ping_target(target_ip: str, count: int = 10) -> Dict:
    rtts = []
    timeout = 2  # Reduced timeout for faster failure detection
    
    for _ in range(count):
        try:
            rtt = ping3.ping(target_ip, timeout=timeout)
            if rtt is not None:
                rtts.append(rtt)
        except Exception as e:
            logger.debug(f"Ping failed for {target_ip}: {e}")
    
    if not rtts:
        return {'up': 0, 'loss_ratio': 1.0}
    
    return {
        'best': min(rtts),
        'worst': max(rtts),
        'mean': mean(rtts),
        'std_dev': stdev(rtts) if len(rtts) > 1 else 0,
        'loss_ratio': 1 - (len(rtts) / count),
        'up': 1
    }

def update_metrics(source_ip: str, target_ip: str, source_nodename: str, 
                  dest_nodename: str, source_podname: str, results: Dict):
    labels = {
        'source': source_ip,
        'destination': target_ip,
        'source_nodename': source_nodename,
        'dest_nodename': dest_nodename,
        'source_podname': source_podname
    }
    
    for metric_name, value in results.items():
        if metric_name in METRICS:
            METRICS[metric_name].labels(**labels).set(value)

def ping_and_update_metrics(source_ip: str, target_info: Tuple[str, str, str], 
                          source_nodename: str, source_podname: str):
    target_ip, dest_nodename, dest_podname = target_info
    results = ping_target(target_ip)
    if results:
        update_metrics(source_ip, target_ip, source_nodename, dest_nodename, source_podname, results)

def main():
    start_http_server(9107)
    logger.info("Started Prometheus HTTP server on port 9107")
    
    source_ip = os.environ.get('POD_IP')
    source_nodename = os.environ.get('NODE_NAME', 'unknown')
    source_podname = os.environ.get('MY_POD_NAME', 'unknown')
    
    # Optimize thread pool size based on system resources
    cpu_count = multiprocessing.cpu_count()
    max_workers = min(32, cpu_count * 4)
    
    with ThreadPoolExecutor(max_workers=max_workers) as executor:
        while True:
            try:
                target_info = get_pod_ips()
                additional_ips = get_additional_ips()
                target_info.extend([(ip, 'external', 'external') for ip in additional_ips])
                
                futures = [
                    executor.submit(
                        ping_and_update_metrics,
                        source_ip,
                        target,
                        source_nodename,
                        source_podname
                    ) for target in target_info
                ]
                
                # Use as_completed for better exception handling
                for future in concurrent.futures.as_completed(futures):
                    try:
                        future.result(timeout=10)
                    except Exception as e:
                        logger.error(f"Error in ping thread: {e}")
            
            except Exception as e:
                logger.error(f"Error in main loop: {e}")
            
            time.sleep(15)

if __name__ == '__main__':
    main()