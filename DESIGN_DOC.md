# TNLCMD 项目设计文档

> 本文档基于当前代码实现编写（2026-11-26 校对，Go 1.24.9，`go build` / `go vet` / `go test -race ./...` 全部通过）。

## 1. 项目概述

**TNLCMD** 是一个纯 Go 语言实现的 Telnet 命令行服务器库，模拟 Cisco IOS / Zebra 风格的网络设备 CLI 交互体验。支持多级命令树、Tab 补全、`?` 分级帮助、命令历史、参数验证、视图层级切换和输出分页。

- **语言**: Go 1.24.9（`go.mod` 声明）
- **依赖**: 零外部依赖（仅 Go 标准库）
- **许可证**: MIT
- **代码量**: 源码 10 个包约 3.1k 行（不含示例与测试）；测试约 3.6k 行（7 个单元测试包 + 1 个集成测试文件）

---

## 2. 系统架构

### 2.1 分层架构图

```
┌─────────────────────────────────────────────────────┐
│                   Public API                        │
│              tnlcmd.go (外观层)                      │
│  NewCmdLine / RegisterCommand / Start / Stop /      │
│  SetConfig / DefaultConfig                          │
├─────────────────────────────────────────────────────┤
│                  Orchestration                      │
│          internal/cmdline/cmdline.go                │
│     配置管理 · 模式注册 · 服务生命周期                │
├──────────┬──────────┬──────────┬────────────────────┤
│  Server  │ Session  │  Mode    │  CommandTree       │
│  (TCP)   │ (终端IO) │ (视图)   │  (Trie解析)        │
├──────────┼──────────┼──────────┼────────────────────┤
│ History  │Completer │ debuglog │                    │
│ (历史)   │ (补全)   │ (调试日志)│                    │
├──────────┴──────────┴──────────┴────────────────────┤
│               pkg/types/types.go                    │
│  公共类型: Config, CommandHandler, Sentinel 常量     │
└─────────────────────────────────────────────────────┘
```

### 2.2 核心组件职责

| 组件 | 文件 | 行数 | 职责 |
|------|------|------|------|
| **Public API** | `tnlcmd.go` | 67 | 对外暴露的类型别名和方法代理 |
| **Orchestrator** | `internal/cmdline/cmdline.go` | 242 | 组装各子系统、动态配置（SetConfig）、服务生命周期 |
| **TCP Server** | `internal/server/server.go` | 206 | TCP 监听、连接接受、会话创建、每连接独立上下文克隆、优雅关闭（WaitGroup） |
| **Session** | `internal/session/session.go` | 909 | 逐字节终端 I/O、输入状态机（Telnet/ANSI/UTF-8）、命令分发、参数验证、分页输出 |
| **Command Tree** | `internal/commandtree/command_tree.go` | 897 | Trie 命令存储、参数解析、命令匹配（含唯一前缀匹配）、参数验证 |
| **Completer** | `internal/completer/completer.go` | 277 | Tab 补全、`?` 帮助、参数建议、最长公共前缀压缩 |
| **History** | `internal/history/history.go` | 123 | 容量受限的历史记录（环形缓冲实现）、上下键导航 |
| **Mode** | `internal/mode/mode.go` | 157 | 视图层级、模式切换、`CommandContext` 及其克隆 |
| **debuglog** | `internal/debuglog/debuglog.go` | 105 | 环境变量控制的调试日志（默认关闭，零开销） |
| **Types** | `pkg/types/types.go` | 60 | 公共类型定义、Sentinel 常量 |

### 2.3 数据流

```
Telnet Client ──TCP──▶ Server.Accept()
                          │
                    ┌─────▼──────────┐
                    │    Session     │
                    │ ts.context.    │  ← 每连接独立克隆上下文
                    │   Clone()      │
                    └─────┬──────────┘
                          │ readLine() — 状态机逐字节解析
                          │   ├─ 0xFF   → Telnet IAC（2/3 字节变长 + SB 子协商）
                          │   ├─ 0x1B   → ANSI ESC [ A/B 方向键（历史导航）
                          │   ├─ 0xEF…  → 全角问号 '？'（UTF-8 三字节）
                          │   ├─ 0x09   → Tab   → Completer
                          │   ├─ 0x3F   → ?     → Completer（分级帮助）
                          │   ├─ 0x7F   → Backspace（清行重绘）
                          │   └─ 0x0D   → Enter
                          ▼
                    processCommand()
                          │ ① 当前模式 CommandTree.FindCommand(parts)
                          │ ② 单 token 失败 → 回退根模式树（跨模式切换）
                          ▼
                    validateCommandParameters()  ← 数量 + 值验证
                          │
                    ┌─────▼──────┐
                    │  Handler   │
                    │  (args)    │
                    └─────┬──────┘
                          │ 返回 SentinelExit        → "Goodbye!" + io.EOF
                          │ 返回 SentinelExitToRoot  → ChangeMode(root)
                          │ 其他非空返回值           → writeResult()（按 PageSize 分页）
                          ▼
                    发送响应 → updateCommands() → 循环
```

