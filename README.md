# kube-helper

A collection of interactive TUI kubectl plugins built with [Bubble Tea](https://github.com/charmbracelet/bubbletea).

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

---

---

# kube-helper (한국어)

[Bubble Tea](https://github.com/charmbracelet/bubbletea) 기반의 인터랙티브 TUI kubectl 플러그인 모음입니다.

---

## 플러그인 목록

| 명령어 | 설명 |
|--------|------|
| `kubectl log` | Deployment 단위 인터랙티브 로그 뷰어 |
| `kubectl node` | 노드 관리 — drain, cordon, SSH, 리소스 사용량 |

---

## 설치

```bash
bash install.sh
```

두 바이너리를 빌드하여 `/usr/local/bin`에 설치합니다.

**필요 조건**
- Go 1.21+
- 유효한 kubeconfig로 설정된 `kubectl`

---

## kubectl log

Deployment의 모든 Pod 로그를 실시간으로 스트리밍하며, 필터링과 키워드 하이라이팅을 지원하는 TUI 뷰어입니다.

### 사용법

```bash
kubectl log                              # 전체 네임스페이스의 deployment 목록
kubectl log -n <namespace>              # 특정 네임스페이스 필터
kubectl log <deployment>                # 바로 로그 화면으로 진입
kubectl log -n <namespace> <deployment>
```

### 키 바인딩

**Deployment 목록**

| 키 | 동작 |
|----|------|
| `↑ / ↓` | 이동 |
| `/` | deployment 검색 / 필터 |
| `Enter` | 로그 화면 열기 |
| `q` | 종료 |

**로그 화면**

| 키 | 동작 |
|----|------|
| `↑ / ↓ / PgUp / PgDn` | 스크롤 |
| `/` | include 필터 — 일치하는 라인만 표시 |
| `!` | exclude 필터 — 일치하는 라인 숨김 |
| `0` | 필터 초기화 |
| `p` | Pod 선택 — 개별 Pod 토글 |
| `h` | 커스텀 하이라이트 키워드 추가 / 제거 (노란색) |
| `b / Esc` | deployment 목록으로 돌아가기 |
| `q` | 종료 |

### 주요 기능

- **실시간 스트리밍** — Deployment 소속 Pod 전체의 로그를 동시에 수신, Pod별 색상 구분
- **Include / Exclude 필터** — 버퍼된 로그 라인에 즉시 적용
- **Pod 선택기** — 특정 Pod의 로그만 선택해서 보기
- **키워드 하이라이팅**
  - 기본 내장: `fatal` / `panic` → 빨강 · `error` → 주황-빨강 · `warn` → 주황 · `debug` → 회색
  - 커스텀: `h` 입력 후 키워드 타이핑 → 노란색 하이라이트 (같은 키워드 재입력 시 제거)

---

## kubectl node

노드 목록을 테이블로 보여주고 실시간 CPU/메모리 사용량, SSH 접속, drain/cordon 작업을 지원하는 노드 관리 TUI입니다.

### 사용법

```bash
kubectl node                   # 전체 노드 목록
kubectl node <node-name>       # 바로 노드 상세 화면으로 진입
```

### 키 바인딩

**노드 목록**

| 키 | 동작 |
|----|------|
| `↑ / ↓` | 이동 |
| `/` | 이름, role, IP로 노드 검색 |
| `Enter / s` | SSH 접속 |
| `i` | 노드 상세 (Pod 목록) |
| `d` | Drain |
| `c` | Cordon |
| `u` | Uncordon |
| `r` | 노드 목록 새로고침 |
| `q` | 종료 |

**노드 상세 (Pod 목록)**

| 키 | 동작 |
|----|------|
| `↑ / ↓` | Pod 이동 |
| `s` | SSH 접속 |
| `d / c / u` | Drain / Cordon / Uncordon |
| `r` | Pod 목록 새로고침 |
| `b / Esc` | 노드 목록으로 돌아가기 |
| `q` | 종료 |

**확인 / 진행 화면**

| 키 | 동작 |
|----|------|
| `y` | 작업 확인 |
| `n / Esc` | 취소 |
| `Enter / b / Esc` | 완료 후 뒤로 가기 |

### 주요 기능

- **실시간 리소스 사용량** — 10초마다 `kubectl top nodes`로 CPU/메모리 자동 갱신
  - 색상: `< 50%` 초록 · `50–79%` 주황 · `≥ 80%` 빨강
  - metrics-server 미설치 시 `-`로 gracefully 표시
- **SSH** — `Enter`로 해당 노드에 SSH 접속, 세션 종료 후 TUI 자동 복귀
- **Drain** — `kubectl drain --ignore-daemonsets --delete-emptydir-data` 실행, 진행 로그 실시간 스트리밍
- **Cordon / Uncordon** — 확인 팝업 후 노드 스케줄링 상태 변경
- **노드 상세** — 선택한 노드에서 실행 중인 전체 네임스페이스의 Pod 목록 조회

---

## drain vs cordon 차이

| | cordon | drain |
|-|--------|-------|
| 새 Pod 스케줄링 차단 | ✅ | ✅ |
| 기존 Pod evict | ❌ | ✅ |
| 사용 사례 | 점검 준비, 소프트 격리 | 노드 재부팅 / 교체 |

작업 완료 후 `u`(uncordon)로 노드를 서비스에 복귀시킵니다.
