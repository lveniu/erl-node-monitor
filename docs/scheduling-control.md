# 采集调度控制

Exporter 默认对每个启用服务器执行一次启动采集，随后按照该服务器的 `poll_interval` 周期采集。

Exporter 每5秒扫描一次启动参数 `-config` 指向的服务器配置。有效变化会热更新调度任务并立即采集；无效变化会保留最后一份有效配置。当前热加载版本和错误可通过 `GET /config/status` 查看。

切换为手动（`refresh`）模式会停止该服务器的定时触发，但不会删除已有指标；切回 `auto` 时会重新计时一个完整的 `poll_interval`，不会立即补采。

## API

接口位于 Exporter 的本地监听端口（默认 `127.0.0.1:20903`）。`server` 可以填写服务器 ID、IP 名称或 `address`。

```powershell
# 停止 qt01-ga 的定时采集
Invoke-RestMethod -Method Post http://127.0.0.1:20903/schedule `
  -ContentType 'application/json' `
  -Body '{"server":"qt01-ga","mode":"refresh"}'

# 手动触发一次采集；不会恢复定时器
Invoke-RestMethod -Method Post http://127.0.0.1:20903/collect `
  -ContentType 'application/json' `
  -Body '{"server":"qt01-ga"}'

# 恢复自动采集；下一次采集在完整 poll_interval 后发生
Invoke-RestMethod -Method Post http://127.0.0.1:20903/schedule `
  -ContentType 'application/json' `
  -Body '{"server":"qt01-ga","mode":"auto"}'
```

Grafana Dashboard 的自动查询刷新固定为 `30m`。刷新菜单只展示关闭自动刷新和 `30m`，如果地址栏带有 Grafana 的动态值 `refresh=auto`，监控插件会自动改为 `refresh=30m`。

点击 Dashboard 的 `Refresh` 会同时执行 Grafana 原生的 Prometheus 查询刷新，并通过插件代理调用 Exporter 的 `/collect`，触发当前页面对应 IP 的一次采集。点击后该 IP 的 `Refresh` 按钮置灰 10 秒；冷却按 IP 独立保存，切换到其他 IP 不受影响。

由于 `/collect` 是异步排队接口，插件会继续等待本次采集完成，并等待 Prometheus 的下一次本地抓取；新指标可查询后，插件会自动补一次只刷新查询、不再次采集的 Grafana Refresh。因此“最近一次采集时间”和其他面板会显示本次采集结果，不需要再手动点击第二次。

这 10 秒只是浏览器端的按钮防连点时间，不是 Exporter 后端限流。`/collect` API 不会因此返回 HTTP 429，脚本或其他客户端仍可直接调用。