---

## 3. 核心设计

### 3.1 命令树 (Trie)

命令字符串被 `parseCommandString` 解析为 Trie 节点链，支持以下参数语法：

| 语法 | 节点类型 | 示例 | 说明 |
|------|---------|------|------|
| `plain` | `NodeTypeCommand` | `show`, `running-config` | 普通命令 |
| `(a\|b\|<1-10>)` | `NodeTypeEnum` | `(on\|off)` | 枚举值存 `EnumValues`，内联范围存 `EnumRanges` |
| `<1-10>` | `NodeTypeNum` | `<1-100>` | 范围存 `RangeMin`/`RangeMax` |
| `STRING` | `NodeTypeString` | `HOSTNAME` | 全大写字母识别为字符串参数 |
| `[OPT]` | `NodeTypeOptional` | `[STRRING]` | 可选参数（`IsRequired=false`） |
| — | `NodeTypeModeSwitch` | `configure` | 视图切换节点（`AddModeCommand` 创建，带 `ModeName`） |
| — | `NodeTypeExit` | — | 已定义于 `pkg/types`，当前代码未使用 |

**匹配策略**（`findCommand`）：
1. 精确匹配 Command / ModeSwitch 节点；
2. 失败后尝试**唯一前缀匹配**（如 `shu` → `shutdown`，多候选则放弃）；
3. 再失败则按顺序尝试参数节点（Optional 递归、Enum/Num/String 按值验证）；
4. 节点带 Handler 但仍有剩余参数时，剩余参数随查找结果一并返回，由会话层数量校验报 `Too many arguments`。
5. 单 token 输入优先查 `ModeCommands`（含唯一前缀匹配）以支持视图切换。

**元数据来源现状**：参数验证、错误信息与补全已统一基于节点结构体字段（`RangeMin/RangeMax`、`EnumValues`、`EnumRanges`），不再从 `Name` 正则提取（2026-11-26 P1 第 4 项修复，详见 §6.2 M1）。

### 3.2 视图模式系统

```
root (privileged EXEC)
├── configure (global config)
│   └── (命令: router, hostname, banner, ...)
├── interface (interface config)
│   └── (命令: ip, description, shutdown, no, ...)
```

- 每个 `CommandMode` 拥有**独立的 CommandTree**（`mode.NewCommandMode` 中创建）
- `CommandContext` 维护当前模式和路径（`CurrentMode`, `Path`；命令树经 `CurrentMode.CommandTree` 访问）
- `ChangeMode()` 重建模式路径链（从当前模式向上遍历至根）
- 视图切换命令作为 `NodeTypeModeSwitch` 存储于 `CommandTree.ModeCommands`（**实例级字段**，非全局）
- `processCommand` 双通道切换：优先走当前模式树的 ModeSwitch 节点；单 token 找不到时回退根模式树；无 Handler 的中间节点若命中子模式名也执行切换
- `CommandContext.Clone()` 为每个会话创建独立副本：`Path` 深拷贝，`CurrentMode` 保持共享指针（注释声明"Start 后命令树只读、模式切换仅重新赋值字段"的前提，见 §6.1 S2）

### 3.3 终端会话处理

- **缓冲复用**: `Session` 持有单个 `*bufio.Reader` / `*bufio.Writer`、会话级 `readBuf` 与 `editor`（行编辑缓冲），全程复用
- **输入过滤**（`internal/session/inputfilter.go`）: 命令、密码、分页三条读取路径共用 `telnetInput` 过滤器，以事件（`evChar/evEnter/evUp/...`）输出；跨 Read 批次保持解析状态，正确处理变长序列
  - 2 字节 IAC（NOP/GA/AYT/EC/EL/IAC-IAC 等，0xF1-0xF9 及 0xFF）；`stIACOpt`: 3 字节协商（WILL/WONT/DO/DONT）
  - `stSB`/`stSBIAC`: 子协商数据流，跳过至 IAC SE
  - `stESC`/`stESCBracket`/`stESCTilde`: 方向键、Home/End、Delete 等光标事件
  - `stUTF8Q1`/`stUTF8Q2`: 全角问号 `？`(EF BC 9F) 等价半角 `?` 触发帮助
  - 未闭合序列超过 64 字节回退正常态（工单 08/11 的长度边界）
