---
name: erlang-ops-analysis
description: 只读分析当前 192.168.100.* 内网服务器的 Erlang 节点、BEAM 内存、Exporter、主机资源和监控告警。用户要求排查、诊断、分析原因或查看状态时必须使用；可通过受控 mgectl exprs 调用固定只读表达式，但不执行 GC、重启或其他处理动作。
---

# Erlang 运维只读分析

本 Skill 只采集证据和给出判断，不改变服务器状态。需要处理时输出建议并结束，不在本 Skill 下执行处理动作。

## 范围与权限

- 只分析当前上下文固定的 `current-server`，其配置地址必须精确属于 `192.168.100.*`。其他私网、公网、域名和监控机本地一律拒绝 Shell。
- 不得自行切换服务器、节点、用户、端口、密钥或目录，不接受用户文字提供的新目标。
- 本 Skill 可以使用以下免审批只读命令及其安全组合：

  ```text
  ls  grep  ps  cd  head  tail  df  find
  ```

- 上述命令可用 `|` 和 `&&`。出现重定向、`;`、`||`、命令替换、后台执行、分组/子 Shell 时拒绝；`find` 包含 `-exec`、`-execdir`、`-ok`、`-okdir`、`-delete`、`-fls` 或 `-fprint*` 时拒绝。
- 优先使用有界接口；查询进程或表明细时使用规定阈值，输出被截断时说明证据不完整。

## Erlang 节点只读诊断

当主机和监控数据不足以判断 Erlang 节点内部状态时，可以在已验证的服务目录中使用 `mgectl exprs`。命中后端固定只读表达式白名单时自动执行、跳过 Admin 审批；仍须一次只提出一条命令。任意表达式、GC 和其他处理动作不属于本白名单。

只允许参考 `mlib_sys.erl` 使用以下固定只读表达式。按问题选择必要命令，不要每次全部调用。

```sh
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:i()"
cd -- '<verified-server-dir>' && ./mgectl exprs "erlang:memory()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:get_memory()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:get_memory(209715200)"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:get_heap()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:get_theap()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:get_ets_memory(megabyte)"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:get_total_mnesia_memory()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:get_mnesia_table_memory()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:atom_info()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:monitor_snapshot()"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:monitor_snapshot(#{memory_threshold_bytes=>209715200,message_queue_threshold=>100})"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:monitor_scheduler_hotspots(3000,10)"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:monitor_process_detail(erlang:list_to_pid(\"<0.123.0>\"))"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:info(erlang:list_to_pid(\"<0.123.0>\"),total_heap_size)"
cd -- '<verified-server-dir>' && ./mgectl exprs "mlib_sys:monitor_role_counts()"
```

- `mlib_sys:i/0`：获取进程数/上限、调度器数量、进程已用/已分配内存、节点总内存和 Mnesia 内存。该函数还会显示当前 Erlang 集群的已连接节点汇总，只作为只读背景，不得据此切换 Shell 目标。
- `erlang:memory/0`：获取当前节点 total、processes、processes_used、system、atom、binary、code 和 ETS 等内存分类；ETS 总量以这里的 `ets` 字段为准。
- `get_memory/0,1`：列出超过内存阈值的进程；自定义阈值按字节计算且不得小于 1 MiB。
- `get_heap/0,1`、`get_theap/0,1`：列出 Heap/Total Heap 超阈值进程；自定义阈值不得小于 100000 words。
- `get_ets_memory/0,1`：只允许无参数或固定 `megabyte` 单位，用于查看 ETS 总量和各表占用；输出截断时不得据此断言已检查全部表。
- `get_total_mnesia_memory/0`、`get_mnesia_table_memory/0`：获取本节点 Mnesia 总量及各本地表内存；不允许传入任意表名。
- `atom_info/0`：只获取 Atom 数量和上限，不允许枚举全部 Atom。
- `monitor_snapshot/0,1`：获取节点内存、Run Queue、进程数量以及内存/邮箱风险 Top 结果；自定义内存阈值不超过 1 TiB，邮箱阈值不超过 1000000。
- `monitor_scheduler_hotspots/2`：采样 reductions 增量；窗口只允许 1000～10000 ms，结果数量只允许 1～20。
- `monitor_process_detail/1`：只查询前一步返回的本节点 PID，PID 必须是 `<0.ID.SERIAL>` 数字格式。
- `info/2`：只在 `monitor_process_detail/1` 仍不足以解释异常时，对前一步返回的同一本节点 PID 查询一个字段。字段只允许 `memory`、`heap_size`、`total_heap_size`、`stack_size`、`message_queue_len`、`reductions`、`current_function`、`status` 或 `garbage_collection`；每次只查一个。禁止 `all`、`messages`、`dictionary`、`binary`、堆栈、links 和其他字段。
- `monitor_role_counts/0`：只在判断玩家在线影响时查询在线数和角色总数。

