# 测试人员配置与验收手册

## 1. 阅读目标

本文面向监控平台测试人员。阅读后应能够找到监控功能配置，安全准备测试数据，并分别验证服务器采集、Prometheus、告警、钉钉、Grafana 和可选 HolmesGPT。Windows 运行环境和部署配置不在本文范围内。

本文记录的是 2026-08-04 仓库状态。真实服务器、钉钉机器人和模型账号可能变化，执行测试时应重新确认。

## 2. 测试安全边界

- 监控只能执行项目内置的只读 SSH/Erlang 诊断，不得在被监控服务器安装软件、写文件、停止进程或修改游戏配置。
- 不得在截图、缺陷单、聊天或报告中粘贴私钥、口令、Webhook、钉钉签名密钥、Grafana 密码、模型 API Key 或内部 Token。
- `secrets/` 下除说明文件外均为本地 Secret；只能检查文件是否存在、是否为空，不能打印内容。
- 生产配置必须使用已核验的 `host_key_sha256` 或 `known_hosts_file`。`insecure_skip_host_key: true` 只允许临时测试。
- 测试钉钉前先确认机器人安全关键字或 HMAC，避免向错误群发送消息。
- Holmes 的 Admin 诊断必须经过单次审批；不得绕过审批或扩大允许命令范围。

## 3. 配置总览

| 配置 | 用途 | 敏感值 |
|---|---|---:|
| `config/servers.native.yml` | Windows/Linux 原生运行时共用的当前测试服务器、周期和阈值 | 不应含口令 |
| `config/servers.yml` | 正式服务器清单 | 不应含口令 |
| `config/servers.example.yml` | 可提交的服务器配置模板 | 否 |
| `prometheus/prometheus.local.yml` / `prometheus.yml` | 抓取、规则和 Alertmanager 目标 | 否 |
| `prometheus/rules/erlang-alerts.yml` | 16 条告警规则 | 否 |
| `alertmanager/alertmanager.local.yml` / `alertmanager.yml` | 告警分组与钉钉路由 | 否 |
| `grafana/provisioning-local/` / `provisioning/` | 数据源、目录和插件接入 | Token 从环境或 Secret 读取 |
| `grafana/dashboards*/` | 9 台服务器的预置页面 | 否 |
| `holmes/config.*.yml` | Holmes 工具集和隔离策略 | 否 |
| `holmes/gateway.*.yml` | Holmes 网关、模型别名和限制 | 否 |
| `holmes/model_list.local.yaml` | 实际模型 ID 与环境变量引用 | 不直接写 Key |
| `secrets/*` | 密钥、Token、密码和接收人 | 是，禁止提交 |

```text
服务器配置 ──> Exporter ──> Prometheus ──> 告警规则 ──> Alertmanager ──> Exporter钉钉适配 ──> 钉钉
                       └──────────────> Grafana 仪表盘与插件
Prometheus + 服务器配置 ──> Holmes Gateway ──> HolmesGPT ──> GLM/Kimi
```

## 4. 服务器与采集配置

### 4.1 当前本地测试清单

当前 `servers.native.yml` 有 9 个启用项：

| ID | 名称 | SSH | 周期 | 认证 | 实例目录 |
|---|---|---|---:|---|---|
| `external-live-check` | `101.34.55.142` | `43999/admin` | 30m | SSH Agent | `/data/server` |
| `qt01-ga` | `101.35.19.137` | `43999/admin` | 30m | SSH Agent | `/data/server` |
| `qt01-gb` | `150.158.94.69` | `43999/admin` | 30m | SSH Agent | `/data/server` |
| `qt01-test-s0` | `162.14.141.52` | `43999/admin` | 30m | SSH Agent | `/data/server` |
| `qt01-gd` | `49.234.183.253` | `43999/admin` | 30m | SSH Agent | `/data/server` |
| `qt01-internal-debug` | `192.168.100.23` | `61618/root` | 5m | 私钥文件 | `/data` |
| `qt01-internal-act` | `192.168.100.25` | `61618/root` | 5m | 私钥文件 | `/data` |
| `qt05-internal-debug` | `192.168.100.33` | `61618/root` | 5m | 私钥文件 | `/data` |
| `qt05-internal-act` | `192.168.100.37` | `61618/root` | 5m | 私钥文件 | `/data` |

