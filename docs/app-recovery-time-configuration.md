# App Recovery Time Configuration

This guide covers the app-recovery-time YAML configuration format — all
available fields, the reboot-and-poll workflow, and how to write your own
configuration file.

Related guides:
- [Getting Started](getting-started.md)
- [Tasks Configuration](tasks-configuration.md) — orchestrating multiple tasks
- [Task Profiles](../task-profiles/README.md) — ready-to-use quickstart and full examples

> ⚠️ **WARNING:** This task reboots real cluster nodes. Only use on disposable
> lab workers. Never run against production nodes.

## What it does

The app-recovery-time task measures how long workload pods take to recover
after a node reboot. It:

1. Creates a privileged debug pod on each listed node and issues a `reboot`
   command via `chroot /host`.
2. Waits until all listed nodes reach `NotReady` status.
3. Polls workload pod status in the configured namespaces for the specified
   duration, recording pod readiness at each interval.
4. Writes timestamped status snapshots to an artifact file.

**Artifact:** `pod_status.out` under `--artifacts-dir`

## Configuration format

The configuration lives under an `app-recovery-time:` root key — either inline
in a `tasks.yaml` or in a standalone file referenced via `configFile`.

### Minimal example

```yaml
app-recovery-time:
  workloadNamespaces:
    - default
  nodeNames:
    - worker-0.lab.example.com
  duration: 5m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

### Full example with all fields

```yaml
app-recovery-time:
  workloadNamespaces:
    - app-recovery-test
    - monitoring
  nodeNames:
    - worker-0.lab.example.com
    - worker-1.lab.example.com
  duration: 30m
  interval: 1m
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

## Field reference

| Field | Required | Default | Description |
|-------|----------|---------|-------------|
| `workloadNamespaces` | Yes | — | List of Kubernetes namespaces to poll for pod status during recovery. Choose namespaces where your test workloads run. |
| `nodeNames` | Yes | — | List of Kubernetes node names to reboot. Each node gets a privileged debug pod that issues `chroot /host reboot`. |
| `duration` | Yes | — | How long to poll pod status after the nodes reach NotReady. Accepts a Go duration string (e.g. `30m`, `1h`). |
| `interval` | Yes | — | How often to check pod status. Must be less than `duration`. Accepts a Go duration string (e.g. `30s`, `1m`). |
| `image` | Yes | — | Container image for the reboot debug pod. Must support `chroot` (the task runs `chroot /host reboot`). |

### Validation rules

- `workloadNamespaces` must contain at least one entry.
- `nodeNames` must contain at least one entry.
- `duration` must be greater than 0.
- `interval` must be greater than 0 and less than `duration`.
- `image` must not be empty.

## Using with a tasks file

### Inline

```yaml
# tasks.yaml
app-recovery-time:
  workloadNamespaces: [default]
  nodeNames: [worker-0.lab.example.com]
  duration: 5m
  interval: 30s
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

### External file via configFile

```yaml
# tasks.yaml
app-recovery-time:
  configFile: app-recovery-config.yaml
```

The external file must wrap the configuration under the `app-recovery-time:` root key:

```yaml
# app-recovery-config.yaml
app-recovery-time:
  workloadNamespaces:
    - app-recovery-test
  nodeNames:
    - worker-0.lab.example.com
  duration: 30m
  interval: 1m
  image: registry.access.redhat.com/ubi9/ubi-minimal:latest
```

`configFile` and inline fields are mutually exclusive — set one or the other.
Paths are resolved relative to the directory containing the tasks file.

## Duration and interval

The `duration` and `interval` fields control the recovery polling window:

| duration | interval | Snapshots | Use case |
|----------|----------|-----------|----------|
| `5m` | `30s` | 10 | Quick smoke test |
| `15m` | `1m` | 15 | Short validation |
| `30m` | `1m` | 30 | Standard validation run |
| `1h` | `2m` | 30 | Extended observation (slow-booting nodes) |

Set `duration` long enough for all rebooted nodes to become Ready and all
workload pods to reach Running. Node reboot typically takes 5–15 minutes
depending on hardware and boot configuration.

## Workload setup

For meaningful results, deploy a workload in the target namespace **before**
running the task. The task measures how long those pods take to reach Running
after the node reboots.

Any Deployment or StatefulSet with a readiness probe works. For example:

```bash
oc create namespace app-recovery-test
oc create deployment workload -n app-recovery-test \
  --image=registry.access.redhat.com/ubi9/ubi-minimal:latest \
  --replicas=3 -- sleep infinity
```

Then set `workloadNamespaces` to include that namespace.

## Image requirements

The reboot pod uses `chroot /host` followed by `reboot`. The image only needs to
support the `chroot` syscall — it does **not** need reboot utilities installed.

Recommended: `registry.access.redhat.com/ubi9/ubi-minimal:latest`

In disconnected environments, mirror the image to your local registry and
update the `image` field accordingly.

## Authentication

Requires `--kubeconfig` with admin privileges. The task creates privileged
reboot pods and lists nodes and pods via the Kubernetes API.

## Pod cleanup

Reboot pods stay on the cluster after the task finishes. Set
`orchestration.cleanup: true` to delete those reboot pods when the task
finishes or fails. Workload pods in `workloadNamespaces` are only polled;
they are not deleted.

## Safety considerations

- **Never run against production nodes.** The task issues a hard reboot —
  running workloads on those nodes will be interrupted.
- **Review `nodeNames` carefully.** Each listed node will be rebooted.
- **Use `orchestration.order`** in your tasks file to run `app-recovery-time`
  last, after less destructive tasks complete.
- **Test with a single node first** before adding multiple nodes to the list.
