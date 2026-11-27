# Telnet 命令行程序

一个功能完整的 Telnet 服务器程序，支持命令 tab 补全、历史命令管理和上下翻页功能。

## 功能特性

- ✅ **Telnet 服务器**: 支持多客户端并发连接
- ✅ **Tab 补全**: 支持命令自动补全，显示命令名称和描述
- ✅ **历史命令**: 支持命令历史记录和浏览
- ✅ **上下翻页**: 支持使用上下箭头键浏览历史命令
- ✅ **命令行编辑**: 支持左右箭头移动光标、行中插入/删除、Home/End 跳转
- ✅ **优雅关闭**: 支持信号处理和优雅关闭
- ✅ **并发安全**: 支持多客户端并发访问（`go test -race` 全量验证）
- ✅ **多行描述**: 支持为命令路径的每个层级提供详细描述
- ✅ **参数验证**: 支持多种参数类型验证（枚举、范围、字符串、可选参数）
- ✅ **占位符参数**: 支持 zebra 风格数据类型校验（IPV4/IPV4_PREFIX/IPV6/IPV6_PREFIX/MAC/MAC_PREFIX）
- ✅ **命令树**: 支持复杂的多级命令结构
- ✅ **连接数限制**: `MaxConnections` 限制最大并发连接数，超限礼貌拒绝
- ✅ **空闲回收**: `IdleTimeout` 自动回收空闲超时的会话
- ✅ **密码认证**: 可选密码认证，支持错误次数上限与不回显输入
- ✅ **输入边界**: `MaxLineLength` 限制单行输入长度，`ReadTimeout` 支持认证后读超时
- ✅ **调试日志**: `TNLCMD_DEBUG=trace|debug` 输出结构化调试日志，默认关闭零开销
- ✅ **命令审计日志**: 记录每条命令的远端地址、命令原文、耗时与结果
- ✅ **审计日志落盘**: 按日期滚动写文件，自动清理过期日志
- ✅ **零依赖**: 仅使用 Go 标准库（go 1.24+）

## 编译和运行

### 编译程序

```bash
go build ./cmd/zebra_example
go build ./examples/example
```

### 连接测试

使用 telnet 客户端连接：

```bash
telnet localhost 2323
```

## 内置命令

- `exit` - 关闭当前连接
- `quit` - 退出当前模式（根模式下等效关闭连接）

> `time`、`history` 等业务命令由使用方自行注册；历史命令可通过 `↑` / `↓` 方向键浏览。

## 键盘快捷键

- `Tab` - 命令补全
- `↑` / `↓` - 浏览历史命令
- `←` / `→` - 移动光标
- `Backspace` / `Delete` - 删除字符
- `Ctrl+A` / `Ctrl+E` - 跳转行首/行尾
- `Ctrl+C` / `Ctrl+D` - 退出会话
- `?` - 显示帮助信息

## 技术实现

### 核心组件

1. **TelnetServer**: 处理网络连接和会话管理
2. **Session**: 管理单个客户端会话，处理命令输入输出
3. **CommandHistory**: 历史命令存储和检索
4. **CommandCompleter**: 命令补全功能
5. **auditlog**: 按日期滚动的审计日志写入器

### 并发模型

- 使用 goroutine 处理每个客户端连接
- 使用 sync.RWMutex 保证并发安全
- 支持优雅关闭和资源清理

### 终端控制

- 支持 Telnet IAC 协商与 ANSI 转义序列解析
- 支持 ANSI 转义序列进行光标控制
- 实现完整的命令行编辑功能

## 安全能力配置

```go
config := tnlcmd.DefaultConfig()

// 连接数限制与空闲回收
config.MaxConnections = 10          // 0 = 不限制
config.IdleTimeout = 5 * time.Minute // 0 = 不回收

// 可选密码认证（默认关闭）
config.Password = "secret"
config.MaxAuthAttempts = 3 // 0 使用内置默认值 3

// 命令审计日志
config.AuditLog = true                  // 开启审计（默认输出到 stderr）
config.AuditLogFile = "/var/log/tnlcmd/audit.log" // 落盘（需在 Start 前设置）
```

### 输入边界与读超时

```go
config.MaxLineLength = 4096           // 单行输入与密码的字节上限（0 = 内置默认 4096）
config.ReadTimeout = 30 * time.Second // 认证后单次读取的超时（0 = 不设置）
```

超长行在回车时被拒绝（`% Line too long`）并重置编辑缓冲，会话仍可继续使用。

## 动态配置（SetConfig）

所有配置键：`prompt`、`welcome`、`maxhistory`、`port`、`pagesize`、`maxconnections`、`idletimeout`、`password`、`maxauthattempts`、`maxlinelength`、`readtimeout`、`auditlog`、`auditlogfile`。

运行期语义：

- `idletimeout` 是唯一热更新项，运行期修改会立即重建空闲回收周期；
- `port`、`auditlogfile`、`auditlog` 仅在 Start 前生效，运行中修改会返回错误；
- 其余键的修改只作用于模板配置，在下次 Start 时生效（活动会话持有启动时的配置快照，互不干扰）。

### 审计日志落盘规则