风险：这 9 项当前都使用 `insecure_skip_host_key: true`。测试报告应明确记录主机身份校验被跳过，不能把该结果视为安全校验通过。

### 4.2 顶层结构与热加载

```yaml
defaults:       # 所有服务器的默认参数
alert_filters:  # 只过滤钉钉通知的节点规则
servers:        # 服务器清单；单台可覆盖 defaults
```

Exporter 每 5 秒按文件内容 SHA-256 检查变化。有效配置会热加载并立即采集；无效配置保留最后一份有效配置。

```powershell
Invoke-RestMethod http://127.0.0.1:20903/config/status
```

成功后 `version` 递增；失败时检查 `last_error`。接口不返回用户名、密钥路径或口令。

### 4.3 采集与阈值字段

时长采用 Go duration 格式，例如 `10s`、`5m`、`24h`，必须大于 0。

| 字段 | 当前默认 | 含义/约束 |
|---|---:|---|
| `poll_interval` | 30m | 自动采集周期；单台可覆盖 |
| `confirm_interval` | 10s | 主机/整机失败后的复核等待 |
| `node_failure_confirm_interval` | 3m | 节点连接或实例进程消失后的定向复核等待 |
| `confirm_attempts` | 1 | 必须为 1，避免连续复核造成压力 |
| `connect_timeout` | 10s | SSH 建连超时 |
| `command_timeout` | 45s | 远端只读命令超时 |
| `collection_stale_after` | 40m | 超过此时间没有成功采集即过期 |
| `queue_threshold` | 100 | 最大单进程消息队列阈值 |
| `memory_threshold_mb` | 200 | 最大单 Erlang 进程内存阈值，MiB |
| `host_cpu_alert_percent` | 80 | 主机 CPU 阈值，1–100 |
| `host_memory_alert_percent` | 80 | 主机内存阈值，1–100 |
| `vm_memory_display_gb` | 10 | BEAM 总内存展示门槛，不得大于告警值 |
| `vm_memory_alert_gb` | 15 | BEAM 总内存告警阈值，GiB |
| `capacity_alert_percent` | 80 | Process/Atom/Port 容量阈值，1–100 |
| `run_queue_display_multiplier` | 4 | Run Queue 相对调度器数量的展示倍数 |
| `run_queue_alert_multiplier` | 16 | 告警倍数，不得小于展示倍数 |

代码兜底 `poll_interval` 为 5m、`command_timeout` 为 30s，但项目模板明确配置为 30m 和 45s。预期以实际加载配置为准。

### 4.4 单台服务器字段

| 字段 | 条件 | 说明 |
|---|---|---|
| `id` | 必填 | 唯一，字母/数字开头，只含字母、数字、`_`、`-` |
| `name` | 可选 | 页面/指标展示名；空值回退到 `id`；已有页面关联时保持稳定 |
| `enabled` | 可选 | 默认启用；`false` 不连接、不采集 |
| `address` | 启用时必填 | `IP:SSH端口` |
| `username` | 启用时必填 | SSH 用户 |
| `use_ssh_agent` | 可选 | `true` 使用已解锁的本机 Agent |
| `ssh_key_file` | Agent 模式必填 | 读取公钥身份，选择 Agent 中同一把密钥 |
| `private_key_file` | 非 Agent 模式必填 | 私钥路径，不把内容写入 YAML |
| `private_key_passphrase_file` | 可选 | 口令文件，不能与环境变量方式并用 |
| `private_key_passphrase_env` | 可选 | 口令环境变量名，不能与文件方式并用 |
| `host_key_sha256` | 正式环境二选一 | 经可信渠道核验的主机指纹 |
| `known_hosts_file` | 正式环境二选一 | 已核验的 known_hosts |
| `insecure_skip_host_key` | 仅临时测试 | 跳过主机校验；正式配置必须为 `false` |
| `filesystem_path` | 可选 | 磁盘采集路径，默认 `/` |
| `instance_directory` | 可选 | 预期游戏实例目录，如 `/data` 或 `/data/server` |

