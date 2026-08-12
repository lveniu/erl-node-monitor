# Grafana 代码解析 MCP 插件

## 目标

`Erlang Monitor Controls` App 在 `/a/erlang-monitor-controls-app/code-analysis` 提供只读代码解析对话。页面不会直接访问代码目录，也不会在浏览器中保存 MCP Token；调用关系为：

```text
Grafana Editor
  -> Grafana plugin-proxy
  -> Code Analysis MCP (Streamable HTTP)
  -> 注册项目的 Agent + SKILL
  -> 完整 Markdown 分析报告
```

页面先执行标准 MCP `initialize`、`notifications/initialized` 和 `tools/list`，确认必要工具存在后再使用三个业务工具：

1. `list_projects`：读取服务端注册项目和精确 `repo_path`。
2. `inspect_repository`：在首轮分析前检查目录、Agent 与 SKILL。
3. `analyze_codebase`：执行只读代码分析。

页面不允许手填代码路径。首轮消息发送后，项目在当前会话内锁定，避免多轮上下文串到另一个项目。

## 多轮对话语义

Code Analysis MCP 的每次 `analyze_codebase` 都是独立、无持久化的分析进程。多轮能力由插件显式实现：

- 当前浏览器标签页使用 `sessionStorage` 保存最多 20 条用户/助手消息和 40 条 MCP 轨迹；关闭浏览器会话后不作为长期记忆保留。
- 追问最多携带最近 6 条消息；助手报告单条最多携带 6000 字符，用户消息单条最多 3000 字符，总问题严格限制在 MCP 的 16000 字符范围内。
- 追问提示明确要求重新依据当前源码核对，上一轮报告只用于理解指代，不作为已验证事实。
- “新建会话”清空前文并重新允许选择项目。
- 浏览器取消请求会中止本轮 HTTP 调用；是否已终止下游分析进程仍应结合 MCP 日志确认。

## Grafana 配置

插件 provisioning 需要以下字段：

```yaml
jsonData:
  codeMcpUrl: http://127.0.0.1:3100
secureJsonData:
  codeMcpToken: $CODE_MCP_AUTH_TOKEN
```

`codeMcpUrl` 是 Grafana 服务端看到的地址，不是操作员浏览器看到的地址：

- Windows 原生 Grafana 与 MCP 在同一台机器时可使用 `http://127.0.0.1:3100`。
- Grafana 在容器或另一台机器时，必须改为 Grafana 服务端可达的地址。容器内的 `127.0.0.1` 只指向该容器自身。
- 非回环 MCP 必须启用 Bearer Token、限制来源和网络 ACL；Token 只通过 Grafana `secureJsonData` 注入。
- 不要把 3100 端口无认证暴露到局域网或公网。

Grafana 代理路由仅允许 Editor：

- `GET code-mcp-health` -> `/healthz`
- `POST code-mcp` -> `/mcp`

## 使用流程

1. 以 Grafana Editor 或 Admin 登录。
2. 打开“代码解析”。
3. 从 MCP 注册项目清单中选择项目。
4. 输入具体问题，例如模块、函数、错误码、日志片段或调用链。
5. 页面先执行项目检查，再执行只读分析；右侧显示 MCP 调用轨迹。
6. 在同一会话中继续追问，或新建会话切换项目。

参考玩家 GM 场景时，应先询问命令是否存在、参数类型和调用方式。该页面不执行 GM；实际执行仍需进入带权限与二次确认的运维流程。

## 验证

```powershell
npm --prefix grafana/plugins/erlang-monitor-controls-app test
npm --prefix grafana/plugins/erlang-monitor-controls-app run build
```

构建和单元测试只能证明插件逻辑与 bundle 可生成。部署后还要分别验证 Grafana 服务端到 MCP 的网络、代理响应、真实 MCP 工具调用，以及浏览器页面的项目选择、多轮追问和取消行为。
