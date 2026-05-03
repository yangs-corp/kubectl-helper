# kube-helper

A collection of interactive TUI kubectl plugins built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

[한국어](./README_ko.md)

---

## Plugins

| Command | Description |
|---------|-------------|
| `kubectl log` | Interactive log viewer grouped by Deployment |
| `kubectl node` | Node manager — drain, cordon, SSH, resource usage |

---

## Installation

```bash
bash install.sh
```

Builds both binaries and copies them to `/usr/local/bin`.

**Requirements**
- Go 1.21+
- `kubectl` configured with a valid kubeconfig

---

## kubectl log

Browse deployment logs interactively with real-time streaming, filtering, and keyword highlighting.

### Usage

```bash
kubectl log                        # list all deployments (all namespaces)
kubectl log -n <namespace>         # filter by namespace
kubectl log <deployment>           # jump directly to log view
kubectl log -n <namespace> <deployment>
```

### Key bindings

**Deployment list**

| Key | Action |
|-----|--------|
| `↑ / ↓` | Navigate |
| `/` | Search / filter deployments |
| `Enter` | Open log view |
| `q` | Quit |

**Log view**

| Key | Action |
|-----|--------|
| `↑ / ↓ / PgUp / PgDn` | Scroll |
| `/` | Include filter — show only matching lines |
| `!` | Exclude filter — hide matching lines |
| `0` | Clear filter |
| `p` | Pod selector — toggle individual pods |
| `h` | Add / remove custom highlight keyword (yellow) |
| `b / Esc` | Back to deployment list |
| `q` | Quit |

### Features

- **Real-time streaming** — tails all pods of a deployment simultaneously, color-coded per pod
- **Include / exclude filter** — applied live to buffered log lines
- **Pod selector** — view logs from specific pods only
- **Keyword highlighting**
  - Built-in: `fatal` / `panic` → red · `error` → orange-red · `warn` → orange · `debug` → gray
  - Custom: press `h`, type a keyword → highlighted in yellow (toggle to remove)

---

## kubectl node

Manage Kubernetes nodes with real-time CPU/memory usage, SSH access, and drain/cordon operations.

### Usage

```bash
kubectl node                  # list all nodes
kubectl node <node-name>      # jump directly to node detail
```

### Key bindings

**Node list**

| Key | Action |
|-----|--------|
| `↑ / ↓` | Navigate |
| `/` | Search nodes by name, role, or IP |
| `Enter / s` | SSH into node |
| `i` | Node detail — pod list |
| `d` | Drain node |
| `c` | Cordon node |
| `u` | Uncordon node |
| `r` | Refresh node list |
| `q` | Quit |

**Node detail (pod list)**

| Key | Action |
|-----|--------|
| `↑ / ↓` | Navigate pods |
| `s` | SSH into node |
| `d / c / u` | Drain / cordon / uncordon |
| `r` | Refresh pod list |
| `b / Esc` | Back to node list |
| `q` | Quit |

**Confirm / Progress**

| Key | Action |
|-----|--------|
| `y` | Confirm action |
| `n / Esc` | Cancel |
| `Enter / b / Esc` | Back (after action completes) |

### Features

- **Live resource usage** — CPU and memory polled every 10 seconds via `kubectl top nodes`
  - Color coding: `< 50%` green · `50–79%` orange · `≥ 80%` red
  - Falls back to `-` gracefully if metrics-server is not installed
- **SSH** — press `Enter` on any node to open an SSH session; TUI resumes on exit
- **Drain** — runs `kubectl drain --ignore-daemonsets --delete-emptydir-data` with live output streaming
- **Cordon / Uncordon** — toggle scheduling on a node with confirmation prompt
- **Node detail** — shows all pods scheduled on the selected node across all namespaces

---

## Difference: drain vs cordon

| | cordon | drain |
|-|--------|-------|
| Blocks new pods | ✅ | ✅ |
| Evicts existing pods | ❌ | ✅ |
| Use case | Maintenance prep, soft isolation | Node reboot / replacement |

After either operation, run `uncordon` (`u`) to return the node to service.