不得改写为任意 Erlang 表达式，也不得调用未列出的 `mlib_sys` 或 Erlang 函数。若监控接口返回 `undef`，说明目标节点未部署对应接口，记录能力缺口并回到节点基线、监控数据和普通只读 Shell。

## 分析流程

1. 先调用 `list_skills`，再加载本 Skill。
2. 固定当前服务器、节点、告警标签和时间范围；确认告警是否仍然有效。
3. 节点可响应时，先逐条获取 `mlib_sys:i()` 和 `erlang:memory()`，建立进程数量、进程内存、ETS、Mnesia 和节点总内存基线；只有对应指标异常时，才继续调用 `get_memory/get_heap/get_theap/get_ets_memory` 或 Mnesia 明细。
4. 一次只提出一条最小只读检查。模型返回多条工具调用时按顺序逐条执行，不并发、不合并。
5. 按问题类型采集有界证据：
   - 节点不可用：区分进程、监听、节点存活、Erlang RPC、Exporter 样本和监控状态；节点可响应时可用 `monitor_snapshot/0` 复核。
   - BEAM 内存高：先用 `erlang:memory/0` 判断主要增长属于 processes、ETS、Mnesia、binary、atom、code 还是其他 system 内存，再进入对应分支。不要把 VM 总内存直接等同于进程内存。
   - processes 高：用 `get_memory/get_heap/get_theap` 定位 PID，再调用 `monitor_process_detail/1`。仍无法区分时，按现象逐个查询 `heap_size`、`total_heap_size`、`stack_size`、`message_queue_len` 或 `garbage_collection`：大 `total_heap_size` 指向进程堆保留，大邮箱指向消息积压，大 stack 指向深调用；结合 `current_function` 和两次 `reductions` 观测判断活动热点。不得仅凭单次 reductions 断言 CPU 根因。若证据确认属于可回收的 processes/heap 保留且节点仍可响应，建议加载 `erlang-node-gc`；不得在本 Skill 中执行 GC。
   - ETS/Mnesia 高：分别用 `get_ets_memory(megabyte)` 或 `get_mnesia_table_memory()` 定位具体表。输出截断时只报告已显示的表，不得声称已检查全部表。
   - binary 高：现有安全接口只能确认节点级 binary 总量，禁止用 `info(PID,binary)` 拉取可能很大的原始引用列表。报告能力缺口，不猜测具体拥有者。
   - atom/code/其他 system 高：用 `atom_info/0` 或现有监控指标复核趋势；没有安全的细分接口时明确写“不确定”，不要开放任意 Erlang 表达式。
   - CPU/负载高：结合逻辑 CPU 数、Load、Run Queue 和持续时间判断；需要定位调度热点时调用 `monitor_scheduler_hotspots/2`。
   - Exporter 异常：分开判断 Exporter 进程、采集状态、最新样本和具体缺失节点，不能把节点缺失误判为平台整体故障。
   - 磁盘告警：只分析容量、挂载点和占用证据；需要清理时建议加载 `internal-disk-space-recovery`。
6. 证据不足时输出“不确定”和下一条最小只读检查，不执行猜测性处理。

## 输出要求

最终只输出：

1. 事实：服务器、节点、时间范围和观测值。
2. 判断：已确认原因、可能原因及证据强弱。
3. 风险：业务影响和继续恶化条件。
4. 建议：无需处理、继续观察、加载磁盘 Skill、加载 `erlang-node-gc` 执行单节点 GC，或在用户明确要求服务启停时加载 `erlang-service-restart`。
5. 未解决项：缺失证据和人工检查项。

不得写“已优化”“已恢复”或“已重启”，因为本 Skill 不执行处理动作。单任务和未显式覆盖的命令使用系统配置的 `30m` 上限。