- **Telnet 协商**: 连接建立时发送 `IAC WILL ECHO`, `IAC DO/WILL SUPPRESS_GO_AHEAD`（走统一写入路径）
- **行编辑**: 光标移动/删除/插入重绘；超长行在回车时提示 `% Line too long` 并重置
- **写入错误短路**: `writeErr` 记录最近一次写/flush 错误，置位后所有写入直接返回，主循环立即退出会话，避免向死连接持续写入
- **分页输出**（`writeResult`）: `PageSize > 0` 时按页聚合写入，每页末尾单独写 `--More-- (NN%)` 并等待按键（Space/Enter/方向键 → 翻页；`q` → 中止；Ctrl+C → 关闭连接）；`PageSize <= 0` 一次性输出
- **读超时**: `ReadTimeout > 0` 时认证后每轮读取前刷新 deadline
- **换行规范化**: `normalizeLineEndings` 统一将 `\n`/`\r\n` 转为 `\r\n`

### 3.4 命令历史

- 线程安全（`sync.RWMutex`），固定容量环形数组，`Add` 为 O(1)
- 去重：连续相同命令不重复添加
- `MaxHistory <= 0` 时禁用历史（Add 即清空）
- 会话层上/下方向键经 `Get`/`Len` 按逻辑下标导航

### 3.5 调试日志 (debuglog)

- 默认关闭，`Enabled()` 供热路径短路，零开销
- `TNLCMD_DEBUG` = off/trace/debug（默认 off）；`info/warn/error` 不是本包级别，视为 off
- `TNLCMD_DEBUG_FILE` 指定输出文件（追加模式，0600），为空输出到 stderr
- 生产日志走 `log/slog`；审计输出走注入的专用 logger，不改写全局默认 logger

---

## 4. 接口与类型

### 4.1 公共 API（tnlcmd.go）

```go
type CommandHandler func(args []string) string
type ContextHandler func(ctx context.Context, args []string) string // 支持 ctx 取消

// Sentinel 返回值与哨兵错误（pkg/types）
const (
    SentinelExit       = "__EXIT__"          // 关闭当前连接
    SentinelExitToRoot = "__EXIT_TO_ROOT__"  // 退出到根模式
)
var (
    ErrIncompleteCommand / ErrUnknownCommand / ErrTooFewArguments /
    ErrTooManyArguments  / ErrInvalidParameterValue        // 均可用 errors.Is 判断
)

type Config struct {
    Prompt, WelcomeMsg string
    Port               int
    MaxHistory         int           // <=0 禁用
    PageSize           int           // <=0 禁用分页
    MaxConnections     int           // 0 = 不限制
    IdleTimeout        time.Duration // 0 = 不回收（唯一热更新键）
    Password           string        // 空 = 关闭认证
    MaxAuthAttempts    int           // 0 = 内置默认 3
    MaxLineLength      int           // <=0 = 内置默认 4096
    ReadTimeout        time.Duration // 0 = 不设置
    AuditLog           bool
    AuditLogFile       string
}

func NewCmdLine(config *Config) *CmdLine                 // config 为 nil 时使用 DefaultConfig()
func DefaultConfig() *Config                             // 单一来源：与 NewCmdLine(nil) 共用
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler, detailedDescription ...string) error
func (c *CmdLine) RegisterContextCommand(name, description string, handler ContextHandler, detailedDescription ...string) error
func (c *CmdLine) RegisterModeCommand(modePath, name, description string, handler CommandHandler, detailedDescription ...string) error
func (c *CmdLine) CreateMode(modePath, description string) error
func (c *CmdLine) Start() error                          // 重复启动返回错误；启动失败回收审计资源
func (c *CmdLine) Stop() error                           // 未运行返回错误；等待所有会话退出
func (c *CmdLine) SetConfig(key, value string) error     // 13 个键；运行期语义见 README
```

### 4.2 内部关键类型

