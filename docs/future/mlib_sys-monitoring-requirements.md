# mlib_sys 监控接口需求单

## 转发摘要

| 优先级 | 接口 | 用途 | 当前监控端行为 |
|---|---|---|---|
| P0 | `monitor_role_counts/0` | 角色总数、在线角色数 | 接口未提供时明确显示“待接口”，不填0、不用BEAM进程数代替 |
| P0 | `monitor_scheduler_hotspots/2` | Run Queue持续过高时定位责任进程 | 目前只显示节点级Run Queue/调度器倍数；不做不可靠的单进程归因 |
| P1 | `monitor_snapshot/1` | 统一节点、容量、队列和内存快照 | 当前已有外置只读采集，接入后可减少临时RPC表达式并固定数据契约 |
| P2 | `monitor_process_detail/1` | 告警后的单进程补充诊断 | 仅按真实PID查询，不接受外部字符串转Atom |

P0接口完成后，监控端即可补齐玩家人数和调度热点进程名；返回格式、性能边界与验收用例如下，可直接转发给`mlib_sys`维护方。

## 1. 目标

为公司统一Erlang监控平台提供稳定、只读、低开销、可版本化的RPC接口，替代监控端临时拼装Erlang表达式。接口只返回结构化数据，不直接打印控制台文本，不执行GC、kill、suspend等改变节点状态的操作。

## 2. 必须接口

### 2.0 玩家人数

已采用：`mlib_sys:monitor_role_counts/0`

返回：`{ok, #{version => 1, node => node(), total_role_count => non_neg_integer(), online_role_count => non_neg_integer(), collected_at_ms => Integer}} | {error, Reason}`。

- `total_role_count`定义为当前业务口径的累计角色人数，监控总览映射到“注册人数”列。
- `online_role_count`定义为当前在线且通过业务登录态校验的角色人数，不能用BEAM进程数、`registered()`长度或连接数代替。
- 若节点不支持该统计，返回`{error, unsupported}`，监控端显示“待mlib_sys接口”，不得填0。
- 统计必须是只读、O(1)或基于已有缓存，不能每次监控触发全表扫描。

### 2.1 节点快照

建议：`mlib_sys:monitor_snapshot/0`

返回：`{ok, #{version => 1, collected_at_ms => Integer, node => Node, vm => Map, process_risks => Map}} | {error, Reason}`

`vm`必须包含：

- `process_count`、`process_limit`
- `memory_total_bytes`
- `run_queue`
- `schedulers_online`
- `atom_count`、`atom_limit`
- `port_count`、`port_limit`

`process_risks`必须包含：

- `max_memory_process`
- `max_message_queue_process`
- `processes_over_memory_threshold`
- `processes_over_message_queue_threshold`

进程对象统一字段：

```erlang
#{
  pid => pid(),
  registered_name => atom() | undefined,
  initial_call => {module(), atom(), arity()} | undefined,
  current_function => {module(), atom(), arity()} | undefined,
  memory_bytes => non_neg_integer(),
  message_queue_len => non_neg_integer(),
  reductions => non_neg_integer()
}
```

阈值建议由调用参数传入，或增加 `monitor_snapshot/1`：

```erlang
mlib_sys:monitor_snapshot(#{
  memory_threshold_bytes => 209715200,
  message_queue_threshold => 100
}).
```

不得根据外部字符串创建Atom。

### 2.2 调度热点进程

建议：`mlib_sys:monitor_scheduler_hotspots/2`

```erlang
mlib_sys:monitor_scheduler_hotspots(WindowMs, Limit).
```

- `WindowMs`：采样窗口，建议允许1000至10000毫秒，监控默认10000毫秒。
- `Limit`：返回Top N，建议允许1至20，监控默认10。
- 对同一批存活进程采集两次`reductions`，按增量倒序返回。
- 返回进程PID、注册名、初始函数、当前函数、`reductions_delta`、`reductions_per_second`、内存和消息队列长度。
- `registered_name`为空时仍必须返回PID、初始函数和当前函数；监控端将按“注册名 > 初始函数 > PID”的顺序生成可读进程名。
- 同时返回窗口开始/结束时的`run_queue`、`schedulers_online`，用于证明节点在该窗口内确实存在调度拥堵。

建议返回：

```erlang
{ok, #{
  version => 1,
  window_ms => 10000,
  run_queue_start => 80,
  run_queue_end => 96,
  schedulers_online => 16,
  processes => [ProcessHotspot]
}}.
```

该接口用于回答“Run Queue高时究竟哪些Erlang进程占用调度时间”，单次`erlang:statistics(run_queue)`无法提供责任进程名。

## 3. 可选接口

建议：`mlib_sys:monitor_process_detail/1`，只接受真实`pid()`，返回单个进程的注册名、初始函数、当前函数、内存、消息队列长度、reductions和status。进程已退出时返回`{error, not_found}`。

## 4. 性能与安全约束

- 全部接口只读，不触发远端全量消息内容复制，不读取进程dictionary和大状态。
- 节点快照只允许一次`processes()`遍历；同一进程的多个轻量字段合并到一次`process_info/2`。
- 调度热点接口最多保留Top N结果，避免返回全部进程明细。
- 单次返回建议小于64KiB；超过限制返回截断标记和实际扫描数量。
- 接口应有明确超时和异常返回，不使用`catch`吞掉错误。
- 不返回Cookie、路径密钥、玩家数据、消息正文或进程状态正文。
- 返回Map字段保持向后兼容，通过`version`演进。

## 5. 验收用例

1. 正常节点能在5秒内返回节点快照，数值与`erlang:system_info/1`一致。
2. 构造消息积压进程后，返回正确PID、注册名、当前函数和队列长度。
3. 构造高内存进程后，返回正确PID及内存字节数。
4. 构造CPU密集进程后，10秒窗口Top reductions能定位该PID。
5. 进程在两次采样之间退出时接口仍正常返回，不导致整体失败。
6. 10万进程规模下验证耗时、内存增量和返回体上限。
