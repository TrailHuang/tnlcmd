# Telnet 命令行程序

一个功能完整的 Telnet 服务器程序，支持命令 tab 补全、历史命令管理和上下翻页功能。

## 功能特性

- ✅ **Telnet 服务器**: 支持多客户端并发连接
- ✅ **Tab 补全**: 支持命令自动补全，显示命令名称和描述
- ✅ **历史命令**: 支持命令历史记录和浏览
- ✅ **上下翻页**: 支持使用上下箭头键浏览历史命令
- ✅ **命令行编辑**: 支持左右箭头移动光标、退格删除
- ✅ **优雅关闭**: 支持信号处理和优雅关闭
- ✅ **并发安全**: 支持多客户端并发访问
- ✅ **多行描述**: 支持为命令路径的每个层级提供详细描述
- ✅ **参数验证**: 支持多种参数类型验证（枚举、范围、字符串、可选参数）
- ✅ **命令树**: 支持复杂的多级命令结构

## 编译和运行

### 编译程序

```bash
go build -o cmdline
```

### 运行服务器

```bash
# 默认端口 2323
./cmdline

# 指定端口和主机
./cmdline -port 8080 -host 127.0.0.1
```

### 连接测试

使用 telnet 客户端连接：

```bash
telnet localhost 2323
```

或者使用内置的测试客户端：

```bash
go run test_client.go localhost:2323
```

## 可用命令

- `help` - 显示帮助信息
- `history` - 显示命令历史
- `clear` - 清空屏幕
- `echo <text>` - 回显文本
- `time` - 显示当前时间
- `exit` / `quit` - 退出会话

## 键盘快捷键

- `Tab` - 命令补全
- `↑` / `↓` - 浏览历史命令
- `←` / `→` - 移动光标
- `Backspace` - 删除字符
- `Ctrl+C` / `Ctrl+D` - 退出会话

## 项目结构

```
cmdline/
├── main.go                 # 主程序入口
├── internal/
│   ├── server/
│   │   └── server.go       # Telnet 服务器实现
│   └── session/
│       ├── session.go      # 会话管理
│       ├── history.go      # 历史命令管理
│       └── completer.go    # 命令补全器
├── test_client.go         # 测试客户端
└── README.md              # 项目说明
```

## 技术实现

### 核心组件

1. **TelnetServer**: 处理网络连接和会话管理
2. **Session**: 管理单个客户端会话，处理命令输入输出
3. **CommandHistory**: 历史命令存储和检索
4. **CommandCompleter**: 命令补全功能

### 并发模型

- 使用 goroutine 处理每个客户端连接
- 使用 sync.RWMutex 保证并发安全
- 支持优雅关闭和资源清理

### 终端控制

- 使用 `golang.org/x/term` 处理终端原始模式
- 支持 ANSI 转义序列进行光标控制
- 实现完整的命令行编辑功能

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

命令补全现在显示格式化的命令名称和描述：

```
show                            - show configuration
ping                            - send echo
debug                           - debug mode
configure                       - Switch to configure mode
```

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

## 性能优化

- 连接池管理空闲会话
- 定期清理过期连接
- 缓冲区优化减少系统调用
- 内存高效的历史命令存储

## 跨平台支持

程序支持在 Linux、macOS 和 Windows 系统上运行，支持 x86 和 ARM 架构。

## 许可证

MIT License