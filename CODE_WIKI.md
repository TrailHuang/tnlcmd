# TNLCMD Code Wiki

> 一个纯 Go 实现的 Telnet 命令行服务器库，模拟 Cisco IOS / Zebra 风格的网络设备 CLI 交互体验。
> 支持多级命令树（Trie）、Tab 补全、命令历史、参数验证和层级视图（Mode）切换。

- **模块路径**：`github.com/TrailHuang/tnlcmd`
- **Go 版本**：1.24.9
- **外部依赖**：零外部依赖（仅 Go 标准库）
- **许可证**：MIT

---

## 目录

1. [项目概述](#1-项目概述)
2. [整体架构](#2-整体架构)
3. [目录结构](#3-目录结构)
4. [模块职责详解](#4-模块职责详解)
5. [关键类与函数说明](#5-关键类与函数说明)
6. [依赖关系](#6-依赖关系)
7. [核心流程与数据流](#7-核心流程与数据流)
8. [项目运行方式](#8-项目运行方式)
9. [已知问题与改进建议](#9-已知问题与改进建议)

---

## 1. 项目概述

TNLCMD 是一个分层清晰的 Telnet CLI 库，目标是让使用方通过少量 API 即可启动一个具备专业网络设备交互体验的命令行服务：

- **多客户端并发**：每个 TCP 连接由独立 goroutine 处理。
- **Trie 命令树**：支持 `show running-config`、`set debug <1-10> (on|off)` 等多级、带参数的命令语法。
- **参数类型**：枚举 `(a|b)`、范围 `<1-10>`、字符串 `STRING`、可选 `[OPT]`。
- **Tab 补全 / `?` 帮助**：基于当前视图命令树实时生成建议。
- **历史命令**：环形缓冲 + 上下键导航 + 去重。
- **视图（Mode）系统**：模拟 `root → configure / interface` 层级 CLI，每个视图拥有独立命令树。
- **优雅关闭**：信号处理 + context 取消 + 会话清理。

---

## 2. 整体架构

### 2.1 分层架构图

```
┌─────────────────────────────────────────────────────┐
│                   Public API                        │
│              tnlcmd.go (外观层)                      │
│   NewCmdLine / RegisterCommand / Start / Stop       │
├─────────────────────────────────────────────────────┤
│                  Orchestration                      │
│          internal/cmdline/cmdline.go                │
│     配置管理 · 模式注册 · 服务生命周期                │
├──────────┬──────────┬──────────┬────────────────────┤
│  Server  │ Session  │  Mode    │  CommandTree       │
│  (TCP)   │ (终端IO) │ (视图)   │  (Trie解析)        │
├──────────┼──────────┼──────────┼────────────────────┤
│          │ History  │Completer │                    │
│          │ (历史)   │ (补全)   │                    │
├──────────┴──────────┴──────────┴────────────────────┤
│               pkg/types/types.go                    │
│        公共类型: Config, CommandHandler, ...         │
└─────────────────────────────────────────────────────┘
```

### 2.2 核心组件职责一览

| 组件 | 文件 | 职责 |
|------|------|------|
| **Public API** | `tnlcmd.go` | 对外暴露的类型别名与方法代理 |
| **Orchestrator** | `internal/cmdline/cmdline.go` | 组装各子系统，管理服务生命周期 |
| **TCP Server** | `internal/server/server.go` | TCP 监听、连接接受、会话创建 |
| **Session** | `internal/session/session.go` | 逐字节终端 I/O、ANSI 转义、Telnet 协商 |
| **Command Tree** | `internal/commandtree/command_tree.go` | Trie 命令存储、参数解析、命令匹配 |
| **Completer** | `internal/completer/completer.go` | Tab 补全、`?` 帮助、参数建议 |
| **History** | `internal/history/history.go` | 环形缓冲区、上下键导航 |
| **Mode** | `internal/mode/mode.go` | 视图层级、模式切换、上下文管理 |
| **Types** | `pkg/types/types.go` | 公共类型定义 |

---

## 3. 目录结构

```
tnlcmd/
├── tnlcmd.go                         # 公共 API 外观层
├── go.mod / go.sum                   # 模块定义（零外部依赖）
├── README.md                         # 项目说明
├── DESIGN_DOC.md                     # 设计文档与代码审查
├── reasonix.toml                     # 工具权限配置
├── pkg/
│   └── types/
│       └── types.go                  # 公共类型（Config / CommandHandler / 节点类型等）
├── internal/
│   ├── cmdline/
│   │   └── cmdline.go                # 编排层：组装 server/mode/commandtree
│   ├── server/
│   │   └── server.go                 # Telnet TCP 服务器
│   ├── session/
│   │   └── session.go                # 单连接会话处理（终端 I/O、行编辑）
│   ├── commandtree/
│   │   ├── command_tree.go           # Trie 命令树核心
│   │   └── command_tree_test.go      # 单元测试
│   ├── completer/
│   │   ├── completer.go              # 补全 / 帮助引擎
│   │   └── completer_test.go         # 单元测试
│   ├── history/
│   │   ├── history.go                # 历史命令环形缓冲
│   │   └── history_test.go           # 单元测试
│   └── mode/
│       └── mode.go                   # 视图模式与上下文
├── cmd/
│   └── zebra_example/
│       └── zebra_example.go          # Zebra 风格完整示例（端口 2323）
└── examples/
    └── example/
        └── main.go                   # 简单示例（端口 2324）
```

---

## 4. 模块职责详解

### 4.1 `tnlcmd.go` — 公共 API 外观层

包名 `tnlcmd`，是唯一对外的入口。通过**类型别名**与**嵌入式结构体**代理 `internal/cmdline` 的能力，隐藏内部包结构。

- 导出类型：`CmdLine`、`Config`、`CommandHandler`
- 提供方法：`NewCmdLine`、`RegisterCommand`、`RegisterModeCommand`、`CreateMode`、`Start`、`Stop`、`SetConfig`
- 提供 `DefaultConfig()` 返回默认配置（端口 2323、MaxHistory 100）

### 4.2 `pkg/types` — 公共类型

定义跨包共享的类型，避免循环导入：

- `CommandHandler func(args []string) string` — 命令处理函数签名
- `CommandInfo` — 命令元信息（Name / Description / Handler）
- `CommandNodeType` — 节点类型枚举（Command / Optional / Enum / Num / String / ModeSwitch / Exit）
- `Config` — 服务配置（Prompt / Port / WelcomeMsg / MaxHistory / PageSize / RootMode / logger）
- **Sentinel 常量**：`SentinelExit = "__EXIT__"`、`SentinelExitToRoot = "__EXIT_TO_ROOT__"`，用于 handler 向 session 层传达「关闭连接」或「退回根模式」的意图。

### 4.3 `internal/cmdline` — 编排层

`CmdLine` 结构体是核心编排者，持有：

| 字段 | 作用 |
|------|------|
| `config *Config` | 服务配置 |
| `commands map[string]CommandInfo` | 向后兼容的平面命令存储 |
| `commandTree *commandtree.CommandTree` | 全局命令树 |
| `rootMode *mode.CommandMode` | 根视图（privileged EXEC） |
| `context *mode.CommandContext` | 命令上下文（当前模式 + 路径） |
| `server *server.TelnetServer` | Telnet 服务器实例 |
| `mu sync.RWMutex` | 并发保护 |

职责：
1. `NewCmdLine`：创建根模式、命令树、上下文。
2. `RegisterCommand`：同时写入根模式平面存储与全局命令树。
3. `RegisterModeCommand` / `CreateMode`：通过 `findOrCreateMode` 查找/创建子视图，并自动注入 `exit`/`quit` 与视图切换命令。
4. `Start`：注册内置命令 → 创建 `TelnetServer` → 启动监听。
5. `Stop`：停止服务器并清理会话。
6. `SetConfig`：运行时动态修改 prompt/welcome/maxhistory/port/pagesize。
7. 提供 `CreateExitToRootHandler` / `CreateCloseConnectionHandler` 工厂，返回 Sentinel 字符串。

### 4.4 `internal/server` — Telnet TCP 服务器

`TelnetServer` 负责：
- `Start()`：`net.Listen("tcp", :port)` 并启动 `acceptConnections` goroutine；`Config.IdleTimeout > 0` 时额外启动 `reapIdleSessions` 回收 goroutine。
- `acceptConnections()`：循环 `Accept`，每连接开一个 goroutine 调 `handleConnection`。
- `handleConnection`：**复制** `CommandContext`（浅拷贝 `CurrentMode` 指针），为每连接创建 `Session`；注册与 `Config.MaxConnections` 上限检查在同一临界区原子完成，超限连接收 `% Too many connections` 提示后关闭。
- `reapIdleSessions` / `closeStaleSessions`：按阈值一半间隔扫描（下限 100ms），`Session.IsStale` 判定空闲超时后锁外 `Close`，解除会话 `readLine` 阻塞使其自行退出清理。
- `Stop()`：`cancel()` context、关闭 listener、关闭所有会话。
- 维护 `sessions map[net.Conn]*session.Session` 与 `sync.RWMutex`。

### 4.5 `internal/session` — 会话与终端 I/O

`Session` 是最复杂的模块，单连接独占一个实例，负责：

0. **可选密码认证** `authenticate()`：`Config.Password` 非空时在欢迎消息前执行——`readPasswordLine` 提示 `Password:` 并**不回显**读取（共享 readLine 的 IAC/ESC 状态机），错误达 `Config.MaxAuthAttempts`（默认 3）次后发送断开提示并关闭连接；认证阶段有 60s 读超时防止槽位被不发密码的客户端无限占用，成功后清除超时。
1. **Telnet 协商**：`enableTelnetCharacterMode` 发送 `IAC WILL ECHO`、`IAC DO/WILL SUPPRESS_GO_AHEAD`。
2. **逐字节行编辑** `readLine()`：解析每个字节
   - 批量读取复用会话级读缓冲 `Session.readBuf`（`readBufSize = 1024`），不再每轮 `make` 分配；状态机跨批次保持
   - 行内容维护在 `lineEditor`（字节缓冲 + 光标）：←/→（ESC[C/D）、Ctrl+A/E 跳行首/行尾、Home/End（ESC[H/F、1~/4~/7~/8~）、Delete（ESC[3~）按光标位置删字；行尾插入逐字节回显，行中插入/删除整行重绘（`redrawLineCursor` 以 ESC[nD 定位光标）；历史回显/Tab 补全后光标复位行尾
   - `0xFF` IAC → 跳过 3 字节协商序列
   - `0x03`/`0x04` Ctrl+C/D → 返回 `io.EOF`
   - `0x7F`/`0x08` Backspace → 删字符并 `\r\033[K` 重绘
   - `0x09` Tab → `handleTabCompletion`
   - `0x3F` `?` → `showCommandHelp`
   - `0x0D`/`0x0A` Enter → 返回当前行
   - `0x1B` ESC → 识别 `[A`/`[B` 上下方向键，浏览历史
3. **命令执行** `processCommand`：
   - 在当前视图命令树 `FindCommand`，回退到根模式树查找 ModeSwitch 命令。
   - 命中 ModeSwitch → `context.ChangeMode` 切换视图。
   - 命中普通命令 → `validateCommandParameters` 校验参数 → 调用 handler。
   - handler 返回 Sentinel → 退出连接 / 退回根模式。
   - 普通输出经 `writeResult` 输出（见下）。
4. **结果输出与分页** `writeResult`：当 `Config.PageSize > 0` 时按 `\r\n` 拆分逐行输出，每 `PageSize` 行插入 `--More-- (NN%)` 提示并调用 `readPagingKey` 等待按键；Space/Enter/方向键 → 下一页，`q`/`Q` → 中止剩余输出，Ctrl+C → 中止并关闭连接。`PageSize <= 0` 时一次性输出。
5. **写入错误传播**：`writerWrite` 返回 error 并记入 `s.writeErr`；`Handle` 主循环在每次命令后检查 `s.writeErr`，写入失败（如客户端断开）立即退出会话，避免向死连接继续写入。
6. **参数校验** `validateCommandParameters`：从叶子节点向根回溯统计 required/optional 参数数量，逐个用 `IsParameterMatch` 验证值。
7. **换行规范化** `normalizeLineEndings`：统一为 `\r\n`。

### 4.6 `internal/commandtree` — Trie 命令树

命令字符串被解析为 Trie 节点链。核心结构 `CommandNode`：

| 字段 | 说明 |
|------|------|
| `Name` / `Type` / `Description` | 节点基础信息 |
| `Handler` | 仅叶子节点持有 |
| `Children map[string]*CommandNode` | 子节点 |
| `Parent` | 父指针（用于参数回溯） |
| `EnumValues` / `RangeMin` / `RangeMax` / `IsRequired` | 参数元数据 |
| `ModeName` | ModeSwitch 节点目标视图名 |

**参数语法解析** `parseCommandPart`：

| 语法 | 节点类型 | 示例 |
|------|---------|------|
| `plain` | `NodeTypeCommand` | `show`, `running-config` |
| `(a\|b\|c)` | `NodeTypeEnum` | `(on\|off)` |
| `<1-10>` | `NodeTypeNum` | `<1-100>` |
| `STRING`（全大写） | `NodeTypeString` | `HOSTNAME` |
| `[OPT]` | `NodeTypeOptional` | `[STRRING]` |

**匹配策略** `findCommand`：优先精确匹配 Command 节点 → 回退到参数节点（Enum/Num/String/Optional）。Optional 节点支持「包含/不包含」两种递归路径。

`CommandTree` 还持有 `ModeCommands map[string]*CommandNode`（实例级视图切换命令存储，已从全局变量迁移为实例字段）。

### 4.7 `internal/completer` — 补全引擎

`CommandCompleter` 持有 `*mode.CommandContext`，基于**当前视图的命令树**工作：

- `GetCommandTreeSuggestions`：`?` 帮助主入口，返回「命令名（32 宽左对齐） - 描述」格式列表，并附带视图切换命令。
- `GetNextLevelCompletions`：Tab 补全主入口，处理唯一匹配（自动补全/展开 ModeSwitch 目标子命令）与多匹配（列出全部）。
- `GetParameterCompletions`：参数节点补全。
- `walkExact`：共享的逐 token 精确匹配导航，`GetNextLevelCompletions` 与 `GetParameterCompletions` 复用。
- `formatCandidate`：统一展示格式（名称定宽 32 + 描述），`?` 帮助与下钻候选共用。
- `describeChildrenWithCR`：生成下钻候选并按需追加 `<cr>`（存在可选参数子节点时）。
- `resolveChildren`：当节点是 ModeSwitch 时，返回目标视图命令树的根子节点。
- 输出契约由 `TestProductionPathContracts` 守护：Tab 补全/参数补全返回裸名称（供整行回填），`?` 帮助返回展示行。

### 4.8 `internal/history` — 命令历史

`CommandHistory` 线程安全环形缓冲（`sync.RWMutex`）：底层为固定容量数组 `buf`，配合 `start`（最旧记录物理下标）与 `size`（有效记录数）实现环形布局。

- `Add`：去重（连续相同不入队）；未满时写入尾部，满了则覆盖最旧记录并前移 `start` —— 恒为 O(1)。
- `Get`/`GetAll`/`Len`：只读查询，按逻辑下标/逻辑顺序返回，不暴露物理布局。
- `Previous`/`Next`：上下键导航，`position = -1` 表示未在浏览中。
- `ResetPosition`：重置浏览位置。

### 4.9 `internal/mode` — 视图模式系统

`CommandMode` 表示一个视图，每个视图拥有**独立的 `CommandTree`**：

- `NewCommandMode`：自动规范 Prompt（根模式以 `>` 结尾，子模式以 `#` 结尾）。
- `AddCommand`：同时写入平面 `Commands` map 与独立 `CommandTree`。
- `AddSubMode`：建立父子关系。
- `CommandContext`：维护 `CurrentMode` + `Path`，`ChangeMode` 重建路径链，`GetRootMode` 回溯到根，`EnterMode` 为模式切换语义唯一实现（ChangeMode + 标准提示消息，processCommand 树节点分支与 `GetAvailableCommands` 切换 handler 均委托），`GetAvailableCommands` 合并当前模式命令与所有视图切换命令。

---

## 5. 关键类与函数说明

### 5.1 公共 API（`tnlcmd.go`）

| 符号 | 签名 | 说明 |
|------|------|------|
| `NewCmdLine` | `(config *Config) *CmdLine` | 创建 CLI 实例，初始化根模式/命令树/上下文 |
| `(*CmdLine).RegisterCommand` | `(name, description string, handler, detailedDescription ...string)` | 注册命令到根模式 |
| `(*CmdLine).RegisterModeCommand` | `(modePath, name, description string, handler, detailedDescription ...string)` | 注册命令到指定视图 |
| `(*CmdLine).CreateMode` | `(modePath, description string)` | 创建新视图 |
| `(*CmdLine).Start` | `() error` | 启动 Telnet 服务 |
| `(*CmdLine).Stop` | `() error` | 停止服务 |
| `(*CmdLine).SetConfig` | `(key, value string) error` | 动态配置（prompt/welcome/maxhistory/port/pagesize） |
| `DefaultConfig` | `() *Config` | 默认配置 |

### 5.2 `pkg/types` 关键类型

```go
type CommandHandler func(args []string) string

const (
    SentinelExit        = "__EXIT__"          // 关闭连接
    SentinelExitToRoot  = "__EXIT_TO_ROOT__"  // 退回根模式
)

type CommandNodeType int  // Command / Optional / Enum / Num / String / ModeSwitch / Exit

type Config struct {
    Prompt, WelcomeMsg string
    Port, MaxHistory   int
    PageSize           int           // <=0 禁用分页
    RootMode           interface{}   // 内部为 *mode.CommandMode
    logger             *slog.Logger
}
```

### 5.3 `internal/commandtree` 关键函数

| 函数 | 说明 |
|------|------|
| `NewCommandTree()` | 创建含 root 节点的空树 |
| `(*CommandTree).AddCommand(command, desc, handler, detailed...)` | 解析命令字符串并插入 Trie，支持多行详细描述 |
| `(*CommandTree).AddModeCommand(modeName, desc)` | 注册视图切换节点 |
| `(*CommandTree).FindCommand(args) (node, path, args, err)` | 查找命令，返回匹配节点、路径、提取的参数 |
| `(*CommandNode).findCommand(...)` | 递归匹配：精确 Command → 参数节点 → Optional 双路径 |
| `IsParameterMatch(node, input)` | 参数值校验入口（Num/Enum/String） |
| `GetNumberValidationError` / `GetEnumValidationError` | 生成具体的参数错误信息 |
| `PrintTree()` | 调试用：打印整棵命令树 |

### 5.4 `internal/session` 关键函数

| 函数 | 说明 |
|------|------|
| `NewSessionWithContext(conn, config, ctx)` | 创建会话，启用 Telnet 字符模式 |
| `(*Session).Handle(ctx)` | 会话主循环：欢迎消息 → readLine → processCommand |
| `(*Session).readLine()` | 逐字节行编辑，处理 IAC/ANSI/Tab/?/Enter |
| `(*Session).processCommand(cmd)` | 命令分发：视图切换 / 参数校验 / handler 调用 / Sentinel 处理 / 经 writeResult 输出 |
| `(*Session).writeResult(result)` | 输出命令结果；`PageSize>0` 时分页（`--More--`），返回写入错误或 io.EOF（Ctrl+C） |
| `(*Session).readPagingKey()` | 分页提示后读取按键，跳过 IAC/ANSI 序列 |
| `(*Session).writerWrite(data)` | 写入并 flush；返回 error 并记入 `writeErr` |
| `(*Session).validateCommandParameters` | 从叶子向根回溯统计并校验参数 |
| `(*Session).handleTabCompletion` | Tab 补全逻辑 |
| `(*Session).showCommandHelp` | `?` 帮助逻辑 |

### 5.5 `internal/completer` 关键函数

| 函数 | 说明 |
|------|------|
| `NewCommandCompleterWithContext(ctx)` | 基于上下文创建补全器 |
| `GetCommandTreeSuggestions(input)` | `?` 帮助：返回「命令名 - 描述」列表 |
| `GetNextLevelCompletions(input)` | Tab 补全：唯一/多匹配处理 |
| `GetParameterCompletions(input)` | 参数节点补全 |
| `resolveChildren(node)` | ModeSwitch 节点解析为目标视图子命令 |

### 5.6 `internal/mode` 关键函数

| 函数 | 说明 |
|------|------|
| `NewCommandMode(name, prompt, desc)` | 创建视图，自动规范 Prompt |
| `(*CommandMode).AddCommand(...)` | 同时写入平面 map 与独立命令树 |
| `(*CommandContext).ChangeMode(newMode)` | 切换视图并重建路径 |
| `(*CommandContext).GetRootMode()` | 回溯到根视图 |
| `(*CommandContext).GetAvailableCommands()` | 合并当前命令 + 所有视图切换命令 |

---

## 6. 依赖关系

### 6.1 包依赖图

```
tnlcmd.go
   ├──> internal/cmdline
   └──> pkg/types

internal/cmdline
   ├──> internal/commandtree
   ├──> internal/mode
   ├──> internal/server
   └──> pkg/types

internal/server
   ├──> internal/commandtree
   ├──> internal/mode
   ├──> internal/session
   └──> pkg/types

internal/session
   ├──> internal/commandtree
   ├──> internal/completer
   ├──> internal/history
   ├──> internal/mode
   └──> pkg/types

internal/completer
   ├──> internal/commandtree
   ├──> internal/mode
   └──> pkg/types

internal/mode
   ├──> internal/commandtree
   └──> pkg/types

internal/commandtree
   └──> pkg/types

pkg/types
   └──> log/slog (仅用于 Config.logger)
```

依赖方向严格遵循「外观 → 编排 → 子系统 → 公共类型」，无循环依赖。`pkg/types.RootMode` 使用 `interface{}` 是为打破 `types → mode` 的潜在循环。

### 6.2 标准库使用

| 包 | 用途 |
|----|------|
| `net` | TCP 监听与连接 |
| `bufio` | 连接读写缓冲 |
| `context` | 服务生命周期与取消 |
| `sync` | `RWMutex` 并发保护 |
| `log/slog` | 结构化日志 |
| `regexp` | 数字范围/枚举值正则提取 |
| `reflect` / `runtime` | 调试用：获取 handler 函数名 |
| `strconv` / `strings` / `fmt` / `sort` / `time` | 通用工具 |

---

## 7. 核心流程与数据流

### 7.1 启动流程

```
main()
  └─ cmdline.NewCmdLine(config)        // 创建根模式/命令树/上下文
  └─ cmdline.RegisterCommand(...)      // 注册命令到根模式命令树
  └─ cmdline.CreateMode("configure")   // 创建子视图（自动注入 exit/quit + ModeSwitch）
  └─ cmdline.RegisterModeCommand(...)  // 注册命令到子视图命令树
  └─ cmdline.Start()
        ├─ registerBuiltinCommands()   // exit / quit
        ├─ server.NewTelnetServerWithContext(config, context)
        └─ server.Start()              // net.Listen + go acceptConnections()
```

### 7.2 连接处理流程

```
Telnet Client ──TCP──▶ Server.Accept()
                          │
                    ┌─────▼──────┐
                    │ handleConn │  复制 CommandContext（浅拷贝 CurrentMode）
                    └─────┬──────┘
                          │ session.NewSessionWithContext()
                          │   ├─ 创建 history / completer
                          │   └─ enableTelnetCharacterMode()  // IAC 协商
                          ▼
                    session.Handle(ctx)
                          │
                    ┌─────▼──────┐
                    │  readLine  │  逐字节读取
                    └─────┬──────┘
                          │ 0xFF→IAC协商 / 0x1B→方向键 / 0x09→Tab / 0x3F→? / 0x0D→Enter
                          ▼
                    processCommand(line)
                          │
                    ┌─────▼──────┐
                    │ 当前视图   │.FindCommand(parts)
                    │ CommandTree│
                    └─────┬──────┘
                          │  回退到根模式树查找 ModeSwitch
                          ▼
                    ┌─────▼──────┐
                    │  Handler   │(args)
                    └─────┬──────┘
                          │ 返回 SentinelExit        → io.EOF（关闭连接）
                          │ 返回 SentinelExitToRoot  → ChangeMode(root)
                          │ 返回普通字符串            → 写回客户端
                          ▼
                    循环直到 ctx.Done() 或 EOF
```

### 7.3 命令匹配策略

`FindCommand` 递归匹配优先级：
1. 单参数时优先查 `ModeCommands`（视图切换命令）
2. 精确匹配 `NodeTypeCommand` / `NodeTypeModeSwitch` 子节点
3. 回退到参数节点：`IsParameterMatch` 校验当前值
   - `NodeTypeOptional`：同时尝试「匹配当前值」与「跳过」两条路径
4. 无匹配但有 handler：剩余参数作为 handler 入参

### 7.4 视图切换机制

- `CreateMode("configure")` → 在 root 下创建子 `CommandMode`，并注册 `NodeTypeModeSwitch` 节点。
- 用户输入 `configure` → `FindCommand` 命中 ModeSwitch → `context.ChangeMode(subMode)` → `updateCommands()` 刷新补全器上下文。
- 子视图自动注入 `exit`（SentinelExit，关连接）与 `quit`（SentinelExitToRoot，回根）。

---

## 8. 项目运行方式

### 8.1 编译

```bash
# 编译 Zebra 风格示例（端口 2323）
go build ./cmd/zebra_example

# 编译简单示例（端口 2324）
go build ./examples/example

# 全量构建 + 静态检查 + 测试
go build ./...
go vet ./...
go test -race ./internal/... -count=1
```

### 8.2 运行并连接

```bash
# 启动示例
./zebra_example
# 或
./example

# 用 telnet 连接
telnet localhost 2323   # zebra_example
telnet localhost 2324   # example
```

### 8.3 在自己的项目中使用

```go
package main

import (
    "log"
    "os"
    "os/signal"
    "syscall"

    "github.com/TrailHuang/tnlcmd"
)

func main() {
    config := tnlcmd.DefaultConfig()
    config.Port = 4000

    cli := tnlcmd.NewCmdLine(config)

    // 注册普通命令
    cli.RegisterCommand("show version", "Show version", func(args []string) string {
        return "v1.0.0\r\n"
    })

    // 注册带参数的命令：<1-100> 范围参数、(on|off) 枚举参数
    cli.RegisterCommand("set level <1-100>", "Set level", func(args []string) string {
        return "level set to " + args[0] + "\r\n"
    })

    // 创建视图并注册命令
    cli.CreateMode("configure", "global configuration")
    cli.RegisterModeCommand("configure", "hostname NAME", "Set hostname", func(args []string) string {
        return "hostname set\r\n"
    })

    if err := cli.Start(); err != nil {
        log.Fatal(err)
    }

    sig := make(chan os.Signal, 1)
    signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
    <-sig
    _ = cli.Stop()
}
```

### 8.4 键盘交互

| 按键 | 行为 |
|------|------|
| `Tab` | 命令补全（唯一匹配自动补全，多匹配列出全部） |
| `?` | 显示当前层级可用命令及描述 |
| `↑` / `↓` | 浏览历史命令 |
| `Backspace` | 删除字符 |
| `Ctrl+C` / `Ctrl+D` | 退出会话 |
| `Enter` | 执行当前行 |
| `exit` | 关闭连接 |
| `quit` | 退回根模式 |
| **分页时** | |
| `Space` / `Enter` / 方向键 | `--More--` 处翻到下一页 |
| `q` / `Q` | `--More--` 处中止剩余输出，回到提示符 |
| `Ctrl+C` | `--More--` 处中止并关闭连接 |

---

## 9. 已知问题与改进建议

> 详见 `DESIGN_DOC.md` 第 6 节，此处摘录关键项。

### 9.1 关键问题

> P0 / P1 问题已在源码中修复，下表标注修复状态与处理方式；P2 项暂未处理。

| 优先级 | 问题 | 影响 | 状态 | 修复方式 |
|--------|------|------|------|----------|
| **P0** | Session 上下文浅拷贝 `CurrentMode` 指针，多连接共享同一模式对象 | 并发修改风险 | ✅ 已修复 | 为 `mode.CommandContext` 新增 `Clone()` 方法（深拷贝 `Path` 切片），`server.handleConnection` 改用 `Clone()` 为每个连接生成独立上下文副本 |
| **P0** | `Config.RootMode` 为 `interface{}` 且在多处类型断言无保护 | 可能 panic | ✅ 已修复 | `server.go` / `session.go` 中的类型断言改为 comma-ok 模式，类型不匹配时记录日志并安全降级（关闭连接或回退到空根模式）而非 panic |
| **P1** | `__EXIT__` / `__EXIT_TO_ROOT__` 魔术字符串 | 应改为 Sentinel Error | ✅ 已解决 | 字符串已集中为 `types.SentinelExit` / `types.SentinelExitToRoot` 常量，全代码库无裸字面量；因 `CommandHandler` 返回 `string`（CLI 输出契约），保留字符串常量方案，不引入破坏性 API 变更 |
| **P1** | Completer 存在三套相似遍历逻辑 | 可维护性差 | ✅ 已修复 | 抽取共享辅助函数 `resolveNodeForInput` / `collectCommandCompletions` / `applyCommonPrefix` / `buildFullCommand`，`Complete`、`GetCompletions`、`GetNextLevelCompletions` 复用同一遍历逻辑；同时修复了空输入下 `buildFullCommand` 的潜在越界 |
| **P1** | `readLine` 假设 IAC 恒为 3 字节，2 字节 IAC 命令（NOP/GA/AYT/EC/IAC-IAC）后的用户字节被吞掉 | `?` 间歇性失效，多输几次又恢复 | ✅ 已修复 | 用状态机（`inputState`：stNormal/stIAC/stIACOpt/stSB/stSBIAC/stESC/stESCBracket）替换 pending 缓冲，根据 IAC 命令字节区分 2/3 字节序列；状态跨 read 批次保持，正确处理变长 IAC 与 ESC 方向键序列 |
| **P2** | 无连接数限制 / 无认证 / 无 TLS | 安全性不足 | ⏳ 未处理 | — |
| **P2** | README 声称的左右箭头光标移动未实现 | 功能缺失 | ⏳ 未处理 | — |

### 9.2 测试覆盖

| 包 | 测试文件 | 覆盖内容 | 状态 |
|----|---------|---------|------|
| `commandtree` | `command_tree_test.go` | Trie 增删查、参数匹配、视图切换节点 | ✅ 已有 |
| `completer` | `completer_test.go` | Tab 补全、`?` 帮助、参数补全、ModeSwitch 解析 | ✅ 已有 |
| `history` | `history_test.go` | 环形缓冲、去重、上下键导航 | ✅ 已有 |
| `mode` | `mode_test.go` | 提示符规范化、AddCommand 双存储、AddSubMode、ChangeMode 路径重建、GetRootMode、GetAvailableCommands 含 ModeSwitch、**Clone 深拷贝 Path / 隔离性 / nil 安全** | ✅ 新增 |
| `session` | `session_test.go` | 基于 `net.Pipe()` 的端到端测试：基础命令执行、`exit` 关闭连接、未知命令提示、**分页（PageSize 翻页 / `q` 中止 / PageSize=0 禁用）**、**客户端断开后写入错误使 Handle 及时退出** | ✅ 新增 |
| `server` | `server_test.go` | 基于 `net.Pipe`/真实监听：启动接受连接并发送欢迎消息、`Stop` 关闭所有会话、**每连接上下文隔离（Clone）** | ✅ 新增 |

> 备注：`session` / `server` 测试使用 `net.Pipe()` 模拟 Telnet 客户端，通过 `readUntil` 辅助函数按标记字符串消费输出，避免与提示符格式耦合。运行：`go test -race ./internal/... -count=1`

---

*本文档基于源码静态分析生成，反映仓库当前状态。*
