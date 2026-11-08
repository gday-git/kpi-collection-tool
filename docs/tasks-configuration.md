# Tasks Configuration

This guide covers the `tasks.yaml` file format — how to configure multiple
task types in a single file and run them with `kpi-collector run --tasks`.

Related guides:
- [Getting Started](getting-started.md)
- [Collecting Prometheus Metrics](collecting-metrics.md) — Prometheus authentication details and dynamic CPU IDs
- [Prometheus KPI Configuration](kpis-file-configuration.md) — Prometheus KPI file format
- [Per-Node Data Configuration](per-node-data-configuration.md) — per-node-data file format
- [Oslat Configuration](oslat-configuration.md) — oslat file format
- [App Recovery Time Configuration](app-recovery-time-configuration.md) — app-recovery-time file format
- [Task Profiles](../task-profiles/README.md) — ready-to-use quickstart and full examples

## Overview

A `tasks.yaml` file defines **which tasks to run** and **how to orchestrate
them**. Each task type has its own configuration section; an optional
`orchestration` block controls execution order and failure policy.

```yaml
orchestration:
  mode: sequential        # sequential (default) or parallel
  on-failure: fail-fast   # continue (default) or fail-fast
  cleanup: false          # true deletes pods created by per-node-data, oslat, and app-recovery-time
  order:                  # optional; defaults to: prometheus, per-node-data, oslat, app-recovery-time
    - prometheus
    - per-node-data
    - oslat
    - app-recovery-time

prometheus:
  configFile: prom-kpi-profiles/kpis-quickstart.yaml

per-node-data:
  workloadNamespaces: [default]
  duration: 15s
  interval: 5s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest

oslat:
  timeout: 5m
  pod:
    metadata:
      name: oslat-test
    spec:
      restartPolicy: Never
      containers:
        - name: oslat
          image: registry.access.redhat.com/ubi9/ubi-minimal:latest
          command: ["sh", "-c", "echo oslat; sleep 15"]

app-recovery-time:
  workloadNamespaces: [default]
  nodeNames: [worker-0.example.com]
  duration: 5m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

Run it:

```bash
kpi-collector run \
  --tasks tasks.yaml \
  --cluster-name my-cluster --cluster-type ran \
  --kubeconfig ~/.kube/config
```

`--tasks` accepts a path to a YAML file **or** a directory containing a file
named `tasks.yaml`. With `--tasks`, set `frequency` / `duration` / `once`
under `prometheus:` in the YAML (not as CLI flags). `--db-type` and
`--postgres-url` remain global CLI flags.

> [!IMPORTANT]
> `--tasks` and `--prom-kpis-config` are mutually exclusive. Use one or the other.

## Orchestration

The `orchestration` section is optional. Defaults are applied when fields are
omitted.

| Field | Values | Default | Description |
|-------|--------|---------|-------------|
| `mode` | `sequential`, `parallel` | `sequential` | Whether tasks run one at a time or concurrently. Also settable via `--parallel` on the CLI. |
| `on-failure` | `continue`, `fail-fast` | `continue` | `continue` runs remaining tasks even if one fails; `fail-fast` stops immediately. |
| `cleanup` | `true`, `false` | `false` | When `true`, delete pods created by per-node-data, oslat, and app-recovery-time after the task finishes, including on failure. Workload pods are not deleted. |
| `order` | list of task names | insertion order of present tasks | Explicit execution order. Must list exactly the task sections present in the file (no extras, no omissions). |

When `order` is omitted, tasks run in the fixed default order: `prometheus`,
`per-node-data`, `oslat`, `app-recovery-time` (only those present in the file).

> [!NOTE]
> The YAML examples below use illustrative values (durations, namespaces,
> images, etc.). For all required and optional fields with descriptions,
> see the field tables under each task type. For ready-to-use configurations
> with sensible defaults, see [Task Profiles](../task-profiles/README.md).

## Task types

### prometheus

Collects PromQL metrics from Prometheus/Thanos and stores them in a database
(SQLite or PostgreSQL). This is the same collection engine used by
`--prom-kpis-config`, but configured inside a tasks file.

**Configuration:**

Exactly one of `configFile` or inline `kpis` must be set:

```yaml
# Option 1: reference an external KPI file
prometheus:
  configFile: prom-kpi-profiles/kpis-ran.yaml

# Option 2: define KPIs inline
prometheus:
  kpis:
    - id: node-cpu-usage
      promquery: avg by (instance) (rate(node_cpu_seconds_total{mode!="idle"}[5m]))
```

`configFile` paths are resolved relative to the directory containing the tasks
file. Use the built-in profiles under `prom-kpi-profiles/` or generate one with
`kpi-collector kpis generate --profile ran --all`.

For the full KPI YAML format (sampling frequency, categories, range queries,
CPU placeholders), see [Prometheus KPI Configuration](kpis-file-configuration.md).

**Artifacts:** `kpi_metrics.db` (SQLite), log file, optional JSON output — all
under `--artifacts-dir`.

**Authentication:** Requires `--kubeconfig` (auto-discovers Thanos URL and
creates a token) or `--token` + `--thanos-url`. See
[Collecting Prometheus Metrics](collecting-metrics.md) for details.

---

### per-node-data

Collects CPU usage over time (`top`), `/proc/meminfo`, `/proc/cmdline` from
every cluster node via a privileged debug pod, plus pod and node describes
from the Kubernetes API.

**Configuration:**

```yaml
per-node-data:
  workloadNamespaces:
    - default
    - my-workload-ns
  duration: 30m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
  # isolcpus: "2-15"  # Optional; omit to use the PerformanceProfile isolated CPU set
