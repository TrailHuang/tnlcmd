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
- ✅ **命令树**: 支持复杂的多级命令结构
- ✅ **连接数限制**: `MaxConnections` 限制最大并发连接数，超限礼貌拒绝
- ✅ **空闲回收**: `IdleTimeout` 自动回收空闲超时的会话
- ✅ **密码认证**: 可选密码认证，支持错误次数上限与不回显输入
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

以上配置项均可通过 `SetConfig` 动态设置（如 `SetConfig("auditlog", "true")`）。

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

- 连接池管理空闲会话
- 定期清理过期连接
- 缓冲区优化减少系统调用
- 会话级读缓冲复用，避免每轮读取分配
- 内存高效的历史命令存储

## 测试

```bash
go vet ./...           # 静态检查
go test -race ./...    # 全量并发安全测试（提交前必跑）
go build ./...         # 编译检查
```

## 跨平台支持

程序支持在 Linux、macOS 和 Windows 系统上运行，支持 x86 和 ARM 架构。

## 许可证

MIT License