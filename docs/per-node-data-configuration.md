# Per-Node Data Configuration

This guide covers the per-node-data YAML configuration format — all available
fields, what data is collected, and how to write your own configuration file.

Related guides:
- [Getting Started](getting-started.md)
- [Tasks Configuration](tasks-configuration.md) — orchestrating multiple tasks
- [Task Profiles](../task-profiles/README.md) — ready-to-use quickstart and full examples

## What it collects

The per-node-data task creates a privileged debug pod on **every** node in the
cluster and collects:

| Data | Source | Artifact file |
|------|--------|---------------|
| CPU usage over time | `top -b` via `taskset` on isolated CPUs | `top-<node>.out` |
| Memory information | `/proc/meminfo` | `proc_meminfo-<node>.txt` |
| Kernel command line | `/proc/cmdline` | `proc_cmdline-<node>.txt` |
| Pod descriptions | Kubernetes API (`kubectl describe pods -n <ns>`) | `describe_pods_<ns>.txt` |
| Node descriptions | Kubernetes API (`kubectl describe nodes`) | `describe_nodes.txt` |

All artifacts are written under `--artifacts-dir`.

## Configuration format

The configuration lives under a `per-node-data:` root key — either inline in a
`tasks.yaml` or in a standalone file referenced via `configFile`.

### Minimal example

```yaml
per-node-data:
  workloadNamespaces:
    - default
  duration: 15s
  interval: 5s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

### Full example with all fields

```yaml
per-node-data:
  workloadNamespaces:
    - default
    - my-app-ns
    - monitoring
  duration: 30m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
  isolcpus: "2-15"
```

## Field reference

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `workloadNamespaces` | Yes | — | List of Kubernetes namespaces. Pods in these namespaces are described and saved to artifact files. |
| `duration` | Yes | — | How long `top` runs on each node. Accepts a Go duration string (e.g. `30m`, `1h`, `5m30s`). |
| `interval` | Yes | — | `top` sampling period — how often a snapshot is taken. Must be less than `duration`. Accepts a Go duration string. |
| `image` | Yes | — | Container image for the privileged debug pod. Must support `chroot` (the task runs `chroot /host` to access host tools). |
| `isolcpus` | No | PerformanceProfile isolated CPUs | CPU list passed to `taskset -c` when running `top`. When omitted, filled from the cluster PerformanceProfile isolated CPU set. Set this to override that value. |

### Validation rules

- `workloadNamespaces` must contain at least one entry.
- `duration` must be greater than 0.
- `interval` must be greater than 0 and less than `duration`.
- `image` must not be empty.

## Using with a tasks file

### Inline

```yaml
# tasks.yaml
per-node-data:
  workloadNamespaces: [default]
  duration: 30m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

### External file via configFile

```yaml
# tasks.yaml
per-node-data:
  configFile: per-node-data-config.yaml
```

The external file must wrap the configuration under the `per-node-data:` root key:

```yaml
# per-node-data-config.yaml
per-node-data:
  workloadNamespaces:
    - default
    - my-app-ns
  duration: 30m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

`configFile` and inline fields are mutually exclusive — set one or the other.
Paths are resolved relative to the directory containing the tasks file.

## Duration and interval

The `duration` and `interval` fields control the `top` collection window:

| duration | interval | Snapshots | Use case |
|----------|----------|-----------|----------|
| `15s` | `5s` | 3 | Quick smoke test |
| `5m` | `10s` | 30 | Short validation |
| `30m` | `30s` | 60 | Standard validation run |
| `1h` | `1m` | 60 | Extended observation |

## Isolated CPUs (`isolcpus`)

The `isolcpus` field tells `top` which CPUs to monitor via `taskset -c`.

**Default:** When `isolcpus` is omitted, the tool reads the reserved and isolated
CPU sets from the cluster's PerformanceProfile CRs and uses the isolated set.
This is the same lookup used for Prometheus `{{RESERVED_CPUS}}` and
`{{ISOLATED_CPUS}}` placeholders. When both are needed, the sets are fetched
once and shared. Requires `--kubeconfig` and a PerformanceProfile on the cluster.

**Override:** Set `isolcpus` to monitor a specific CPU list instead of the
PerformanceProfile isolated set.

```yaml
per-node-data:
  isolcpus: "2-15"        # Range notation
  # isolcpus: "2,4,6,8"   # Comma-separated list also works
```

## Image requirements

The debug pod uses `chroot /host` to access host-level tools (`top`, `taskset`,
`cat`). The image itself only needs to support the `chroot` syscall — it does
**not** need these tools installed.

Recommended: `registry.access.redhat.com/ubi9/ubi-minimal:latest`

In disconnected environments, mirror the image to your local registry and
update the `image` field accordingly.

## Authentication

Requires `--kubeconfig` with admin privileges. The task creates privileged debug
pods and lists all cluster nodes via the Kubernetes API.

## Pod cleanup

Debug pods stay on the cluster after the task finishes. Set
`orchestration.cleanup: true` to delete them when the task finishes or fails.
Pods in `workloadNamespaces` are only described; they are not deleted.
