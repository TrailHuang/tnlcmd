# TNLCMD 项目设计文档

## 1. 项目概述

**TNLCMD** 是一个纯 Go 语言实现的 Telnet 命令行服务器库，模拟 Cisco IOS / Zebra 风格的网络设备 CLI 交互体验。支持多级命令树、Tab 补全、命令历史、参数验证和层级视图切换。

- **语言**: Go 1.24.9
- **依赖**: 零外部依赖（仅 Go 标准库）
- **许可证**: MIT
- **代码量**: ~2074 行（9 个包）

---

## 2. 系统架构

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

### 2.2 核心组件职责

| 组件 | 文件 | 职责 |
|------|------|------|
| **Public API** | `tnlcmd.go` | 对外暴露的类型别名和方法代理 |
| **Orchestrator** | `internal/cmdline/cmdline.go` | 组装各子系统，管理服务生命周期 |
| **TCP Server** | `internal/server/server.go` | TCP 监听、连接接受、会话创建 |
| **Session** | `internal/session/session.go` | 逐字节终端 I/O、ANSI 转义、Telnet 协商 |
| **Command Tree** | `internal/commandtree/command_tree.go` | Trie 命令存储、参数解析、命令匹配 |
| **Completer** | `internal/completer/completer.go` | Tab 补全、`?` 帮助、参数建议 |
| **History** | `internal/history/history.go` | 环形缓冲区、上下键导航 |
| **Mode** | `internal/mode/mode.go` | 视图层级、模式切换、上下文管理 |
| **Types** | `pkg/types/types.go` | 公共类型定义 |

### 2.3 数据流

```
Telnet Client ──TCP──▶ Server.Accept()
                          │
                    ┌─────▼──────┐
                    │  Session   │
                    │  .Handle() │
                    └─────┬──────┘
                          │ readLine() — 逐字节读取
                          │   ├─ 0xFF → Telnet IAC 协商
                          │   ├─ 0x1B → ANSI 方向键
                          │   ├─ 0x09 → Tab → Completer
                          │   ├─ 0x3F → ?   → Completer
                          │   └─ 0x0D → Enter
                          ▼
                    processCommand()
                          │
                    ┌─────▼──────┐
                    │ Mode's     │
                    │ CommandTree│.FindCommand(parts)
                    └─────┬──────┘
                          │
                    ┌─────▼──────┐
                    │  Handler   │
                    │  (args)    │
                    └─────┬──────┘
                          │ 返回值 "____EXIT__" → io.EOF
                          │ 返回值 "____EXIT_TO_ROOT__" → 切换根模式
                          ▼
                    发送响应 → 更新模式/命令 → 循环
```

---

## 3. 核心设计

### 3.1 命令树 (Trie)

命令字符串被解析为 Trie 节点链，支持以下参数语法：

| 语法 | 节点类型 | 示例 |
|------|---------|------|
| `plain` | `NodeTypeCommand` | `show`, `running-config` |
| `(a\|b\|c)` | `NodeTypeEnum` | `(on\|off)` |
| `<1-10>` | `NodeTypeNum` | `<1-100>` |
| `STRING` | `NodeTypeString` | `HOSTNAME`（全大写） |
| `[OPT]` | `NodeTypeOptional` | `[STRRING]` |

**匹配策略**: 优先精确匹配 Command 节点 → 回退到参数节点（Enum/Num/String/Optional）

### 3.2 视图模式系统

```
root (privileged EXEC)
├── configure (global config)
│   └── (命令: router, hostname, banner, ...)
├── interface (interface config)
│   └── (命令: ip, description, shutdown, no, ...)
```

- 每个 Mode 拥有**独立的 CommandTree**
- `CommandContext` 维护当前模式和路径
- `ChangeMode()` 重建模式路径链
- 模式切换命令作为 `NodeTypeModeSwitch` 全局可用

### 3.3 终端会话处理

- **逐字节读取**: 使用 `bufio.NewReader` 逐个处理字节
- **Telnet 协商**: 发送 `IAC WILL ECHO`, `IAC DO/WILL SUPPRESS_GO_AHEAD`
- **ANSI 转义**: 识别 `ESC [ A/B/C/D` 方向键序列
- **行编辑**: 退格 → `\r\033[K` 清行重绘；Tab → Completer；`?` → 帮助

