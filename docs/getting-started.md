# Getting Started

This guide walks you through installing kpi-collector and running your first
collection. By the end you will have real data from your cluster stored locally.

## Prerequisites

Before you begin, make sure you have:

- **Access to an OpenShift cluster** via one of:
  - A kubeconfig file (`~/.kube/config` or a custom path) — required for non-Prometheus tasks; optional for Prometheus (auto-discovers Thanos URL and creates a token)
  - A bearer token and Thanos querier URL — Prometheus task only (see [Collecting Prometheus Metrics](collecting-metrics.md) for how to obtain these)
- **(Optional)** Docker or Podman, if you want to visualize Prometheus data in Grafana later

## Step 1: Install

Follow the [Installation](installation.md) guide to download and set up the binary.

Verify:

```bash
kpi-collector --help
```

> [!NOTE]
> All examples in this documentation assume `kpi-collector` is on your
> PATH. If you prefer not to move it, replace `kpi-collector` with the
> full path to the binary wherever it appears.

## Step 2: Choose how to run

kpi-collector supports two modes:

| Mode | When to use | Configuration |
|------|-------------|---------------|
| **Multi-task** (`--tasks`) | Run multiple task types (prometheus, per-node-data, oslat, app-recovery-time) in one go | A `tasks.yaml` file — see [Tasks Configuration](tasks-configuration.md) |
| **Prometheus-only** (`--prom-kpis-config`) | Collect only Prometheus/Thanos metrics (no other tasks) | A KPI YAML file — see [Prometheus KPI Configuration](kpis-file-configuration.md) |

### Option A: Multi-task quickstart

Use a ready-made [task profile](../task-profiles/README.md) to run all task types:

```bash
kpi-collector run \
  --tasks task-profiles/tasks-quickstart.yaml \
  --cluster-name my-cluster \
  --cluster-type ran \
  --kubeconfig /path/to/your/kubeconfig \
  --insecure-tls
```

This runs Prometheus KPI collection, per-node diagnostics, and an oslat plumbing
test — all sequentially. The `app-recovery-time` task is not included because it
reboots cluster nodes — see [App Recovery Time Configuration](app-recovery-time-configuration.md)
to add it explicitly. See [Tasks Configuration](tasks-configuration.md) for
the full schema and all four task types.

### Option B: Prometheus-only quickstart

Generate a KPI file matching your cluster type (`ran`, `core`, or `hub`):

```bash
kpi-collector kpis generate --profile ran --all
```

Or use a built-in profile directly:

```bash
kpi-collector run \
  --cluster-name my-cluster \
  --cluster-type ran \
  --kubeconfig /path/to/your/kubeconfig \
  --prom-kpis-config prom-kpi-profiles/kpis-quickstart.yaml \
  --once \
  --insecure-tls
```

Replace `/path/to/your/kubeconfig` with your kubeconfig path.

See [Prometheus KPI Configuration](kpis-file-configuration.md) for the full file format, all available fields, and the list of built-in profiles.

> [!NOTE]
> The `--insecure-tls` flag skips TLS certificate verification, which is
> common in lab and development clusters with self-signed certificates.
> Remove it if your cluster uses trusted certificates.

> [!TIP]
> If you don't have a kubeconfig, you can authenticate with a bearer token
> and Thanos URL instead — see [Collecting Prometheus Metrics](collecting-metrics.md)
> for details.

### Global flags (all tasks)

