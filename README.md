# kubernetes-ping-exporter
 
Install it with helm
```
helm install infra-ping-exporter infra-ping-exporter --namespace monitoring --create-namespace --wait
```
Upgrade it with helm
```
helm upgrade infra-ping-exporter infra-ping-exporter --namespace monitoring --wait
```

Script runs every 15 seconds and pings all pods in daemonset. It exports the results in prometheus format on port 9107.

Example metrics:
```
# TYPE ping_rtt_best_seconds gauge
ping_rtt_best_seconds{dest_nodename="m2",destination="10.42.2.60",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0003981590270996094
ping_rtt_best_seconds{dest_nodename="m1",destination="10.42.1.45",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.00025343894958496094
# HELP ping_rtt_worst_seconds Worst round trip time
# TYPE ping_rtt_worst_seconds gauge
ping_rtt_worst_seconds{dest_nodename="m2",destination="10.42.2.60",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.002203702926635742
ping_rtt_worst_seconds{dest_nodename="m1",destination="10.42.1.45",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0033423900604248047
# HELP ping_rtt_mean_seconds Mean round trip time
# TYPE ping_rtt_mean_seconds gauge
ping_rtt_mean_seconds{dest_nodename="m2",destination="10.42.2.60",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.001270437240600586
ping_rtt_mean_seconds{dest_nodename="m1",destination="10.42.1.45",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0018872499465942382
# HELP ping_rtt_std_deviation_seconds Standard deviation of RTT
# TYPE ping_rtt_std_deviation_seconds gauge
ping_rtt_std_deviation_seconds{dest_nodename="m2",destination="10.42.2.60",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0005834860252997406
ping_rtt_std_deviation_seconds{dest_nodename="m1",destination="10.42.1.45",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0011797348868376168
# HELP ping_loss_ratio Packet loss ratio
# TYPE ping_loss_ratio gauge
ping_loss_ratio{dest_nodename="m2",destination="10.42.2.60",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0
ping_loss_ratio{dest_nodename="external",destination="1.1.1.1",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 1.0
ping_loss_ratio{dest_nodename="external",destination="8.8.8.8",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 1.0
ping_loss_ratio{dest_nodename="m1",destination="10.42.1.45",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0
# HELP ping_up Target reachability status (1=up, 0=down)
# TYPE ping_up gauge
ping_up{dest_nodename="m2",destination="10.42.2.60",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 1.0
ping_up{dest_nodename="external",destination="1.1.1.1",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0
ping_up{dest_nodename="external",destination="8.8.8.8",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 0.0
ping_up{dest_nodename="m1",destination="10.42.1.45",source="10.42.0.70",source_nodename="m0",source_podname="ping-exporter-ctmnn"} 1.0
```