- `CommandNode` — Trie 节点（Name, Type, Children, Parent, Handler, EnumValues, EnumRanges, RangeMin/Max, IsRequired, ModeName）
- `CommandTree` — Trie 根包装器（`Root` + **实例级** `ModeCommands`）
- `CommandContext` — 模式状态（`CurrentMode`, `Path`, `CommandTree`；`Clone()` / `ChangeMode()` / `GetAvailableCommands()` / `GetRootMode()`）
- `CommandCompleter` — 补全引擎，主要入口：
  - `GetNextLevelCompletions` — Tab 补全（唯一匹配 → 补全完整命令；多匹配 → 列表；含最长公共前缀压缩）
  - `GetParameterCompletions` — 参数候选
  - `GetCommandTreeSuggestions` — `?` 分级帮助（空输入 → 一级命令；完整关键字 → 下钻子级 + `<cr>` 提示）
  - `Complete` / `GetCompletions` / `GetCurrentViewCommands` — 目前仅测试引用，生产路径未使用
- `Session` — 会话（`Handle(ctx)` 主循环；`Close()`；`UpdatePrompt()`；`IsStale()` 无调用方）
- `TelnetServer` — 服务器（`NewTelnetServerWithContext` 为生产构造器；`NewTelnetServer` 为兼容旧路径；`Addr()`/`Listener()`/`Stop()`）

### 4.3 生命周期与并发模型

```
NewCmdLine → Register* (mu.Lock 保护) → Start()
  ├─ registerBuiltinCommands (exit/quit)
  ├─ server = NewTelnetServerWithContext(config, context)
  └─ server.Start() → go acceptConnections()
                         └─ go handleConnection(conn)  [wg.Add(1)]
                              ├─ ts.context.Clone()
                              ├─ session.Handle(ts.ctx)   ← 阻塞至断开/Server 停止
                              └─ 清理 + conn.Close()       [wg.Done()]
Stop() → cancel() → listener.Close() → 关闭全部会话 → wg.Wait()
```

- `Server` 使用 `context.WithCancel` + `sync.WaitGroup` 保证优雅关闭（`Stop` 阻塞至所有会话 goroutine 退出）
- 注意：`Handler` 为用户代码且**同步执行**，无 ctx/超时控制；慢 Handler 会延迟 `Stop()` 返回（见 §6.1 M8）

---

## 5. 构建与运行

```bash
# 编译
go build ./cmd/zebra_example    # Zebra 风格示例（默认端口 2323，环境变量 TNLCMD_PORT 可覆盖）
go build ./examples/example     # 简单示例（端口 2324）

# 测试
go test ./...                   # 全部通过（集成测试含并发/安全/回归场景）

# 连接
telnet localhost 2323
```

---

## 6. 代码审查发现（以当前代码为准）

### 6.0 旧版问题修复状态

旧版审查中的以下问题**已修复**：

| 旧编号 | 问题 | 现状 |
|--------|------|------|
| C1 | 全局 `ModeCommands` | ✅ 已移入 `CommandTree` 实例字段 |
| C2 | `RootMode` 类型断言无保护 | ✅ 已彻底移除：`Config.RootMode` 字段删除（M3），根模式经 `CmdLine.rootMode`/`CommandContext` 显式传递，无类型断言 |
| C3 | Session 共享上下文 | ✅ 每连接 `ts.context.Clone()`（但见新 S2） |
| C4 | readLine 重复创建 bufio.Reader | ✅ Session 持有并复用 reader/writer |
| m1 | `fmt.Printf` 调试日志残留 | ✅ 统一为 `slog` + `debuglog` |
| m2 | `SimpleExample()` 死代码 | ✅ 已删除 |
| m4 | `flushWriter` 每次新建 Writer | ✅ 复用单一 Writer |
| m5 | `SetConfig` 签名不一致 | ✅ 统一为 `(key, value string) error` |
| m7 | 零测试覆盖 | ✅ 7 个单测包 + 1690 行集成测试 |
| S1 | `UpdatePrompt` 跨 goroutine 并发写连接 | ✅ 已修复（见下） |
| S2 | Start 后注册命令的并发 map 竞争 | ✅ 已修复（见下） |
| S3 | `readPagingKey` IAC 处理不一致 | ✅ 已修复（见下） |