### 3.4 命令历史

- 线程安全的环形缓冲区 (`sync.RWMutex`)
- 容量限制，超出时移除最旧记录
- 去重：连续相同命令不重复添加
- 上下键导航，`position = -1` 表示不在历史浏览中

---

## 4. 接口与类型

### 4.1 公共 API

```go
// 核心类型
type CommandHandler func(args []string) string

type Config struct {
    Prompt     string       // 提示符
    Port       int          // 监听端口
    WelcomeMsg string       // 欢迎消息
    MaxHistory int          // 历史记录上限
    RootMode   interface{}  // 根模式（内部使用）
}

// 核心方法
func NewCmdLine(config *Config) *CmdLine
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler, detailedDescription ...string)
func (c *CmdLine) RegisterModeCommand(modePath, name, description string, handler CommandHandler, detailedDescription ...string)
func (c *CmdLine) CreateMode(modePath, description string)
func (c *CmdLine) Start() error
func (c *CmdLine) Stop()
```

### 4.2 内部接口

- `CommandNode` — Trie 节点（Name, Type, Children, Handler, 参数元数据）
- `CommandTree` — Trie 根包装器（Root + 全局 ModeCommands）
- `CommandContext` — 模式状态（CurrentMode, Path, CommandTree）
- `CommandCompleter` — 补全引擎（支持 context 和 tree 两种模式）

---

## 5. 构建与运行

```bash
# 编译
go build ./cmd/zebra_example    # → ./zebra_example (端口 2323)
go build ./examples/example     # → ./example (端口 2324)

# 连接
telnet localhost 2323
```

---

## 6. 代码审查发现

### 6.1 严重问题 (Critical)

| # | 问题 | 位置 | 影响 |
|---|------|------|------|
| C1 | **全局可变状态** `ModeCommands` | `commandtree.go:57` | 多个 CmdLine 实例会互相覆盖，不支持并发安全 |
| C2 | **类型断言无保护** | `server.go:129` | `config.RootMode.(*mode.CommandMode)` 可能 panic |
| C3 | **Session 上下文浅拷贝** | `server.go:120-125` | 多个 session 共享同一个 `CurrentMode` 指针，并发修改危险 |
| C4 | **readLine 每次创建新 bufio.Reader** | `session.go:135` | 可能丢失已缓冲但未处理的数据 |

### 6.2 设计缺陷 (Major)

| # | 问题 | 位置 | 建议 |
|---|------|------|------|
| M1 | **命令树中存在重复的参数提取逻辑** | `commandtree.go:629-643` | `isValidNumberInRange` 用正则从 Name 重新提取范围，但节点已有 `RangeMin`/`RangeMax` 字段 |
| M2 | **`__EXIT__`/`__EXIT_TO_ROOT__` 魔术字符串** | 多处 | 应使用类型安全的方式（如 Sentinel Error 或回调） |
| M3 | **`Config.RootMode` 为 `interface{}`** | `types.go:33` | 暴露了内部实现细节，破坏封装 |
| M4 | **Completer 存在三套重复的遍历逻辑** | `completer.go` | `Complete()`, `GetCompletions()`, `GetNextLevelCompletions()` 逻辑高度相似 |
| M5 | **Completer 的 `allSamePrefix` 逻辑有误** | `completer.go:84-98` | 检查所有候选项是否以第一个项为前缀，逻辑不通 |
| M6 | **缺少接口抽象** | 全局 | 所有组件都是具体结构体，无法 mock 测试 |

### 6.3 一般问题 (Minor)

| # | 问题 | 位置 |
|---|------|------|
| m1 | 大量 `fmt.Printf` 调试日志残留在生产代码中 | `cmdline.go:160-189` |
| m2 | `SimpleExample()` 死代码 | `tnlcmd.go:73-106` |
| m3 | 错误被 `_` 忽略而不处理 | `cmdline.go:79,103`, `mode.go:63` |
| m4 | `flushWriter()` 每次创建新 Writer，无实际效果 | `session.go:416-419` |
| m5 | `SetConfig` facade 方法签名不一致 | `tnlcmd.go:58` vs `cmdline.go:130` |
| m6 | README 声称的功能（左右箭头、x/term）未实现 | `README.md` |
| m7 | **零测试覆盖** | 整个项目 |
| m8 | 无连接数限制、无认证、无 TLS | `server.go` |

