# Task Profiles

Ready-to-use task configuration files for `kpi-collector run --tasks`. Each
task type has a **quickstart** (fast smoke test) and a **full** (validation-scale)
variant, plus two combined orchestration files that wire all four tasks together.

## Quick start

From the repository root:

```bash
kpi-collector run \
  --tasks task-profiles/tasks-quickstart.yaml \
  --cluster-name my-cluster --cluster-type ran \
  --kubeconfig ~/.kube/config --once
```

> **⚠️  app-recovery-time reboots nodes.** Edit `nodeNames` in the recovery
> fragment (or remove that task from the orchestration `order`) before running.
>
> Pods created by per-node-data, oslat, and app-recovery-time stay on the cluster
> unless `orchestration.cleanup` is `true`.

## Available files

### Combined task files

| File | Tasks included | Purpose |
|------|----------------|---------|
| `tasks-quickstart.yaml` | prometheus, per-node-data, oslat | Fast end-to-end smoke test |
| `tasks-full.yaml` | prometheus, per-node-data, oslat | Lab validation with production-scale durations |

> **app-recovery-time is excluded** from both combined files because it reboots
> cluster nodes. To add it, follow the instructions in the comments at the top
> of each file, or use the standalone `app-recovery-time-quickstart.yaml` /
> `app-recovery-time-full.yaml` fragments directly.

Both files use `configFile` to reference the per-task fragments below and the
Prometheus KPI profiles under [`../prom-kpi-profiles/`](../prom-kpi-profiles/README.md).

### Per-task fragments

Each fragment is a standalone file with a wrapped root key (e.g. `per-node-data:`).
They can be referenced from a tasks file via `configFile`, or used as a
starting point for your own configuration.

| Task | Quickstart | Full | Config guide |
|------|-----------|------|--------------|
| **per-node-data** | `per-node-data-quickstart.yaml` | `per-node-data-full.yaml` | [docs](../docs/per-node-data-configuration.md) |
| **oslat** | `oslat-quickstart.yaml` | `oslat-full.yaml` | [docs](../docs/oslat-configuration.md) |
| **app-recovery-time** | `app-recovery-time-quickstart.yaml` | `app-recovery-time-full.yaml` | [docs](../docs/app-recovery-time-configuration.md) |

### Prometheus KPI profiles

Prometheus task configuration lives in [`../prom-kpi-profiles/`](../prom-kpi-profiles/README.md) — the
same profiles used by `kpi-collector run --prom-kpis-config` and
`kpi-collector kpis generate`. No KPI YAML is duplicated here.

| Profile | KPIs | Use case |
|---------|------|----------|
| `kpis-quickstart.yaml` | 2 | Smoke test |
| `kpis-basic.yaml` | 11 | General cluster health |
| `kpis-ran.yaml` | 31 | RAN/DU with PerformanceProfile |
| `kpis-core.yaml` | 22 | Core / control-plane clusters |
| `kpis-hub.yaml` | 22 | Hub / ACM clusters |

To swap the Prometheus profile in a tasks file, change the `configFile` line
under the `prometheus:` section (e.g. `../prom-kpi-profiles/kpis-ran.yaml`), or
generate a custom KPI file with `kpi-collector kpis generate --profile ran --all`.

## What to customize

Before running a **full** validation, review and edit `CHANGE_ME` placeholders:

| Placeholder | Where | What to set |
|-------------|-------|-------------|
| `nodeNames` | `app-recovery-time-*.yaml` | Kubernetes node names of disposable lab workers |
| `workloadNamespaces` | `per-node-data-full.yaml`, `app-recovery-time-full.yaml` | Namespaces where your workload pods run |
| `isolcpus` | `per-node-data-full.yaml` | Isolated CPU list; omit to use the PerformanceProfile isolated CPU set |
| `image` (oslat) | `oslat-full.yaml` | Your real oslat container image |
| All `image` fields | All fragments | Mirror to a local registry in disconnected environments |

## Artifacts

Each task writes output under `--artifacts-dir` (default: `./kpi-collector-artifacts/`):

| Task | Artifacts |
|------|-----------|
| **prometheus** | `kpi_metrics.db` (SQLite), log file, optional JSON output |
| **per-node-data** | `top-<node>.out`, `proc_meminfo-<node>.txt`, `proc_cmdline-<node>.txt`, `describe_pods_<ns>.txt`, `describe_nodes.txt` |
| **oslat** | `oslat_logs.out` |
| **app-recovery-time** | `pod_status.out` |

## Safety

- **app-recovery-time reboots cluster nodes.** Only run on lab/disposable workers.
- **per-node-data and oslat** create privileged pods. Ensure your kubeconfig has
  sufficient permissions.
- The **quickstart** variants use short durations and harmless images, but
  `app-recovery-time` still triggers a real reboot if `nodeNames` is set.
