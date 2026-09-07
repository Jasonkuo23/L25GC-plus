# Multi-UE performance checkpoint

## Quick two-UE test

Use this section for a straightforward measurement. The longer procedure below
is only needed when preparing a fully reproducible paper artifact.

On the DN, keep two servers running in separate terminals:

```bash
iperf3 -s -B 192.168.3.2 -p 5201
```

```bash
iperf3 -s -B 192.168.3.2 -p 5202
```

On the CN, save counters immediately before and after each pair of UE commands:

```bash
sudo ./bin/n3iwf-dpctl -operation stats | tee /tmp/stats.before
# Run one test pair on the UE host.
sudo ./bin/n3iwf-dpctl -operation stats | tee /tmp/stats.after
```

Run each following pair on the UE host. The `&` and `wait` make both UEs run
concurrently.

TCP uplink, UE to DN:

```bash
sudo ip netns exec n3iwue1 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.1 -c 192.168.3.2 -p 5201 -t 60 -i 1 \
  | tee /tmp/tcp-ul-ue1.log &
p1=$!
sudo ip netns exec n3iwue2 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.2 -c 192.168.3.2 -p 5202 -t 60 -i 1 \
  | tee /tmp/tcp-ul-ue2.log &
p2=$!
wait "$p1" "$p2"
```

TCP downlink, DN to UE:

```bash
sudo ip netns exec n3iwue1 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.1 -c 192.168.3.2 -p 5201 -R -t 60 -i 1 \
  | tee /tmp/tcp-dl-ue1.log &
p1=$!
sudo ip netns exec n3iwue2 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.2 -c 192.168.3.2 -p 5202 -R -t 60 -i 1 \
  | tee /tmp/tcp-dl-ue2.log &
p2=$!
wait "$p1" "$p2"
```

UDP uplink at an initial offered load of 300 Mbit/s per UE:

```bash
sudo ip netns exec n3iwue1 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.1 -c 192.168.3.2 -p 5201 \
  -u -b 300M -l 1200 -t 60 -i 1 | tee /tmp/udp-ul-ue1.log &
p1=$!
sudo ip netns exec n3iwue2 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.2 -c 192.168.3.2 -p 5202 \
  -u -b 300M -l 1200 -t 60 -i 1 | tee /tmp/udp-ul-ue2.log &
p2=$!
wait "$p1" "$p2"
```

UDP downlink at 300 Mbit/s per UE:

```bash
sudo ip netns exec n3iwue1 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.1 -c 192.168.3.2 -p 5201 \
  -u -b 300M -l 1200 -R -t 60 -i 1 | tee /tmp/udp-dl-ue1.log &
p1=$!
sudo ip netns exec n3iwue2 ip vrf exec vrf-pdu-10 \
  iperf3 -B 10.60.0.2 -c 192.168.3.2 -p 5202 \
  -u -b 300M -l 1200 -R -t 60 -i 1 | tee /tmp/udp-dl-ue2.log &
p2=$!
wait "$p1" "$p2"
```

Require zero dataplane error/drop counter deltas. For UDP, record each UE's
receiver bitrate and packet-loss percentage; a valid controlled point has no
more than 0.1% loss. If both UEs pass at 300 Mbit/s, repeat at 400 Mbit/s and
then 500 Mbit/s per UE until loss exceeds 0.1% to locate the saturation knee.

As an optional stress test, replace the TCP direction option with `--bidir` on
both concurrent commands. Also record an idle ping and a ping during the
two-UE load; keep the raw output for later p99 latency calculation. Repeat the
four essential tests three times when the numbers will be reported formally.

This procedure records a reproducible two-UE ONVM/DPDK software-IPsec
throughput and fairness checkpoint. It is not the final performance acceptance:
that additionally requires an identically constrained Linux/free5GC run, UDP
loss, p99 latency, and controlled CPU comparison.

Keep rekey disabled. Use the already accepted two-UE topology, distinct PDU
addresses `10.60.0.1` and `10.60.0.2`, DN `192.168.3.2`, and iperf3 ports 5201
and 5202. Do not change CPU placement, MTU, NIC queues, cipher, or configuration
between trials.

## 1. Create the run record

On the CN/N3IWF host:

```bash
cd /home/ubuntu/L25GC-plus
run_id="n3iwf-two-ue-perf-$(date -u +%Y%m%dT%H%M%SZ)"
result_dir="$PWD/results/$run_id"
mkdir -p "$result_dir"/{cn,ue,dn,trials}

git rev-parse HEAD >"$result_dir/cn/root.revision"
git status --short --branch >"$result_dir/cn/root.status"
git -C NFs/n3iwf rev-parse HEAD >"$result_dir/cn/n3iwf.revision"
git -C NFs/n3iwf status --short --branch >"$result_dir/cn/n3iwf.status"
git -C NFs/onvm-upf rev-parse HEAD >"$result_dir/cn/onvm-upf.revision"
git -C NFs/onvm-upf status --short --branch >"$result_dir/cn/onvm-upf.status"
cp config/n3iwfcfg.yaml config/n3iwf_dp_topology.env "$result_dir/cn/"
lscpu -e >"$result_dir/cn/lscpu.txt"
uname -a >"$result_dir/cn/uname.txt"
```