设置 `AuditLogFile` 后，审计日志写入文件（目录不存在时自动创建）：

- 当天日志使用配置路径原样：`/var/log/tnlcmd/audit.log`
- 历史日志自动重命名：`/var/log/tnlcmd/audit_20261125.log`
- 超过 30 天的历史日志由后台 goroutine 每小时自动清理
- 服务 `Stop` 时优雅关闭：等待会话退出、日志落盘、清理 goroutine 结束

审计记录包含远端地址、命令原文、执行耗时与结果：

```
time=2026-11-26T17:52:52.939+08:00 level=INFO msg=audit remote=127.0.0.1:54321 command="show version" duration_ms=0 result=ok
```

密码认证失败事件同样纳入审计（`event=auth_failed` / `event=auth_exhausted`）。

## 新增功能

### 多行描述支持

现在可以为复杂的多级命令路径提供详细的层级描述：

```go
// 注册命令时提供多行描述
cmdline.RegisterCommand("show running-config", "Show running system information", handler,
    "show configuration\ndisplay running config")
```

**效果**：
- `show` 节点的描述："show configuration"
- `running-config` 节点的描述："display running config"
- 叶子节点保留原有描述："Show running system information"

### 改进的命令补全显示

**特点**：
- 命令名称固定32宽度左对齐
- 清晰的描述信息
- 专业的显示格式

### 参数类型支持

支持多种参数类型验证：
- **枚举参数**：如 `(on|off)`
- **范围参数**：如 `<1-10>`
- **字符串参数**：如 `STRING`
- **可选参数**：如 `[OPTIONAL]`
- **占位符参数**：网络设备风格的数据类型（zebra 写法），见下表

#### 占位符参数

注册命令时使用占位符声明参数，命令执行前按类型校验参数值格式，handler 的 `args` 中拿到用户输入的原始字符串（对应 zebra 的 `argv[i]->arg` 语义）。所有占位符均为必填参数。

| 占位符写法 | 类型名称 | 匹配示例 | 说明 |
|---|---|---|---|
| `A.B.C.D` | IPV4 | `192.168.1.1` | 点分十进制 IPv4 地址 |
| `A.B.C.D/M` | IPV4_PREFIX | `10.0.0.0/8` | 含掩码长度的 IPv4 前缀 |
| `X:X::X:X` | IPV6 | `2001:db8::1` | 标准 IPv6 文本 |
| `X:X::X:X/M` | IPV6_PREFIX | `2001:db8::/32` | 含前缀长度的 IPv6 前缀 |
| `M:A:C` | MAC | `aa:bb:cc:dd:ee:ff` | 六段冒号分隔十六进制 |
| `M:A:C/M` | MAC_PREFIX | `aa:bb:cc:00:00:00/24` | 带掩码长度的 MAC 前缀 |

```go
cmdline.RegisterCommand("ip route A.B.C.D/M X:X::X:X", "Add IPv4 route via IPv6 nexthop",
    func(args []string) string {
        // args[0] = "10.0.0.0/8"，args[1] = "2001:db8::1"（已通过格式校验的原始字符串）
        return "ok"
    })
```

非法输入会被拒绝并给出格式提示，例如：

```text
Error: Invalid parameter value for command 'neighbor'
Parameter 1: 无效的 MAC 地址: 'aa:bb:cc'（期望形如 aa:bb:cc:dd:ee:ff）
```

校验细节：IPv4/IPv6 及前缀基于 `net.ParseIP`/`net.ParseCIDR` 并区分地址族；MAC 要求每段严格两位十六进制（`aabb.ccdd.eeff` 点分与 `a:b:c` 缩写均不接受）；MAC 前缀掩码为十进制 0-48。空输入时 Tab 补全会提示占位符格式（如 `A.B.C.D/M`）。

### 参数统计逻辑优化

修复了参数统计逻辑，现在正确地从当前节点向根节点回溯统计参数数量。

### 上下文感知命令

支持 `ContextHandler` 签名的命令：handler 可通过 `ctx` 感知连接关闭或服务停止，提前取消长时间运行的任务：

```go
cmdline.RegisterContextCommand("run task", "Run a cancellable task", func(ctx context.Context, args []string) string {
    // select ctx.Done() 提前退出
})
```

## 性能优化

- 会话级读缓冲与行编辑缓冲复用，避免逐行/逐键分配
- 分页与命令结果输出按页批量 flush，减少系统调用
- 空闲回收 goroutine 常驻并按需扫描，阈值可运行期调整
- 历史命令使用固定容量环形存储，写入 O(1)
- 命令、密码、分页三条读取路径共用同一 Telnet/ANSI 输入过滤器

## 测试

```bash
make vet        # 静态检查（含集成测试代码）
make test-fast  # 单元测试（快速验证）
make test       # 全量并发安全测试，含集成测试（提交前必跑）
make build      # 编译检查
```

等价的原始命令：`go test -race -count=1 -tags integration ./...`（集成测试带 `integration` 构建标签）。

## 跨平台支持

程序支持在 Linux、macOS 和 Windows 系统上运行，支持 x86 和 ARM 架构。

## 许可证

MIT License