> **2026-11-26 P0 修复记录**（`go test -race ./...` 全绿）：
> - **S1**：`UpdatePrompt` 不再直接写连接，仅更新 `s.prompt` 字段，由主循环在下一轮 `readLine` 重绘；`lastActive` 写入持锁；`updateCommands` 字段写入持写锁；`processCommand` 改为仅在读取 context/mode 结构的临界区持 `RLock`，写锁操作移到锁外（避免 RWLock 不可重入死锁）。
> - **S2**：`RegisterCommand`/`RegisterModeCommand`/`CreateMode` 签名改为返回 `error`，Start 后调用返回错误；公共 facade `tnlcmd.go` 同步更新；示例程序改为检查错误；集成测试 helper 拆分 `newIntegrationTest`（不启动）+ `startServer()`，确保"先注册、后启动"。
> - **S3**：`readPagingKey` 与主状态机 IAC 规则对齐：2 字节命令（NOP/GA/AYT 等）只跳 1 字节、WILL/WONT/DO/DONT 跳 2 字节、SB 子协商跳至 IAC SE。
> - 新增回归测试：`TestSession_FullWidthQuestionMark_IACSequence`（已有）、`TestSession_Pagination_TwoByteIACBeforeKey`、`TestSession_UpdatePromptConcurrent`、`TestRegisterAfterStartRejected`、`TestSetConfigAfterStart`。

### 6.1 严重问题 (Critical)

> 上一轮的 S1/S2/S3 已全部修复（见 §6.0 修复记录），当前无已确认的 Critical 问题。

### 6.2 设计缺陷 (Major)

| # | 问题 | 位置 | 建议 |
|---|------|------|------|
| M1 | **参数元数据双口径**：`isValidNumberInRange` 优先用 `RangeMin/RangeMax` 结构体字段，而 `GetNumberValidationError`/`GetEnumValidationError`/`GetEnumCompletions` 仍从 `Name` 正则重新提取；枚举节点 `Name` 保留 `(on|off)` 原文而 Optional 节点被 trim，提取口径不一 | `command_tree.go:737-897` | 验证与错误信息统一基于结构体字段 |
| M2 | ✅ **错误契约仍为字符串匹配**（已修复 2026-11-26 P1 第 5 项；2026-11-27 工单 06 扩展）：`ErrIncompleteCommand`/`ErrUnknownCommand`/`ErrTooFewArguments`/`ErrTooManyArguments`/`ErrInvalidParameterValue` 均可 `errors.Is` 判断 | `pkg/types`, `session.go`, `command_tree.go` | — |
| M3 | ✅ **`Config.RootMode` 仍为 `interface{}`**（已修复 2026-11-26，工单 01） | `types.go:46` | 已删除该字段：根模式在 `NewCmdLine` 内部构造，经 `CmdLine.rootMode` → `CommandContext` → `server.context` 显式传递；server 兜底分支（类型断言）删除，context 缺失时拒绝连接 |
| M4 | ✅ **补全器存在未使用入口与重复逻辑**（已修复：2026-11-26 工单 04 合并入口；2026-11-27 工单 07 统一 Tab/`?` 导航为共享 `walkInput`，工单 12 删除 `CommandNode.GetCompletions`/`ValidateCommand` 死入口） | `completer.go` | — |
| M5 | ✅ **模式切换双路径**（已修复）：`GetAvailableCommands` 生成的 `createModeChangeHandler`（闭包直接 `ChangeMode`）与 `processCommand` 的 ModeSwitch 树节点分支重复实现切换语义 | `mode.go`, `session.go` | 已收敛为 `CommandContext.EnterMode` 单一实现（ChangeMode + 标准提示消息），两条路径均委托 |
| M6 | ✅ **`SetConfig` 运行期语义不完整**（已修复 2026-11-27 工单 02/03）：配置快照语义（活动会话不受运行期修改影响）、`idletimeout` 唯一热更新、`port`/`auditlogfile`/`auditlog` 运行期修改明确报错、13 个键全部接入并校验 | `cmdline.go` | — |
| M7 | ✅ **历史非环形缓冲**（已修复）：容量超限时 `history[1:]` 头部裁剪，导致 O(n) 拷贝与底层数组指针漂移 | `history.go` | 已改为固定容量环形数组，覆盖写入 O(1) |
| M8 | ✅ **Handler 无 ctx/超时，Stop 可被慢 Handler 拖住**（已修复 2026-11-26 P1 第 6 项）：新增 `ContextHandler` 与 `types.Adapt`；`server.Stop` 5s 超时兜底，`cmdline.Stop` 锁外等待 | `pkg/types`, `server.go`, `session.go` | — |

### 6.3 一般问题 (Minor)