Record equivalent `uname -a`, `lscpu -e`, N3IWUE revision/status, interface
state, routes, and `iperf3 --version` on the UE host. Record the DN host data as
well. Do not copy subscriber or Child-SA keys into the result.

Copy the runner to the UE host, preserving its contents:

```bash
scp onvm_test/multi_ue_performance/run_two_ue_iperf.sh \
  ubuntu@<UE_HOST>:~/n3iwue/test/run_two_ue_iperf.sh
```

## 2. Start persistent DN servers

In two DN terminals:

```bash
iperf3 -s -B 192.168.3.2 -p 5201 | tee /tmp/${run_id}-5201.log
```

```bash
iperf3 -s -B 192.168.3.2 -p 5202 | tee /tmp/${run_id}-5202.log
```

Verify both UEs are registered and that `active_sessions=2`,
`active_child_sas=2`, and `access_mac_learns=2` remain stable.

## 3. Run the fixed matrix

Run three repetitions of every row. Each client gets a five-second omitted
warm-up followed by sixty measured seconds.

| Case | Mode | Target | Purpose |
| --- | --- | --- | --- |
| `ue1-ul`, `ue1-dl` | uplink/downlink | ue1 | UE1 single-user ceiling |
| `ue2-ul`, `ue2-dl` | uplink/downlink | ue2 | UE2 single-user ceiling |
| `two-ul` | uplink | both | Concurrent aggregate uplink and fairness |
| `two-dl` | downlink | both | Concurrent aggregate downlink and fairness |
| `two-bidir` | bidir | both | Four-direction concurrency checkpoint |

For each trial, first create matching directories on the CN and UE hosts. Save
CN counters immediately before and after the UE command. Example for the first
concurrent-uplink repetition:

```bash
# CN host
trial=two-ul-r1
mkdir -p "$result_dir/trials/$trial"
sudo ./bin/n3iwf-dpctl -operation stats \
  >"$result_dir/trials/$trial/stats.before"
mpstat -P ALL 1 70 >"$result_dir/trials/$trial/mpstat.txt" &
mpstat_pid=$!
```

```bash
# UE host; use the same run_id and trial strings
ue_result="$HOME/n3iwue/results/$run_id/trials/$trial"
mkdir -p "$ue_result"
chmod +x "$HOME/n3iwue/test/run_two_ue_iperf.sh"
"$HOME/n3iwue/test/run_two_ue_iperf.sh" uplink both "$ue_result" 60 5
```

```bash
# CN host, immediately after the UE command finishes
sudo ./bin/n3iwf-dpctl -operation stats \
  >"$result_dir/trials/$trial/stats.after"
wait "$mpstat_pid"
```

Change `MODE` and `TARGET` according to the table. Use `r1`, `r2`, and `r3`.
Allow at least 30 seconds idle time between trials and confirm TCP traffic has
stopped before taking the next baseline. If `mpstat` is unavailable, install
the `sysstat` package or record `top -b -d 1` output instead.

Optionally pin the two client processes to distinct, documented UE-host cores:

```bash
UE1_CPU=2 UE2_CPU=3 \
  "$HOME/n3iwue/test/run_two_ue_iperf.sh" uplink both "$ue_result" 60 5
```

Use the same pinning for every comparable trial. Do not pin either client to a
core used by N3IWUE packet processing unless that sharing is deliberate and
recorded.

## 4. Validate and collect each trial

Both client exit-status files must contain `0`, both JSON files must contain an
empty `.error` value, and the CN error/drop counters must have zero delta.
Reject and repeat a trial if a UE disconnects, a session/SA count changes, or
any unknown TEID/QFI/SPI, replay, crypto, malformed, fragment, oversize,
buffer, stale, punt, MAC-change, or neighbor-drop counter increases.

Copy the UE JSON and metadata plus both DN server logs into the CN result tree:

```bash
scp -r ubuntu@<UE_HOST>:~/n3iwue/results/$run_id/trials/* \
  "$result_dir/trials/"
scp ubuntu@<DN_HOST>:/tmp/${run_id}-5201.log "$result_dir/dn/"
scp ubuntu@<DN_HOST>:/tmp/${run_id}-5202.log "$result_dir/dn/"
```

For each direction, report every repetition plus median throughput. For a
two-UE case also report UE1, UE2, aggregate throughput, and Jain's fairness
index:

```text
fairness = (ue1 + ue2)^2 / (2 * (ue1^2 + ue2^2))
```

Do not select only the fastest run. Record TCP retransmissions and explain the
receiver-duration tail separately from the sender's fixed measurement window.
Retain the raw JSON; it is the source of truth for later tables and plots.

## 5. Minimum result table

```text
case | repetition | UE1 Gbit/s | UE2 Gbit/s | aggregate Gbit/s |
TCP retransmits | fairness | error/drop delta | valid/invalid
```

After this ONVM checkpoint is complete, repeat the identical matrix with the
Linux/free5GC backend and the same hardware, cipher, session count, MTU, stream
count, duration, and CPU-core budget. Only that paired run can support the 2x
performance objective.