| Flag                | Required | Default                      | Purpose                                                                                                        |
| ------------------- | -------- | ---------------------------- | -------------------------------------------------------------------------------------------------------------- |
| `--cluster-name`    | Yes      | -                            | A label you choose to identify this cluster in the database                                                    |
| `--cluster-type`    | Yes      | -                            | Category for grouping clusters — `ran`, `core`, or `hub`                                                       |
| `--kubeconfig`      | No*      | -                            | Path to a kubeconfig with access to the cluster. Required for non-Prometheus tasks; optional for Prometheus (auto-discovers Thanos URL and creates a token) |
| `--tasks`           | No**     | -                            | Path to a `tasks.yaml` file (or a directory containing one) — see [Tasks Configuration](tasks-configuration.md) |
| `--prom-kpis-config`| No**     | -                            | Path to a Prometheus KPI YAML file — see [Prometheus KPI Configuration](kpis-file-configuration.md)            |
| `--once`            | No       | false                        | With `--prom-kpis-config` only: collect all KPIs once and exit (mutually exclusive with `--tasks`, `--frequency`, `--duration`) |
| `--parallel`        | No       | false                        | Run tasks concurrently (also settable via `orchestration.mode` in a tasks file)                                |
| `--insecure-tls`    | No       | false                        | Skip TLS certificate verification (dev/lab clusters with self-signed certs)                                    |
| `--tar-artifacts`   | No       | false                        | Create a `.tar.gz` archive of the artifacts directory after the run completes                                  |
| `--artifacts-dir`   | No       | `./kpi-collector-artifacts/` | Directory for database, logs, and output files                                                                 |

\* Required for non-Prometheus tasks; for Prometheus-only, can use `--token` + `--thanos-url` instead
\*\* Provide exactly one of `--tasks` or `--prom-kpis-config`

For Prometheus-specific flags (`--frequency`, `--duration`, `--token`, `--thanos-url`), see [Collecting Prometheus Metrics](collecting-metrics.md#prometheus-specific-cli-flags). Database flags (`--db-type`, `--postgres-url`) are global and work with both `--tasks` and `--prom-kpis-config`.

## Step 3: Check the artifacts

The tool creates a `kpi-collector-artifacts/` directory in your current working
directory (override with `--artifacts-dir`). What's inside depends on which
tasks ran:

| Task | Artifacts |
|------|-----------|
| **prometheus** | `kpi_metrics.db` (SQLite), `kpi-<timestamp>.log`, optional JSON output |
| **per-node-data** | `top-<node>.out`, `proc_meminfo-<node>.txt`, `proc_cmdline-<node>.txt`, `describe_pods_<ns>.txt`, `describe_nodes.txt` |
| **oslat** | `oslat_logs.out` |
| **app-recovery-time** | `pod_status.out` |

## Step 4: Verify Prometheus data (if applicable)

If you ran the prometheus task, check that your cluster was registered:

```bash
kpi-collector db show clusters
```

Expected output:

```
ID  CLUSTER_NAME   CREATED_AT            TOTAL_METRICS
--  ---            ---                   ---
1   my-cluster     2026-04-12 14:30:00   3
```

Query the collected KPI values:

```bash
kpi-collector db show kpis --name node-cpu-usage
```

All timestamps are displayed in UTC. If you see data, everything is working correctly.

## Next steps

- **Full validation run** — use `task-profiles/tasks-full.yaml` for production-scale durations. See [Task Profiles](../task-profiles/README.md).
- **Understand the tasks file** — learn how orchestration, task types, and `configFile` work in [Tasks Configuration](tasks-configuration.md).
- **Write your own task config** — each task type has a dedicated guide: [Per-Node Data](per-node-data-configuration.md), [Oslat](oslat-configuration.md), [App Recovery Time](app-recovery-time-configuration.md), [Prometheus KPIs](kpis-file-configuration.md).
- **Generate a full prometheus KPI profile** — use `kpi-collector kpis generate --profile ran --all` to create a comprehensive Prometheus KPI file tailored to your cluster type. See [Prometheus KPI Configuration](kpis-file-configuration.md).
- **Longer collection runs** — remove `--once` and use `--frequency 1m --duration 1h` to collect Prometheus metrics over time. See [Collecting Prometheus Metrics](collecting-metrics.md).
- **Visualize in Grafana** — launch a local Grafana dashboard with `kpi-collector grafana start --datasource=sqlite`. See [Grafana](grafana.md).
- **Query and manage data** — filter, sort, and export stored Prometheus metrics with `kpi-collector db show`. See [Database Commands](database-commands.md).
