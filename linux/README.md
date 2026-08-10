# Linux 原生运行环境

此目录包含 CentOS/Linux 原生部署基础监控所需的安装、校验和 systemd 文件，不依赖 Docker。

包含：

- Prometheus 3.5.0
- Alertmanager 0.28.1
- Grafana OSS 12.1.0
- 当前项目的 Linux AMD64 Erlang Exporter
- 当前项目的 Linux AMD64 运维 Ops Agent

不包含 HolmesGPT、Holmes Gateway、Go、Node.js、Java、Python 3.11 或生产 Secret。

项目目录固定为 `/data/node_monitor`。配置、Secret、Grafana provisioning 和运行数据均保留在该目录中：

```text
/data/node_monitor/config/servers.native.yml
/data/node_monitor/prometheus/prometheus.local.yml
/data/node_monitor/alertmanager/alertmanager.local.yml
/data/node_monitor/grafana/grafana.local.ini
/data/node_monitor/grafana/provisioning-local/
/data/node_monitor/ops-agent/config.native.yml
/data/node_monitor/ops-agent/skills/
/data/node_monitor/secrets/
/data/node_monitor/data/
```

`/opt/erlang-monitor` 只存放二进制运行时。`/etc/systemd/system` 只存放引用上述项目路径的 systemd 单元，不再使用 `/etc/erlang-monitor` 或 `/data/erlang-monitor`。

`servers.native.yml`、Prometheus、Alertmanager、`grafana.local.ini` 和 Grafana provisioning 与 Windows 原生运行时共用，并跟随项目版本从 Windows 源码端发布，不在 Linux 上长期分叉维护。旧的 `servers.local.yml` 只作为机器本地兼容文件保留，更新脚本会备份但服务不再读取它。配置中的 SSH 文件统一使用 `secrets/ssh/...` 项目相对路径；Linux 只单独准备 `/data/node_monitor/secrets` 内的实际 Secret。Grafana 的 Linux 数据路径、HTTPS 域名和管理员密码由 systemd 与 `run-grafana.sh` 在启动层注入，不在公共 INI 中复制一套逻辑配置。

Ops Agent 原生服务从 `secrets/glm_api_key` 读取模型密钥，并与现有 Holmes Gateway 共用 `secrets/holmes_tool_api_token` 作为 Grafana 代理令牌；两个文件必须属于 `root:erlang-monitor`、仅允许所属组读取。Secret 内容不进入 YAML、systemd 单元或 SVN。Grafana 正常重启时也由 `run-grafana.sh` 从同一令牌文件注入 Ops Agent 的 `secureJsonData`。

运行时安装脚本只创建独立账号、项目内数据目录并安装/校验二进制，不启动服务，不修改 Nginx、防火墙或现有业务进程：

```bash
cd /data/node_monitor/linux
sudo ./install-runtime.sh
```

把本机参考配置和 Secret 安全复制到项目目录后，先校验：

```bash
cd /data/node_monitor
sudo -u erlang-monitor ./linux/validate-config.sh
```

再安装 systemd 单元：

```bash
cd /data/node_monitor
sudo bash ./linux/install-services.sh
```

该脚本覆盖既有单元前会创建带时间戳的备份，只执行 `daemon-reload` 和单元校验，不会 enable 或 start 服务。服务启动必须在配置、时钟同步和端口复核通过后单独执行。

首次配置验收并启动后，日常更新和重启统一执行：

```bash
sudo bash /data/node_monitor/linux/update-and-restart.sh --revision REVISION
```

外服加密私钥采用与 Windows 相同的 SSH Agent 模式。第一次运行或系统重启后，脚本会在
`/run/erlang-monitor-ssh-agent/agent.sock` 创建专用 Agent，并交互执行 `ssh-add`；因此必须从
有 TTY 的终端运行并输入私钥口令。口令只用于解锁，不写入项目或磁盘。后续仅重启监控服务时，
只要 Agent 仍存活就不再提示。整机重启会清空 `/run` 和 Agent 内存，必须重新运行本脚本。
私钥和对应公钥必须分别放在 `secrets/ssh/ssjj_identity` 与
`secrets/ssh/ssjj_identity.pub`；脚本通过公钥文件核对 Agent 中加载的身份，并把 Windows
私钥的 CRLF 行尾转换为旧版 Linux OpenSSH 可读取的临时 LF 副本。

