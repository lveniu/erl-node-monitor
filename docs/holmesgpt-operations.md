# HolmesGPT 运维根因分析接入说明

## 已实现架构

HolmesGPT 固定为 `0.38.1`、Git commit `7af34f5e716e28adcbcbd584cd4708434929f183`。Windows 使用本机 Python 3.11 虚拟环境；CentOS 7 原生部署使用项目内便携 Python 3.11.15、裁剪后的固定依赖和独立 Linux 虚拟环境。两种虚拟环境不能跨操作系统复制，但加载相同版本的 Holmes 源码和服务端模型配置。容器文件仅保留为旧的可选开发入口，本次 Linux 发布不使用 Docker、Compose 或镜像仓库。

浏览器只访问 Grafana 同源插件代理。Grafana 以 `secureJsonData` 保存内部 Token，分别使用 Editor 调查路由和 Admin 审批路由；浏览器得不到 Holmes、模型或 SSH 凭据。Grafana 必须启用 `GF_DATAPROXY_SEND_USER_HEADER=true`，由数据代理注入真实 `X-Grafana-User`；网关拒绝仅有内部 Token、但没有真实 Grafana 用户名的请求，不使用共享用户名兜底。Go 网关监听 `127.0.0.1:20904`（容器内同为 `20904`），Holmes 统一使用 `20905`，仅在容器网络或本机回环可见。

网关负责：

- `glm`、`kimi` 服务端别名过滤，不返回上游模型 ID、API Base 或 Key。
- 会话 JSON 持久化、单会话并发、跨会话 `request_id` 原子唯一、创建/追问/审批幂等、7 天/100 会话保留、SSE 断线重连和重启安全失败。并发创建、追问或审批只能有一个处理方取得执行权，旧 Holmes 轮次的迟到结果不能覆盖新轮次或已取消状态。
- Holmes SSE 事件白名单转换；未知事件只记录名称，隐藏推理和认证字段被删除。
- 工具暂停/恢复、Admin 单次审批、12 次工具上限、累计 256 KiB 输出上限、单工具 10/45 秒超时。
- JSONL 审计和 Prometheus 指标；审计参数、结果和日志不记录认证头、Token、私钥或完整配置。

Grafana 页面为 `/a/erlang-monitor-controls-app/holmes`。每台 Erlang 仪表板会出现“Holmes 分析”入口，带入固定服务器、节点候选、当前时间范围，以及与所选节点匹配的一个活动告警；选中的有界告警标签和可选 fingerprint 会进入调查上下文，并在页面显示。网关把展示名重新解析成服务端稳定 ID，重新验证节点，且将告警标签视为不可信数据而不是模型指令。匿名 Viewer 可继续查看原监控，但 App 页面和调查代理至少需要 Editor；热点工具审批需要 Admin。

## 模型配置

复制 `holmes/model_list.example.yaml` 为 Git 忽略的 `holmes/model_list.local.yaml`，然后只替换当前账号实际可用的模型 ID：

- GLM 使用 `https://open.bigmodel.cn/api/paas/v4` OpenAI 兼容入口。
- Kimi 使用 `https://api.moonshot.cn/v1`，不显式配置 `temperature`。
- Key 始终来自 `GLM_API_KEY`、`KIMI_API_KEY` 环境变量；示例文件没有真实值。

普通聊天成功不等于可用。生产模型必须用 `scripts/smoke-real-models.ps1` 分别完成 Prometheus、受控诊断、至少两轮工具、审批暂停、成功的热点调用、最终中文 RCA 和连续追问。该脚本默认拒绝运行，只有提供 Secret、核对模型成本并同时传入 `-Execute -ApproveHotspots` 后才会调用真实供应商。`-RequireCompaction` 把真实上下文压缩作为强制门槛。

供应商错误使用预先创建的故障注入会话验收，不在命令行传 Key。传入 `-RequireProviderErrors -ProviderErrorSessionId <id1>,<id2>,<id3>,<id4>` 时，脚本要求这些会话合计覆盖 `HOLMES_AUTH_FAILED`、`MODEL_RATE_LIMITED`、`HOLMES_TIMEOUT` 和 `MODEL_REQUEST_REJECTED`，并同时验证无效网关 Token 返回 401。示例：

```powershell
.\scripts\smoke-real-models.ps1 -ServerId <stable-id> -Node <discovered-node> -Execute -ApproveHotspots -RequireProviderErrors -ProviderErrorSessionId <auth-id>,<rate-id>,<timeout-id>,<rejected-id>
```

## 受控诊断

开放工具只有：