```

Or split into a separate file:

```yaml
# In tasks.yaml:
per-node-data:
  configFile: per-node-data.yaml

# per-node-data.yaml (wrapped root key):
per-node-data:
  workloadNamespaces: [default]
  duration: 30m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

| Field | Required | Description |
|-------|----------|-------------|
| `workloadNamespaces` | Yes | Namespaces to describe pods from |
| `duration` | Yes | How long `top` runs (e.g. `30m`) |
| `interval` | Yes | `top` sample period; must be less than `duration` |
| `image` | Yes | Container image for the debug pod (must support `chroot`) |
| `isolcpus` | No | CPU list for `taskset`; if omitted, the PerformanceProfile isolated CPU set |

**Artifacts** (under `--artifacts-dir`):
- `top-<node>.out` — CPU usage over time
- `proc_meminfo-<node>.txt` — memory info
- `proc_cmdline-<node>.txt` — kernel command line
- `describe_pods_<ns>.txt` — pod YAML per workload namespace
- `describe_nodes.txt` — node YAML for all nodes

**Requires:** `--kubeconfig` with admin privileges (creates privileged pods and lists cluster nodes)

For the full per-node-data file format, all fields, and usage details, see
[Per-Node Data Configuration](per-node-data-configuration.md).

---

### oslat

Creates a user-supplied Pod on the cluster, waits until it finishes (or times
out), and writes pod logs to an artifact file.

**Configuration:**

```yaml
oslat:
  timeout: 30m
  pod:
    metadata:
      name: oslat-test
    spec:
      restartPolicy: Never
      containers:
        - name: oslat
          image: your-registry/oslat:latest
          command: ["oslat"]
          args: ["-D", "1800"]
```

Or split into a separate file:

```yaml
# In tasks.yaml:
oslat:
  configFile: oslat.yaml

# oslat.yaml (wrapped root key):
oslat:
  timeout: 30m
  pod:
    metadata:
      name: oslat-test
    spec:
      # ...
```

| Field | Required | Description |
|-------|----------|-------------|
| `timeout` | Yes | Maximum wait time for the pod to complete |
| `pod` | Yes | Full Kubernetes Pod spec (metadata + spec) |
| `pod.metadata.name` | Yes | Pod name |
| `pod.spec.containers[].image` | Yes | Container image for each container |

**Artifacts:** `oslat_logs.out` under `--artifacts-dir`

**Requires:** `--kubeconfig` with admin privileges (creates pods on the cluster)

For the full oslat file format, all fields, and pod spec tips, see
[Oslat Configuration](oslat-configuration.md).

---

### app-recovery-time

Reboots configured cluster node(s) via a privileged debug pod, waits until they
are NotReady, then polls workload pod status for the configured duration and
writes the results.

> ⚠️ **WARNING:** This task reboots real cluster nodes. Only use on disposable
> lab workers. Never run against production nodes.

**Configuration:**

```yaml
app-recovery-time:
  workloadNamespaces:
    - app-recovery-test
  nodeNames:
    - worker-0.lab.example.com
  duration: 30m
  interval: 1m
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

Or split into a separate file:

```yaml
# In tasks.yaml:
app-recovery-time:
  configFile: app-recovery-time.yaml
```

| Field | Required | Description |
|-------|----------|-------------|
| `workloadNamespaces` | Yes | Namespaces to poll for pod status |
| `nodeNames` | Yes | Kubernetes node names to reboot |
| `duration` | Yes | How long to poll pods after nodes are NotReady |
| `interval` | Yes | Poll period; must be less than `duration` |
| `image` | Yes | Container image for the reboot debug pod (must support `chroot`) |

**Artifacts:** `pod_status.out` under `--artifacts-dir` (timestamp blocks + pod status columns)

**Requires:** `--kubeconfig` with admin privileges (creates privileged reboot pods and lists nodes/pods)

For the full app-recovery-time file format, all fields, and safety guidance, see
[App Recovery Time Configuration](app-recovery-time-configuration.md).

## configFile pattern

Each task type supports a `configFile` field as an alternative to inline
configuration. The external file must wrap the configuration under the task's
root key:

```yaml
# tasks.yaml — reference only
per-node-data:
  configFile: per-node-data.yaml

# per-node-data.yaml — wrapped root key is required
per-node-data:
  workloadNamespaces: [default]
  duration: 30m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

Rules:
- `configFile` and inline fields are **mutually exclusive** — set one or the other.
- Paths are resolved **relative to the directory containing the tasks file**.
- Nested `configFile` (a configFile that itself contains `configFile`) is rejected.

For the prometheus task, `configFile` points to a KPI YAML file (which uses
`kpis:` as its root key, not `prometheus:`).

## Terminal progress

When tasks run, step-level progress messages appear on the terminal:

```
[prometheus] starting (2 KPIs, frequency=1m, duration=45m)
[per-node-data] starting
[per-node-data] found 3 node(s): worker-0, worker-1, worker-2
[per-node-data] worker-0: collecting (duration=30m, interval=30s)
[oslat] creating pod default/oslat-test
[oslat] waiting for pod (timeout=30m)
[oslat] still waiting (5m elapsed)
[app-recovery-time] creating reboot pod on worker-0
[app-recovery-time] polling workload pods for 30m (interval=1m)
```

Detailed diagnostics go to the log file under `--artifacts-dir`, not the terminal.

## Task profiles

Ready-to-use quickstart and full validation configurations are available under
[`task-profiles/`](../task-profiles/README.md). Use them as-is or as a starting
point for your own `tasks.yaml`.

For a fully annotated reference of all task types and fields, see
[`tasks.yaml.template`](../tasks.yaml.template) in the project root.
