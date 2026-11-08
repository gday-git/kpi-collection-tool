# Validation Tasks Overview

kpi-collector supports four task types that run against an OpenShift cluster.
You configure them in a single `tasks.yaml` file and launch everything with
one command:

```bash
kpi-collector run --tasks tasks.yaml \
  --cluster-name my-cluster --cluster-type ran \
  --kubeconfig ~/.kube/config
```

This page introduces each task — what it does, what artifacts you get, and when
to use it. For the full configuration reference, see
[Tasks Configuration](tasks-configuration.md).

---

## The four tasks

### 1. Prometheus — metric collection

Collects PromQL metrics from Prometheus/Thanos at a configurable frequency and
stores them in a local SQLite database (or PostgreSQL). Built-in KPI profiles
cover RAN, Core, and Hub clusters out of the box.

| | |
|---|---|
| **Artifacts** | `kpi_metrics.db`, log file, optional JSON output |
| **Use when** | You need to capture cluster health metrics over time for analysis or compliance |
| **Auth** | `--kubeconfig` (auto-discovers Thanos) **or** `--token` + `--thanos-url` |
| **Config guide** | [Collecting Prometheus Metrics](collecting-metrics.md), [Prometheus KPI Configuration](kpis-file-configuration.md) |

### 2. Per-node data — node-level diagnostics

Creates a privileged debug pod on every cluster node and collects CPU usage
(`top`), memory info (`/proc/meminfo`), kernel command line (`/proc/cmdline`),
plus pod and node describes from the Kubernetes API.

| | |
|---|---|
| **Artifacts** | `top-<node>.out`, `proc_meminfo-<node>.txt`, `proc_cmdline-<node>.txt`, `describe_pods_<ns>.txt`, `describe_nodes.txt` |
| **Use when** | You need a snapshot of system-level resource usage and kernel configuration across all nodes |
| **Auth** | `--kubeconfig` with admin privileges |
| **Config guide** | [Per-Node Data Configuration](per-node-data-configuration.md) |

### 3. Oslat — OS latency testing

Creates a user-supplied Pod on the cluster, waits until it completes (or times
out), and writes the pod's logs to an artifact file. You provide your own image
and command — oslat, cyclictest, or any latency-measurement workload.

| | |
|---|---|
| **Artifacts** | `oslat_logs.out` |
| **Use when** | You need to measure OS-level latency on cluster nodes using your own test pod |
| **Auth** | `--kubeconfig` with admin privileges |
| **Config guide** | [Oslat Configuration](oslat-configuration.md) |

### 4. App recovery time — workload recovery after reboot

Reboots configured cluster node(s) via a privileged debug pod, waits until they
are NotReady, then polls workload pod status for the configured duration.
Measures how long pods take to come back after a node reboot.

> ⚠️ **This task reboots real nodes.** Only use on disposable lab workers.

| | |
|---|---|
| **Artifacts** | `pod_status.out` |
| **Use when** | You need to validate workload recovery time after a node reboot in a lab environment |
| **Auth** | `--kubeconfig` with admin privileges |
| **Config guide** | [App Recovery Time Configuration](app-recovery-time-configuration.md) |

---

## Try it in 2 minutes

The fastest way to run the non-Prometheus tasks is with a
[task profile](../task-profiles/README.md):

```bash
kpi-collector run \
  --tasks task-profiles/tasks-quickstart.yaml \
  --cluster-name my-cluster --cluster-type ran \
  --kubeconfig ~/.kube/config
```

This runs **prometheus + per-node-data + oslat** with safe, short-duration
defaults. App-recovery-time is excluded because it reboots nodes — see
[its dedicated profile](../task-profiles/app-recovery-time-quickstart.yaml)
to run it separately.

Artifacts appear under `./kpi-collector-artifacts/`.

## Authentication summary

| Task | `--kubeconfig` | `--token` + `--thanos-url` | Admin required |
|------|:--------------:|:--------------------------:|:--------------:|
| prometheus | ✅ (auto-discovers Thanos) | ✅ | Yes (creates SA token) |
| per-node-data | ✅ | ❌ | Yes (privileged pods) |
| oslat | ✅ | ❌ | Yes (creates pods) |
| app-recovery-time | ✅ | ❌ | Yes (privileged pods, node reboot) |

## Orchestration

Tasks run **sequentially** by default. You can configure:
- **Order** — which tasks run first
- **Parallel mode** — run all tasks concurrently
- **Failure policy** — stop on first failure or continue
- **Cleanup** — delete pods created by per-node-data, oslat, and app-recovery-time (`orchestration.cleanup`, default `false`)

See the [orchestration section](tasks-configuration.md#orchestration) for details.

## Next steps

| Goal | Where to go |
|------|-------------|
| Full configuration reference | [Tasks Configuration](tasks-configuration.md) |
| Ready-to-use examples | [Task Profiles](../task-profiles/README.md) |
| Annotated template | [`tasks.yaml.template`](../tasks.yaml.template) |
| Write your own per-task config | [Per-Node Data](per-node-data-configuration.md) · [Oslat](oslat-configuration.md) · [App Recovery Time](app-recovery-time-configuration.md) · [Prometheus KPIs](kpis-file-configuration.md) |
| First-time setup | [Getting Started](getting-started.md) |