---

## 7. 优化建议（按优先级排序）

### P0 — 立即修复

1. **消除全局状态 `ModeCommands`**
   - 将 `ModeCommands` 移到 `CommandTree` 结构体内部作为实例字段

2. **修复 Session 上下文共享问题**
   - 为每个 Session 创建独立的 `CommandContext` 深拷贝（包括 `CurrentMode` 的独立副本）

3. **修复 `readLine` 的 bufio.Reader 重用**
   - 在 Session 结构体中保存一个 `*bufio.Reader` 实例并复用

### P1 — 短期改进

4. **引入接口抽象**
   ```go
   type CommandStore interface {
       FindCommand(args []string) (*CommandNode, []string, []string, error)
       AddCommand(name, description string, handler CommandHandler) error
   }
   ```
   便于单元测试和未来扩展

5. **消除魔术字符串**
   - 使用 `var ErrExit = errors.New("exit")` / `var ErrExitToRoot = errors.New("exit to root")` 替代

6. **清理调试日志**
   - 引入结构化日志（如 `log/slog`）替代 `fmt.Printf`，支持日志级别

7. **添加测试**
   - 优先为 `commandtree`、`history`、`completer` 添加单元测试
   - 使用 `net.Pipe()` 为 session 添加集成测试

### P2 — 中期改进

8. **安全增强**
   - 添加最大连接数限制
   - 支持基于密码的认证
   - 可选 TLS 支持

9. **功能增强**
   - 实现真正的左右箭头光标移动
   - 支持 `Ctrl+A`/`Ctrl+E` 行首行尾跳转
   - 支持命令别名

10. **配置验证**
    - 端口范围检查 (1-65535)
    - MaxHistory 合理性检查
    - 提供 `Config.Validate() error` 方法

### P3 — 长期演进

11. **协议扩展**
    - 支持 SSH（使用 `golang.org/x/crypto/ssh`）
    - 支持 WebSocket CLI

12. **可观测性**
    - 导出 Prometheus 指标（连接数、命令执行延迟）
    - 命令审计日志

13. **性能优化**
    - 对命令树使用 `sync.RWMutex` 或 `sync.Map` 支持无锁读取
    - 对象池复用 `bytes.Buffer`

---

## 8. 推荐目录结构演进

```
tnlcmd/
├── tnlcmd.go                    # 公共 API
├── pkg/
│   └── types/
│       └── types.go             # 公共类型（移除 RootMode interface{}）
├── internal/
│   ├── cmdline/
│   │   └── cmdline.go           # 编排层
│   ├── commandtree/
│   │   ├── command_tree.go      # Trie 核心
│   │   ├── parser.go            # 命令字符串解析（从 command_tree.go 拆分）
│   │   └── command_tree_test.go # 单元测试
│   ├── completer/
│   │   ├── completer.go
│   │   └── completer_test.go
│   ├── history/
│   │   ├── history.go
│   │   └── history_test.go
│   ├── mode/
│   │   ├── mode.go
│   │   └── mode_test.go
│   ├── server/
│   │   ├── server.go
│   │   ├── auth.go              # 认证中间件（新建）
│   │   └── server_test.go
│   └── session/
│       ├── session.go
│       ├── terminal.go          # 终端 I/O 处理（从 session.go 拆分）
│       └── session_test.go
├── cmd/
│   └── zebra_example/
│       └── main.go
├── examples/
│   └── example/
│       └── main.go
├── Makefile                     # 新建：build/test/lint 目标
├── go.mod
└── README.md
```

---

## 9. 总结

TNLCMD 是一个架构清晰、功能实用的 Telnet CLI 库。核心的 Trie 命令树和视图模式设计合理，成功模拟了网络设备 CLI 的交互体验。零外部依赖的特点使其非常适合嵌入式和资源受限场景。

**主要优势**:
- 分层架构清晰，包职责分明
- 零外部依赖，部署简单
- 命令树设计支持复杂的参数语法
- 视图模式系统提供真实的层级 CLI 体验

**主要短板**:
- 全局状态和浅拷贝导致并发安全隐患
- 缺少测试和接口抽象
- 魔术字符串和调试日志残留

建议按 P0 → P1 → P2 → P3 的优先级逐步优化，重点先解决并发安全和测试覆盖问题。