`instance_directory` 会识别有效 `wl_*`/`ysmw_*` 实例并排除日志、临时、备份、`saccter`、`.bk` 等目录。预期实例存在但 `beam.smp` 消失时进入失败复核和告警。

### 4.5 钉钉节点过滤

```yaml
alert_filters:
  ignored_nodes:
    qt01-internal-act:
      - "wl_act_8.bk.*"
      - "wl_temporary_?@127.0.0.1"
```

- 键必须是已存在的 server `id`；值支持精确名称和 `*`、`?`、`[]` glob。
- 命中后只取消钉钉发送，Prometheus、Alertmanager、Grafana 仍保留告警。
- 空模式、非法 glob 或未知 server ID 会拒绝整次热加载。
- 用日志 `dingtalk-alerts-filtered` 和指标 `dingtalk_adapter_alerts_filtered_total` 验证。

## 5. Secret 与环境配置

每个 Secret 文件只写一个值且不加引号；私钥保留原始格式。

| Secret | 用途 |
|---|---|
| `monitor_private_key` / `monitor_private_key_passphrase` | 容器 SSH 私钥及口令 |
| `dingtalk_webhook_url` / `dingtalk_secret` | 机器人 Webhook 与 HMAC |
| `dingtalk_at_mobiles` | 手机号列表，逗号/分号/换行分隔 |
| `dingtalk_at_user_ids` | 用户 ID 列表，逗号/分号/换行分隔 |
| `grafana_admin_password` | Grafana 管理员密码 |
| `ssh/qt_identity` | 当前本地 4 台内网服务器的 SSH 私钥 |
| `holmes_api_key` | 网关调用 Holmes 的独立 Key |
| `holmes_tool_api_token` | Grafana 后端代理调用网关的 Token |
| `glm_api_key` / `kimi_api_key` | 模型账号 Key |

两个接收人文件都不存在时，告警仍发送但不 @ 人，禁止测试人员猜测接收人。server ID 匹配 `qt[0-9]+-internal-.*` 时，Alertmanager 路由到 `mention=false`，同样发送但不 @ 人。

## 6. Prometheus、告警与钉钉

两套 Prometheus 配置的监控语义一致：抓取和规则计算 1m，Holmes Gateway 抓取 30s，数据保留 30d。

告警中的“等待”是 Prometheus 条件持续成立的时间，不包含 Exporter 自己的复核时间。例如节点失败会先等待 3 分钟做一次定向复核，复核后指标仍为失败，Prometheus 再按规则确认。因此从第一次异常到钉钉通知，实际时间可能比表中等待时间更长。

### 6.1 采集链路告警

| 告警 | 级别/等待 | 表示什么 | 常见原因与检查重点 |
|---|---|---|---|
| `ErlangExporterUnavailable` | critical / 3m | Prometheus 连续 3 分钟无法抓取 Exporter。此时所有服务器和节点的数据都会停止更新。 | 先检查 Exporter 服务、监听端口和启动日志。这不是某一台游戏服务器的故障。 |
| `ErlangServerCollectionFailed` | critical / 1m | 某台服务器的一轮整体采集失败，并且经过默认 10 秒快速复核仍未恢复。该服务器下的全部节点指标都可能不再更新。 | 检查 SSH 地址、端口、密钥、主机身份校验、远端 BEAM 发现和 Erlang Cookie。它只影响标签中指定的服务器。 |
| `ErlangNodeCollectionFailed` | critical / 5m | SSH 可能仍正常，但指定 Erlang 节点 RPC 不通，或预期实例目录还在而对应 `beam.smp` 已消失。其他节点可能正常。 | Exporter 默认等待 3 分钟，只复核失败节点一次；随后 Prometheus 还要求失败指标持续 5 分钟。检查节点进程、节点名、Cookie 和分布式端口。 |
| `ErlangCollectionStale` | critical / 1m | 距离最近一次成功采集已经超过 `collection_stale_after`，默认 40 分钟。页面上的旧值还可能存在，但已不能代表当前状态。 | 查看最近成功时间、Exporter 状态、SSH 和 RPC。它与“当前一次采集失败”不同：只有长时间没有任何成功样本才触发。 |