- `get_host_snapshot`
- `list_erlang_nodes`
- `get_node_snapshot`
- `get_scheduler_hotspots`
- `get_process_hotspots`

工具只接受当前 `server_id`、当前发现节点、枚举指标和有界数值。服务端不接受地址、端口、用户名、密钥路径、URL 或命令字段。热点输出中的 `role_数字` 和 11 位以上业务标识会在进入模型前脱敏。通用 Shell、文件写入、服务启停、进程终止、完整消息、process dictionary、角色数据和 `mgectl exprs` 永久不可用。

连接结果分别记录 TCP、SSH 握手、公钥认证、远端命令会话和 Erlang RPC。`list_erlang_nodes`、主机快照不执行 RPC，因此即使前四步成功，RPC 仍正确显示为 `false`。调度器 wall-time 未预先启用时返回明确不支持；工具不会调用 `system_flag/2` 修改远端运行时。

经人工确认的命令行只读烟测入口：

```powershell
go run ./cmd/holmes-diagnostic-smoke -config config/servers.native.yml -server <stable-id> -tool list_erlang_nodes -execute
```

`-execute` 只批准这一次固定白名单调用，不等价于永久 SSH 授权。

## Windows 原生启动

Holmes 是可选组件，缺失或停止不会阻止现有 Exporter、Prometheus、Alertmanager、Grafana 和钉钉运行。

1. 安装 Python 3.11。
2. 运行 `scripts/install-holmes-local.ps1`，安装器核对固定 commit 并使用上游 lockfile。
3. 准备上述四个 Secret 和 `holmes/model_list.local.yaml`。
4. 运行 `scripts/start-holmes-local.ps1`。
5. 重启一次本地监控启动器，使 Grafana 子进程从 `secrets/holmes_tool_api_token` 注入插件代理 Secret，并启用真实用户头转发；值不会打印。当前运行中的 Grafana 不会被脚本或本次交付自动重启。

当前开发机的 Holmes Windows 虚拟环境为 Python 3.11.9。虚拟环境解释器与静态配置已检查；服务进程启动、模型调用和浏览器渲染仍要与各自的运行验证证据分开报告。

## CentOS 7 原生部署

Linux 不复制 Windows `.venv`，也不替换系统 Python 3.6。发布包包含便携 Python 3.11.15、HolmesGPT 0.38.1 固定源码、CentOS 7/x86_64 wheel 集和静态 Holmes Gateway。安装器在 `/opt/erlang-monitor/holmesgpt/.venv` 从零创建 Linux 虚拟环境，systemd 服务只监听回环 `20904/20905`。

部署入口要求精确 SVN revision：

```bash
sudo bash /data/node_monitor/linux/update-holmes-and-restart.sh --revision REVISION
```

脚本不会重启 Exporter、Alertmanager、Prometheus、Grafana 或 Nginx。它在部署前后分别检查原四项服务为 active；Holmes 与 Gateway 就绪后，通过 Grafana API 把 Tool Token 保存到 `secureJsonData`，再验证健康代理和鉴权模型代理。Grafana 不因本次 Holmes 部署重启，未来正常重启时由 `run-grafana.sh` 从 Secret 文件重新注入 Token。

## 容器启动

基础监控仍使用：

```text
docker compose -f compose.yml up -d
```

Holmes 服务放在可选 `holmes` profile，Grafana 的 Secret 挂载放在独立 overlay，避免缺少 Holmes Secret 时影响原监控：

```text
docker compose -f compose.yml -f compose.holmes.yml --profile holmes up -d
```

当前开发机没有 Docker，因此只进行配置和构建静态验证，没有拉取镜像、启动容器或验证容器网络。

## 验证层级

已验证：Go/前端测试、假 Holmes 多轮暂停恢复、跨会话请求 ID 与审批幂等、迟到轮次/取消保护、重启恢复、真实用户名缺失拒绝、活动告警上下文、独立依赖健康状态、权限、SSE、脱敏、参数边界、真实 Prometheus、真实 SSH 五阶段、真实节点 RPC 和真实短窗口热点；Linux 离线包校验、CentOS 7 兼容 Python 3.11.15、从零创建 Linux venv、关键模块导入及 x86-64 Gateway ELF 也已验证。

部署提交前仍未验证：目标机正式 systemd 部署、Grafana live `secureJsonData` 更新、真实 GLM 完整调查、工作台浏览器渲染、四类供应商故障注入会话和上下文压缩真实阈值。Kimi 未配置，不纳入第一版生产模型验收；容器不属于本次发布路径。静态构建、离线安装、服务健康、真实模型、代理调用和浏览器渲染是不同证据层级。