更新重启脚本会：

1. 从 `/data/save/qt01_server_rebuild` 读取 SVN 密码，只对 `/data/node_monitor` 执行 `svn update`，不缓存认证信息且绝不 commit。
2. 更新前把 `servers.native.yml`、兼容用的旧 `servers.local.yml`、Prometheus、Alertmanager 和 Grafana 本地配置备份到 `data/deploy-backups/<时间戳>/`，不重复复制 Secret。
3. 更新后检查 SVN 冲突；存在冲突或配置校验失败时停止，不重启现有服务。
4. 使用项目内、已校验和的 Linux AMD64 Exporter 与 Ops Agent 验证配置，再安装运行时产物到 `/opt/erlang-monitor/bin/`。
5. 准备专用 SSH Agent，必要时交互执行 `ssh-add`。
6. 重新安装并校验项目内 systemd 单元，设置五项监控服务开机启动。
7. 先按 Grafana、Ops Agent、Prometheus、Alertmanager、Exporter 顺序停止全部监控服务，再按 Exporter、Alertmanager、Prometheus、Ops Agent、Grafana 顺序启动，并检查 `127.0.0.1:20903`、`20902`、`20901`、`20906`、`20900`。

基础监控脚本不会重启 Nginx，也不管理可选的 `20904/20905` Holmes 服务。如果 `chronyd` 未运行会明确警告，但不会自动修改时间同步服务。Holmes 使用下文独立的精确 revision 部署入口，避免因分析组件发布而重启基础监控与 Ops Agent 服务。

计划端口：

- Grafana: `127.0.0.1:20900`
- Prometheus: `127.0.0.1:20901`
- Alertmanager: `127.0.0.1:20902`
- Erlang Exporter: `127.0.0.1:20903`
- Holmes Gateway: `127.0.0.1:20904`（可选原生 systemd 服务）
- HolmesGPT: `127.0.0.1:20905`（可选原生 systemd 服务）
- Ops Agent: `127.0.0.1:20906`

Holmes 原生服务不依赖 Docker，也不修改 CentOS 7 的系统 Python。`install-holmes-runtime.sh` 从项目内已校验和的便携 Python 3.11.15、固定 HolmesGPT 0.38.1 源码包和 Linux wheel 包创建 `/opt/erlang-monitor/holmesgpt/.venv`。

准备 Git/SVN 忽略的 `holmes/model_list.local.yaml` 与 `secrets/holmes_api_key`、`secrets/holmes_tool_api_token`、`secrets/glm_api_key` 后，使用精确 SVN revision 部署：

```bash
sudo bash /data/node_monitor/linux/update-holmes-and-restart.sh --revision REVISION
```

该脚本先确认原四项服务均为 active，备份本机 Holmes 配置与 Grafana App 设置，只安装并重启 HolmesGPT、Holmes Gateway。Grafana 的 Holmes Tool Token 通过管理 API 写入 `secureJsonData` 并立即验证代理，不重启 Grafana；`run-grafana.sh` 也会在未来正常重启时从 Secret 文件重新注入。脚本最后再次确认原四项服务状态，且不重启 Nginx。

Grafana 在内网通过 HTTPS 域名 `nmonitor-qt01.ftfnc.com` 使用，HTTP 请求会重定向到 HTTPS。对应 vhost 模板在 `linux/nginx/nmonitor-qt01.ftfnc.com.conf`，配置指向本机 `127.0.0.1:20900`，并引用 `/data/conf/nginx/cert/ftfnc.com.crt` 和 `/data/conf/nginx/cert/ftfnc.com.key`。启用前需要让内网 DNS 将该域名解析到 `192.168.100.24`，核对证书 SAN 和私钥匹配后再执行 Nginx `-t` 和 reload。