| # | 问题 | 位置 |
|---|------|------|
| m1 | ✅ **死代码/无调用方 API**（已修复 2026-11-26 P1 第 7 项与 2026-11-27 工单 12）：`Config.SetLogger/Logger`、双命令树与平面命令表、`GetCompletions/ValidateCommand/GetCommandDescription/GetNumberCompletions/GetEnumCompletions/isPlaceholderNode`、`GetAvailableCommands/createModeChangeHandler`、`history.Previous/Next/ResetPosition`、`UpdateAllSessionsPrompt/UpdatePrompt`、`TelnetServer.Listener` 等均已删除 | 多处 |
| m2 | ✅ **`readLine` 热路径每轮分配**（已修复）：读缓冲提升为会话级 `Session.readBuf`，会话创建时分配一次并全程复用 | `session.go` | 已改为复用缓冲，`BenchmarkReadLine` 24 B/op、2 allocs/op |
| m3 | ✅ **`enableTelnetCharacterMode` 绕过 `writeErr`**（已修复 2026-11-27 工单 08）：协商写入改走统一写入路径 | `session.go` | — |
| m4 | ✅ **`NodeTypeExit` 已定义但无实现引用**（已修复 2026-11-26 P1 第 7 项删除） | `types.go` | — |
| m5 | ✅ **`minInt` 辅助函数**（已修复 2026-11-26 P1 第 7 项改用内置 `min`） | `session.go` | — |
| m6 | ✅ **测试盲区**（已修复 2026-11-26/27）：`internal/cmdline`、`internal/debuglog` 均已有单元测试；`pkg/types` 由依赖它的包间接覆盖；集成测试 Name 误传已修正 | `cmdline/`, `debuglog/` | — |
| m7 | ✅ **安全能力缺失（已全部修复）**：连接数上限、密码认证、空闲回收已实现（`Config.MaxConnections` / `Config.IdleTimeout` / `Config.Password` / `Config.MaxAuthAttempts`，均默认关闭）；TLS 经用户确认剔除本批 | `server.go`, `session.go` |
| m8 | ✅ **行编辑能力不全（已修复）**：←/→ 光标移动、Ctrl+A/E 跳行首/行尾、Home/End（ESC[H/F 与 ESC[1~/4~/7~/8~）、Delete（ESC[3~）按光标位置删字均已实现；`readLine` 改用 `lineEditor`（字节缓冲+光标），历史回显/Tab 补全后光标复位行尾 | `session.go` |
| m9 | `append(path, currentArg)` 等切片扩容共享底层数组的写法较隐蔽，当前依赖"newPath 每次 make+copy"保证安全，属脆弱点 | `command_tree.go:417,425` |

---

## 7. 优化建议（按优先级排序）

> **2026-11-27 更新**：本轮修复以 `.scratch/code-review-fixes/issues/` 为跟踪载体（审查报告 `CODE_REVIEW.md`）。
> 工单 01–08、11、12、13 已完成；09（多级视图/带参切换）、10（认证加固/BindAddr）、15（Prometheus 指标）经用户决定废弃，14（文档收敛）为本节同步的最后一张。
> 以下条目为历史记录，当前状态以工单与 `CODE_REVIEW.md` 修改清单为准。

### P0 — 立即修复

> ✅ 已全部完成（2026-11-26）：S1 并发写、S3 分页 IAC、S2 Start 后注册守卫均已修复并通过 `-race` 全量测试。

### P1 — 短期改进

> ✅ 4-7 已全部完成（2026-11-26）并通过 `go vet ./...` 与 `go test -race ./...` 全量验证：
> 4. **统一参数元数据口径**（M1）：`GetNumberValidationError`/`GetEnumValidationError`/`GetEnumCompletions`/`isValidEnumValue` 全部基于 `RangeMin/RangeMax/EnumValues/EnumRanges` 结构体字段，删除 `extractNumberRange` 与 `numberRangeRegexp`；
> 5. **sentinel error**（M2）：新增 `types.ErrIncompleteCommand`/`types.ErrUnknownCommand`（`fmt.Errorf("%w: ...")` 包装），`session.processCommand` 改用 `errors.Is` 判断；
> 6. **Handler ctx 支持 + Stop 超时**（M8）：采用兼容方案——公共 `CommandHandler` 保持旧签名 `func(args []string) string` 不变（外部代码无需迁移）；新增 `types.ContextHandler`（`func(ctx context.Context, args []string) string`）与 `types.Adapt` 适配器，命令树内部统一以 ContextHandler 存储（`AddCommand` 注册时自动包装），`session.processCommand(ctx, line)` 将会话 ctx 透传给 handler；`server.Stop` 以 5s 超时兜底等待会话退出（慢 Handler 无法中断时记 Warn 放弃等待），`cmdline.Stop` 改为锁外等待；新增 `RegisterContextCommand` 公共 API 支持直接注册 ContextHandler（旧签名经 `AddCommand` 自动 Adapt）；
> 7. **清理死代码**（m1/m4/m5）：删除 `NewTelnetServer/NewSession/NewCommandCompleter/NewCommandCompleterWithTree/UpdateCommandTree`、`Complete/GetCompletions/GetCurrentViewCommands` 及仅被其使用的 `applyCommonPrefix/findLongestCommonPrefix/collectCommandCompletions`、`Session.commands`/`TelnetServer.commands`/`TelnetServer.commandTree` 字段、`Session.IsStale`、`Config.SetLogger/Logger`/`logger` 字段、`NodeTypeExit`、`minInt`（改用内置 `min`）。

8. **补测试**（m6）：`cmdline` 编排层已有测试（P0 阶段新增）；补 `debuglog` 开关行为；修复集成测试 Name 误传

### P2 — 中期改进

> 工单拆分见 `.scratch/p2-improvements/issues/`（本地 markdown tracker，共 7 张工单）。

9. **安全增强**（m7）：最大连接数、密码认证、可选 TLS、空闲会话回收（接线 `IsStale`）— ✅ 已全部完成（2026-11-26）：工单 05 实现连接数上限（`Config.MaxConnections`，检查+注册同临界区原子完成）与空闲回收（`Config.IdleTimeout`，`Session.IsStale` 判定后锁外关闭）；工单 06 实现可选密码认证（`Config.Password`，空=关闭）——连接先经 `Session.authenticate`（Cisco 风格提示、`readPasswordLine` 不回显、IAC/ESC 状态机跳过、错误 `Config.MaxAuthAttempts` 次（默认 3）后断开、60s 认证读超时防槽位占用），通过后进入 CLI；TLS 确认剔除本批
10. **行编辑增强**（m8）：左右方向键、Ctrl+A/E、命令别名 — ✅ 光标编辑已完成（2026-11-26，工单 07）：`readLine` 改用 `lineEditor`（字节缓冲 + 光标），支持 ←/→（ESC[C/D）、Ctrl+A/E 跳行首/行尾、Home/End（ESC[H/F 与带参 1~/4~/7~/8~ 变体）、Backspace/Delete（ESC[3~）按光标位置删字、行中插入整行重绘并定位光标（ESC[nD）；历史回显与 Tab 补全后光标复位行尾；新增 7 个集成测试覆盖光标编辑与 IAC 混合输入；命令别名经用户确认剔除本批
11. **历史环形缓冲**（M7）— ✅ 已完成（2026-11-26，工单 01）：`internal/history` 改为固定容量环形数组（`buf`/`start`/`size`），容量满时覆盖最旧记录并前移起点，`Add` 为 O(1)；`Get`/`GetAll` 公共 API 语义保持不变（`Previous`/`Next`/`ResetPosition` 于 2026-11-27 工单 12 作为无生产调用方 API 删除，会话层经 `Get`/`Len` 导航）；新增回绕/去重/零容量用例与 `BenchmarkAddOverflow`（满容量写入 ~23 ns/op）；会话层上/下键历史浏览经集成测试验证与改动前一致
12. **收敛模式切换与补全入口**（M4/M5）— ✅ 已全部完成（2026-11-26）：M5（工单 03）新增 `CommandContext.EnterMode`（ChangeMode + `"Entering %s mode\r\n"` 消息）作为模式切换唯一实现路径，`processCommand` 的 ModeSwitch 分支、无 handler 中间节点回退分支均委托它；M4 残余（工单 04）—— `completer` 提取共用导航与统一展示格式 `formatCandidate`，新增 `TestProductionPathContracts` 守护三条生产入口的输出契约（该导航于 2026-11-27 工单 07 进一步统一为 `walkInput`，`walkExact` 于工单 12 删除）
13. **热路径内存优化**（m2）：复用读缓冲 — ✅ 已完成（2026-11-26，工单 02）：`readLine` 读缓冲提升为会话级字段 `Session.readBuf`（`readBufSize = 1024`），会话创建时分配一次、全程复用；`BenchmarkReadLine` 显示每行 24 B/op、2 allocs/op（不再每轮读取分配 1KB）；IAC/ESC/UTF-8 跨批次状态机用例全部通过

### P3 — 长期演进

> 工单拆分见 `.scratch/p3-long-term/issues/`（本地 markdown tracker）。SSH / WebSocket 经用户确认本批剔除，保持待办。

14. **协议扩展**：SSH（`golang.org/x/crypto/ssh`）、WebSocket CLI — 本批剔除（用户确认 2026-11-26）
15. **可观测性**：Prometheus 指标（连接数、命令延迟）、命令审计日志 — 工单拆分：02 命令审计日志（✅ 已完成 2026-11-26：`Config.AuditLog` 默认关闭，slog 结构化输出 remote/command/duration_ms/result，认证失败事件纳入审计，SetConfig 接入 `auditlog` key）、03 Prometheus 指标（用户确认引入 client_golang，独立 registry 不写 DefaultRegisterer）
16. **`Config.RootMode` 去接口化**（M3）— ✅ 已完成（2026-11-26，工单 01）：公开字段删除，根模式经 `CmdLine.rootMode` → `CommandContext` → `server.context` 显式传递；server 的 `interface{}` 类型断言兜底分支删除（context 缺失直接拒绝连接并告警）；约 22 处测试构造点迁移，`go test -race ./...` 全绿

---

## 8. 实际目录结构

```
tnlcmd/
├── tnlcmd.go                    # 公共 API 外观层 (83 行)
├── integration_test.go          # 集成测试 (1802 行，integration 构建标签)
├── REVIEW.md / CODE_REVIEW.md   # 代码审查报告（CODE_REVIEW.md 为唯一有效版本）
├── DESIGN_DOC.md / CODE_WIKI.md # 本文档与代码维基
├── Makefile                     # build/vet/test/test-fast/clean 目标
├── LICENSE                      # MIT
├── .github/workflows/ci.yml     # CI（build/vet/test -race + integration 标签）
├── .golangci.yml                # golangci-lint（默认标准 linter）
├── pkg/
│   └── types/
│       └── types.go             # 公共类型 + Sentinel/哨兵错误 (118 行)
├── internal/
│   ├── auditlog/
│   │   ├── auditlog.go          # 按日期滚动的审计日志 (237 行)
│   │   └── auditlog_test.go
│   ├── cmdline/
│   │   └── cmdline.go           # 编排层 (480 行)
│   │   └── cmdline_test.go
│   ├── commandtree/
│   │   ├── command_tree.go      # Trie 核心 (793 行)
│   │   ├── placeholder.go       # zebra 占位符 (207 行)
│   │   └── *_test.go
│   ├── completer/
│   │   ├── completer.go         # 补全引擎 (429 行)
│   │   └── *_test.go
│   ├── debuglog/
│   │   ├── debuglog.go          # 环境变量调试日志 (113 行)
│   │   └── debuglog_test.go
│   ├── history/
│   │   ├── history.go           # 命令历史 (89 行，环形缓冲)
│   │   └── history_test.go
│   ├── mode/
│   │   ├── mode.go              # 视图与上下文 (129 行)
│   │   └── mode_test.go
│   ├── server/
│   │   ├── server.go            # TCP 服务器 (362 行)
│   │   └── server_test.go
│   └── session/
│       ├── session.go           # 终端会话 (1143 行)
│       ├── inputfilter.go       # 共享 Telnet/ANSI 输入过滤器 (244 行)
│       └── session_test.go
├── cmd/
│   └── zebra_example/
│       └── zebra_example.go     # Zebra 风格示例
├── examples/
│   └── example/
│       └── main.go              # 简单示例
├── go.mod                       # go 1.24.9, 零依赖
├── AGENTS.md
└── README.md
```

---

## 9. 总结

TNLCMD 是一个架构清晰、功能实用的 Telnet CLI 库。相比早期版本，当前实现已解决全局状态、上下文共享、bufio 复用、魔术字符串、调试日志等核心问题，并新增了输出分页、变长 Telnet IAC 状态机、全角问号支持、唯一前缀匹配、参数补全、审计日志、安全能力、输入边界与读超时等能力。

**主要优势**:
- 分层架构清晰，包职责分明，零外部依赖
- 命令唯一来源：每视图独立 CommandTree（工单 12 移除双命令树）
- 输入状态机统一为共享过滤器（工单 11），变长 Telnet/ANSI/UTF-8 与长度边界全路径一致
- 配置快照语义（工单 02）+ 全键校验（工单 03），运行期无配置数据竞争
- 审计输出走专用 logger（工单 05），不污染进程全局日志

**2026-11-27 审查轮次**：`.scratch/code-review-fixes/` 工单 01–08、11、12、13 已完成；09（多级视图/带参切换）、10（恒定时间密码比较/退避/BindAddr）、15（Prometheus 指标）经用户决定废弃；14（文档收敛）完成后本节与实现保持同步。细节与逐项证据见 `CODE_REVIEW.md`。