四者的区别：

- Exporter 不可用：监控采集器整体失联，影响所有服务器。
- Server 采集失败：某台服务器整体失败。
- Node 采集失败：某台服务器里的个别 Erlang 节点失败。
- 数据过期：不是描述一次失败，而是强调成功数据已经太旧。

### 6.2 Erlang 节点风险告警

| 告警 | 级别/等待 | 触发条件 | 风险与检查重点 |
|---|---|---|---|
| `ErlangVMMemoryHigh` | warning / 1m | 节点 BEAM 总内存超过 `vm_memory_alert_gb`，当前默认 15 GiB。 | 说明整个 VM 内存偏高，不等于某一个进程超限。检查 `erlang:memory/0`、ETS、binary 和高内存进程。 |
| `ErlangMessageQueueHigh` | warning / 1m | 节点内消息队列最大的进程超过 `queue_threshold`，当前默认 100 条。告警会携带 PID、注册名和当前函数。 | 进程消费消息跟不上，继续积压可能推高延迟和内存。重点检查该 PID 的调用流量、当前函数和消费速度。 |
| `ErlangProcessMemoryHigh` | warning / 1m | 节点内存最大的单个 Erlang 进程超过 `memory_threshold_mb`，当前默认 200 MiB。 | 这是单进程风险，与 BEAM 总内存告警不同。检查进程消息、binary、dictionary 和持有的业务状态。 |
| `ErlangProcessCapacityHigh` | warning / 1m | 当前进程数占 `process_limit` 的比例超过 `capacity_alert_percent`，默认 80%。 | 接近上限后将无法创建新进程。排查进程泄漏、批量创建和长期存活的临时进程。 |
| `ErlangAtomCapacityHigh` | warning / 1m | Atom 数占 `atom_limit` 的比例超过默认 80%。 | Atom 不会被垃圾回收，耗尽可能导致 BEAM 终止。优先排查 `list_to_atom` 等动态 Atom 创建路径。 |
| `ErlangPortCapacityHigh` | warning / 1m | Port 数占 `port_limit` 的比例超过默认 80%。 | 接近上限后 socket、文件或外部程序可能无法打开。排查 socket、文件描述符和外部 Port 泄漏。 |
| `ErlangRunQueueSustainedHigh` | warning / 1m | Run Queue 首次超过“在线调度器数 × 16”后，Exporter 等待默认 10 秒定向复核；复核仍高且 Prometheus 最近 10 分钟记录的最低倍数仍超过阈值。该告警仅在平台内保留，不推送钉钉。 | 表示持续调度拥堵，不是瞬时尖峰。先核对 Run Queue 和调度器数，再用受控热点采样检查 reductions 较高的进程。 |

除 Run Queue 外，上述内存、队列和容量告警直接由 Prometheus 根据采集值判断，不会要求 Exporter 再采一次才告警。这样可以避免资源风险因额外复核而延迟。

### 6.3 主机资源告警

| 告警 | 级别/等待 | 触发条件 | 风险与检查重点 |
|---|---|---|---|
| `RemoteHostMetricsFailed` | warning / 1m | CPU、内存、负载或磁盘等主机指标采集失败，并经默认 10 秒复核仍失败。Erlang 节点 RPC 可能仍然正常。 | 检查 SSH 命令权限、`/proc` 和 `df`；不要直接判断游戏节点已经掉线。 |
| `RemoteHostCPUHigh` | warning / 1m | 主机 CPU 使用率超过 `host_cpu_alert_percent`，默认 80%。 | 影响整机上的 BEAM 调度和其他系统任务。检查 CPU 持续时间、BEAM 调度器利用率及其他高 CPU 进程。 |
| `RemoteHostMemoryHigh` | warning / 1m | 按 `MemAvailable` 计算的主机内存使用率超过 `host_memory_alert_percent`，默认 80%。 | 可能引起 swap 或 OOM，影响这台主机上的全部节点。检查 BEAM 总内存、系统缓存、其他进程和 OOM 日志。 |
| `RemoteHostDiskLow` | warning / 15m | `filesystem_path` 所在文件系统可用比例持续 15 分钟低于 15%，但仍不低于 8%。 | 属于提前预警。检查日志或数据增长来源，按既定运维流程处理。 |
| `RemoteHostDiskCritical` | critical / 5m | 同一文件系统可用比例持续 5 分钟低于 8%。 | 磁盘接近耗尽，日志、数据或临时文件可能写入失败，需要立即升级处理。 |

主机 CPU、内存和磁盘告警描述的是整台机器；`ErlangVMMemoryHigh`、消息队列、进程内存和容量告警描述的是某个 BEAM 节点。测试报告应保留 `server`、`name`、`address`、`node` 标签，以免把主机问题和节点问题混为一谈。

### 6.4 钉钉发送策略

Alertmanager 按 `alertname/severity/server/node` 分组，首次等待 15s、同组间隔 5m、未恢复前重复间隔 8760h。因此同一事件通常只发送一次，恢复后再次触发才视为新事件。触发和恢复都通知，恢复不 @ 人。

## 7. Grafana 配置与页面

端口为 `20900`，语言 `zh-Hans`，匿名用户为 Viewer，禁止匿名注册。Prometheus 是唯一默认数据源，UID 固定为 `prometheus`。

| 目录 | 页面 |
|---|---|
| `qt-01` | `101.34.55.142(game) (gc)`、`101.35.19.137(game) (ga)`、`150.158.94.69(game) (gb)`、`162.14.141.52(test_game,s0)`、`49.234.183.253(game) (gd)` |
| `qt-01内网` | `192.168.100.23(debug)`、`192.168.100.25(act)` |
| `qt-05内网` | `192.168.100.33(debug)`、`192.168.100.37(act)` |

新增 server 不会自动生成仪表盘。必须同时确认对应 Dashboard JSON 已提供、标题正确、`server` 变量固定到对应 `name`，并加入正确目录/标签。

插件读取 Exporter、Holmes Gateway 地址和只从 Secret/环境注入的 `HOLMES_TOOL_API_TOKEN`。Dashboard 自动查询刷新固定为 30m；页面 `Refresh` 会触发当前服务器异步采集，等待采集和 Prometheus 下一次抓取后刷新。按钮置灰 10s 只是前端防连点。

## 8. HolmesGPT 可选配置

基础监控不依赖 Holmes。没有真实模型 Secret 时，不应声称 GLM/Kimi 已通过。

`holmes/config.local.yml` 和 `config.container.yml` 配置 `max_steps: 20`，只启用只读 Prometheus 工具，禁用 bash、Kubernetes、internet，并加载项目专用 skill。

复制 `holmes/model_list.example.yaml` 为 Git 忽略的 `holmes/model_list.local.yaml`，只替换账号当前可用模型 ID。API Key 必须继续使用 `{{ env.GLM_API_KEY }}` 和 `{{ env.KIMI_API_KEY }}`。

| 网关限制 | 当前值 | 约束 |
|---|---:|---|
| `max_range` | 24h | 不得超过 24h |
| `investigation_timeout` | 5m | 正数 |
| `tool_timeout` | 45s | 不得超过 45s |
| `max_tool_calls` | 12 | 1–50 |
| `max_output_bytes` | 262144 | 32 KiB–4 MiB |
| `max_sessions` | 100 | 1–10000 |
| `session_retention` | 168h | 正数 |
| `max_user_running` | 1 | 正数且不大于全局值 |
| `max_global_running` | 2 | 正数 |

真实冒烟使用 `scripts/smoke-real-models.ps1`，分别记录 GLM/Kimi 是否先获取 Prometheus 证据再调用受控诊断，并区分认证、限流、拒绝和超时。

## 9. 核心测试场景

### 9.1 配置加载与热更新

1. 记录 `/config/status` 版本。
2. 修改测试服务器的非敏感阈值并保存。
3. 约 5 秒后确认版本递增并立即采集。
4. 临时写入 `confirm_attempts: 2`，确认配置被拒绝、`last_error` 明确、旧配置继续工作。
5. 恢复正确配置并确认再次加载。

### 9.2 SSH 分层记录

必须分别记录 TCP 端口、SSH 握手、公钥认证、远端命令执行和 Erlang 只读采集。前一层成功不能代替后一层成功。

### 9.3 调度控制

```powershell
Invoke-RestMethod -Method Post http://127.0.0.1:20903/schedule `
  -ContentType application/json -Body '{"server":"qt01-ga","mode":"refresh"}'
Invoke-RestMethod -Method Post http://127.0.0.1:20903/collect `
  -ContentType application/json -Body '{"server":"qt01-ga"}'
Invoke-RestMethod -Method Post http://127.0.0.1:20903/schedule `
  -ContentType application/json -Body '{"server":"qt01-ga","mode":"auto"}'
```

`refresh` 停止自动触发但保留指标；手动采集不会恢复定时器；切回 `auto` 后等待完整周期。

### 9.4 钉钉与页面

- 用测试机器人验证触发只发一次、恢复可发送且不 @ 人。
- 普通外网 server 按配置 @ 人；`qt*-internal-*` 发送但不 @ 人。
- `ignored_nodes` 命中时只过滤钉钉，不影响 Prometheus、Alertmanager、Grafana。
- 发送失败应返回 HTTP 502，钉钉 health 为 503，日志不得出现敏感值。
- 9 个页面标题、目录和服务器数据一一对应；中文状态显示“在线/离线”。
- CPU 为单核 100% 口径；内存/磁盘以 M 显示并保留两位；网络以 KB/s 显示。
- Refresh 只触发当前服务器；新增 server 没有对应 Dashboard 应判配置不完整。

## 10. 验收层级

项目验证应覆盖配置解析、Go 测试、Exporter/Holmes 示例配置、Grafana JSON、插件测试和 Secret 泄漏模式。测试报告只记录实际执行并取得证据的项目。

报告必须分层记录，不能合并成“全部通过”：

| 层级 | 通过条件 |
|---|---|
| 静态配置 | YAML/JSON 解析和字段校验通过 |
| 测试/构建 | Go、插件测试和构建通过 |
| 监控服务 | Exporter、Prometheus、Alertmanager、Grafana 对应健康检查通过 |
| 真实服务器 | 分层 SSH 与真实采集有当前证据 |
| 告警链路 | Prometheus → Alertmanager → 钉钉触发/恢复有证据 |
| 页面渲染 | 浏览器实际渲染、单位、筛选和刷新通过 |
| Holmes | 真实 GLM/Kimi、受控工具和审批分别通过 |

## 11. 常见失败

| 现象 | 优先检查 |
|---|---|
| 无服务器或认证字段错误 | `servers`、认证模式及必填字段 |
| Agent 模式失败 | `ssh_key_file` 与 Agent 已解锁身份是否一致 |
| 主机密钥失败 | 指纹/known_hosts 是否可信，禁止直接跳过掩盖问题 |
| 热加载版本不变 | `/config/status.last_error`、YAML 与字段约束 |
| 页面有但无数据 | server `name`、Exporter 采集、Prometheus target |
| Refresh 后仍是旧值 | 等待异步采集及下一次 1m 抓取，不要只按 F5 |
| 告警有但钉钉无 | route、ignored_nodes、Webhook/HMAC、模块 health |
| 钉钉无 @ | 接收人文件、内网 no-mention 规则 |
| Holmes 不可用 | profile/进程、Secret、模型列表、20904 health |
| 模型限流/超时 | 按明确错误码记录，不归因成基础监控故障 |

## 12. 测试结果模板

```text
日期/人员：
Git提交或包版本：
服务器配置文件与启用数量：

静态配置：通过 / 失败 / 未执行（证据）
测试与构建：通过 / 失败 / 未执行（证据）
本地服务：通过 / 失败 / 未执行（逐端口）
真实SSH：TCP / 握手 / 认证 / 命令 / Erlang采集（分别记录）
Prometheus与16条规则：
Alertmanager与钉钉触发/恢复：
Grafana 9个页面和单位：
Holmes GLM：通过 / 失败 / 未配置
Holmes Kimi：通过 / 失败 / 未配置
临时配置及恢复情况：
未验证项和原因：
缺陷单链接：
```
