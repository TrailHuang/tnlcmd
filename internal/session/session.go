package session

import (
	"bufio"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/internal/completer"
	"github.com/TrailHuang/tnlcmd/internal/debuglog"
	"github.com/TrailHuang/tnlcmd/internal/history"
	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// readBufSize 为 readLine 单次读取的批大小。
// 该缓冲在会话生命周期内复用，避免每轮读取都分配新切片。
const readBufSize = 1024

// Session 会话结构
type Session struct {
	conn       net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	config     *types.Config
	mu         sync.RWMutex
	lastActive time.Time
	isClosed   bool
	history    *history.CommandHistory
	completer  *completer.CommandCompleter
	context    *mode.CommandContext
	prompt     string

	// readBuf 为 readLine 的读取缓冲，会话创建时分配一次并在整个会话期间复用。
	// 仅由 Handle→readLine 所在的单一 goroutine 访问，无需额外加锁。
	readBuf []byte

	// writeErr 记录最近一次写入错误。一旦置位，后续 writerWrite/flushWriter 直接返回该错误，
	// 主循环据此及时退出，避免向已断开的连接继续写入。
	writeErr error
}

// NewSessionWithContext 使用现有上下文创建新的会话
func NewSessionWithContext(conn net.Conn, config *types.Config, context *mode.CommandContext) *Session {
	s := &Session{
		conn:       conn,
		reader:     bufio.NewReader(conn),
		writer:     bufio.NewWriter(conn),
		config:     config,
		context:    context,
		lastActive: time.Now(),
		prompt:     config.Prompt,
		readBuf:    make([]byte, readBufSize),
	}

	s.history = history.NewCommandHistory(config.MaxHistory)
	s.completer = completer.NewCommandCompleterWithContext(context)

	// 更新命令列表
	s.updateCommands()

	if debuglog.Enabled() {
		debuglog.Debug("session", "session_created",
			"remote", conn.RemoteAddr().String(),
			"local", conn.LocalAddr().String(),
			"prompt", s.prompt,
			"mode", s.context.CurrentMode.Name,
		)
	}

	return s
}

// updateCommands 更新提示符与补全器上下文。
// 写操作持写锁，与外部 goroutine 的 UpdatePrompt 互斥；
// 主循环内调用方（processCommand / 初始化）已持有 RLock 时依赖 Go 的 RWLock 不可重入语义，
// 故本方法不得在持有任何锁的状态下调用。
func (s *Session) updateCommands() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.context != nil {
		s.prompt = s.context.CurrentMode.Prompt
		s.completer.UpdateContext(s.context)
	} else {
		s.prompt = s.config.Prompt
	}
}

// Handle 处理会话
func (s *Session) Handle(ctx context.Context) error {
	// 启用telnet字符模式
	s.enableTelnetCharacterMode()

	// 发送欢迎消息
	if err := s.sendWelcomeMessage(); err != nil {
		return err
	}

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		line, err := s.readLine()
		if err != nil {
			if debuglog.Enabled() {
				debuglog.Debug("session", "readline_ended",
					"remote", s.remoteStr(),
					"err", err,
					"goroutines", runtime.NumGoroutine())
			}
			if err == io.EOF {
				return nil
			}
			return err
		}

		s.mu.Lock()
		s.lastActive = time.Now()
		s.mu.Unlock()

		if debuglog.Enabled() {
			debuglog.Debug("session", "command_line_received",
				"remote", s.remoteStr(),
				"line", line,
				"goroutines", runtime.NumGoroutine())
		}

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		s.history.Add(line)
		err = s.processCommand(ctx, line)
		if debuglog.Enabled() {
			errStr := ""
			if err != nil {
				errStr = err.Error()
			}
			debuglog.Debug("session", "command_processed",
				"remote", s.remoteStr(),
				"line", line,
				"err", errStr,
				"goroutines", runtime.NumGoroutine())
		}
		// 写入失败（如客户端断开）→ 立即退出会话，避免向死连接继续写入
		if s.writeErr != nil {
			return s.writeErr
		}
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// 参数验证错误等非致命错误，只记录日志，不关闭连接
			slog.Error("command execution error", "error", err)
		}
	}
}

// inputState 标识输入解析状态机的当前状态，用于正确处理跨 read 批次的
// Telnet IAC 序列与 ANSI ESC 方向键序列。
//
// 关键修复：Telnet IAC 命令长度不固定——NOP/GA/AYT/EC 等为 2 字节，
// WILL/WONT/DO/DONT 为 3 字节。旧实现（及 pending 缓冲方案）假设恒为 3 字节，
// 导致 2 字节 IAC 命令后的第一个用户字节（常是 ?）被误当作第三字节吞掉。
type inputState uint8

const (
	stNormal     inputState = iota // 正常输入处理
	stIAC                          // 收到 IAC(0xFF)，等待命令字节
	stIACOpt                       // 收到 WILL/WONT/DO/DONT，等待选项字节（第三字节）
	stSB                           // 子协商数据流中，等待 IAC SE
	stSBIAC                        // 子协商中收到 IAC，等待 SE 或继续
	stESC                          // 收到 ESC(0x1B)，等待 '['
	stESCBracket                   // 收到 ESC [，等待方向键字母
	stUTF8Q1                       // 收到 0xEF（UTF-8 多字节首字节，可能为全角问号），等待 0xBC
	stUTF8Q2                       // 收到 0xEF 0xBC，等待 0x9F 完成全角问号
)

// isIAC2ByteCmd 判断 IAC 命令字节是否为 2 字节序列（无选项字节）。
// 0xF1-0xF9: NOP/DM/BRK/IP/AO/AYT/EC/EL/GA
// 0xFF: IAC IAC（转义的 0xFF）
func isIAC2ByteCmd(cmd byte) bool {
	return cmd == 0xFF || (cmd >= 0xF1 && cmd <= 0xF9)
}

// isIAC3ByteCmd 判断 IAC 命令字节是否为 3 字节序列（带选项字节）。
// 0xFB-0xFE: WILL/WONT/DO/DONT
func isIAC3ByteCmd(cmd byte) bool {
	return cmd >= 0xFB && cmd <= 0xFE
}

// readLine 读取一行输入
func (s *Session) readLine() (string, error) {
	var buffer strings.Builder
	var historyIndex int = -1

	if s.writeErr != nil {
		return "", s.writeErr
	}

	// 显示初始提示符
	if err := s.writerWrite(s.prompt); err != nil {
		return "", err
	}
	if err := s.flushWriter(); err != nil {
		return "", err
	}

	// 状态机：跨 read 批次保持解析状态，正确处理变长 IAC / ESC 序列
	ps := stNormal

	for {
		if s.writeErr != nil {
			return "", s.writeErr
		}

		// 复用会话级读缓冲读取，避免每轮分配；data 仅在本次循环内有效
		n, err := s.reader.Read(s.readBuf)
		if err != nil {
			if debuglog.Enabled() {
				debuglog.Debug("session", "read_error",
					"remote", s.remoteStr(),
					"n", n,
					"state", ps,
					"err", err)
			}
			return "", err
		}

		if n == 0 {
			continue
		}

		data := s.readBuf[:n]

		if debuglog.Enabled() {
			debuglog.Debug("session", "input_received",
				"remote", s.remoteStr(),
				"n", n,
				"state", ps,
				"buffer_len", buffer.Len(),
				"first_byte_hex", hex.EncodeToString(data[:1]),
				"prefix_hex", hex.EncodeToString(data[:min(n, 8)]))
		}

		// 处理接收到的数据
		for i := 0; i < n; i++ {
			b := data[i]

			// 优先处理多字节序列状态机
			switch ps {
			case stIAC:
				// b 是 IAC 命令字节
				switch {
				case isIAC3ByteCmd(b): // WILL/WONT/DO/DONT → 还需选项字节
					ps = stIACOpt
				case b == 0xFA: // SB → 子协商开始
					ps = stSB
				default: // 2 字节命令（NOP/GA/AYT/EC/IAC-IAC 等）→ 完成
					ps = stNormal
				}
				continue
			case stIACOpt:
				// b 是选项字节，序列完成，丢弃
				ps = stNormal
				continue
			case stSB:
				// 子协商数据流，跳过直到 IAC
				if b == 0xFF {
					ps = stSBIAC
				}
				continue
			case stSBIAC:
				// 子协商中的 IAC，b==0xF0(SE) 结束，否则继续
				if b == 0xF0 {
					ps = stNormal
				} else {
					ps = stSB
				}
				continue
			case stESC:
				if b == '[' {
					ps = stESCBracket
				} else {
					ps = stNormal // 非 '[' 的 ESC 序列，忽略
				}
				continue
			case stESCBracket:
				ps = stNormal
				switch b {
				case 'A': // Up arrow - 浏览更早的历史命令
					if s.history.Len() == 0 {
						buffer.Reset()
						s.redrawLine("")
					} else {
						if historyIndex < 0 {
							historyIndex = s.history.Len() - 1
						} else if historyIndex > 0 {
							historyIndex--
						}
						cmd := s.history.Get(historyIndex)
						buffer.Reset()
						buffer.WriteString(cmd)
						s.redrawLine(buffer.String())
					}
					continue
				case 'B': // Down arrow - 浏览更新的历史命令
					if historyIndex >= 0 && historyIndex < s.history.Len()-1 {
						historyIndex++
						cmd := s.history.Get(historyIndex)
						buffer.Reset()
						buffer.WriteString(cmd)
						s.redrawLine(buffer.String())
					} else if historyIndex == s.history.Len()-1 {
						historyIndex = -1
						buffer.Reset()
						s.redrawLine("")
					}
					continue
				default:
					// 其他 ESC [ X 序列，忽略
					continue
				}
			case stUTF8Q1:
				// 0xEF 后应为 0xBC 才可能是全角问号；否则恢复正常处理
				if b == 0xBC {
					ps = stUTF8Q2
				} else {
					ps = stNormal
				}
				continue
			case stUTF8Q2:
				// 0xEF 0xBC 0x9F = 全角问号 '？'（U+FF1F）→ 触发帮助
				ps = stNormal
				if b == 0x9F {
					currentInput := buffer.String()
					s.showCommandHelp(currentInput)
				}
				continue
			}

			// stNormal：检测多字节序列开头
			if b == 0xFF { // IAC
				ps = stIAC
				continue
			}
			if b == 0x1B { // ESC
				ps = stESC
				continue
			}
			if b == 0xEF { // UTF-8 多字节序列（如全角问号 '？' = EF BC 9F）
				ps = stUTF8Q1
				continue
			}

			// 单字节命令与可打印字符
			switch b {
			case 0x03: // Ctrl+C
				return "", io.EOF
			case 0x04: // Ctrl+D
				return "", io.EOF
			case 0x7F, 0x08: // Backspace
				if buffer.Len() > 0 {
					current := buffer.String()
					buffer.Reset()
					buffer.WriteString(current[:len(current)-1])
					s.redrawLine(buffer.String())
				}
			case 0x09: // Tab - 命令补全
				if !s.handleTabCompletion(&buffer) {
					continue
				}
			case 0x3F: // ? - 显示命令提示
				currentInput := buffer.String()
				s.showCommandHelp(currentInput)
				continue

			case 0x0D, 0x0A: // Enter
				if debuglog.Enabled() {
					debuglog.Debug("session", "input_line_ready",
						"remote", s.remoteStr(),
						"line", buffer.String(),
						"len", buffer.Len())
				}
				s.writerWrite("\r\n")
				s.flushWriter()
				return buffer.String(), nil
			default:
				if b >= 0x20 && b <= 0x7E {
					buffer.WriteByte(b)
					s.writerWrite(string([]byte{b}))
					s.flushWriter()
				}
			}
		}
	}
}

// processCommand 处理命令
func (s *Session) processCommand(ctx context.Context, cmd string) error {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	// 仅在读取 context/mode 结构时持读锁，写锁操作（updateCommands）在锁外执行，
	// 避免 RLock 下调用加写锁的方法导致死锁。
	s.mu.RLock()
	if s.context != nil && s.context.CurrentMode != nil && s.context.CurrentMode.CommandTree != nil {
		node, matchedPath, args, err := s.context.CurrentMode.CommandTree.FindCommand(parts)

		// 回退：如果当前模式树找不到，尝试根模式树（用于跨模式切换）
		if err != nil && len(parts) == 1 {
			rootMode := s.context.GetRootMode()
			if rootMode != nil && rootMode.CommandTree != nil {
				if fallbackNode, fbPath, fbArgs, fbErr := rootMode.CommandTree.FindCommand(parts); fbErr == nil && fallbackNode != nil && fallbackNode.Type == types.NodeTypeModeSwitch {
					node = fallbackNode
					matchedPath = fbPath
					args = fbArgs
					err = nil
				}
			}
		}

		if err == nil && node != nil {
			s.mu.RUnlock()

			// 处理视图切换命令（切换语义统一委托 CommandContext.EnterMode）
			if node.Type == types.NodeTypeModeSwitch {
				if s.context != nil && len(parts) == len(matchedPath) {
					// 查找要切换到的视图
					modeName := node.ModeName
					rootMode := s.context.GetRootMode()
					if subMode, exists := rootMode.Children[modeName]; exists {
						s.writerWrite(s.context.EnterMode(subMode))
						s.updateCommands()
						return nil
					}
				}
			}

			if node.Handler != nil {
				//args := parts[len(matchedPath):]
				if err := s.validateCommandParameters(node, matchedPath, args); err != nil {
					return err
				}

				result := node.Handler(ctx, args)
				if result != "" {
					// 检查是否为退出命令的特殊标记
					if result == types.SentinelExit {
						s.writerWrite("Goodbye!\r\n")
						s.flushWriter()
						return io.EOF
					}

					// 检查是否为退出到根模式的特殊标记
					if result == types.SentinelExitToRoot {
						s.writerWrite("Exiting to privileged EXEC mode\r\n")
						rootMode := s.context.GetRootMode()
						s.context.ChangeMode(rootMode)
						s.updateCommands()
						return nil
					}

					// 输出命令结果（按 PageSize 分页，若启用）
					if err := s.writeResult(result); err != nil {
						return err
					}
				}

				s.updateCommands()
				return nil
			}

			// 如果没有 handler 但有匹配的节点，说明是中间节点（子命令前缀）
			// 显示该节点下的子命令选项
			if s.context != nil && node != nil {
				// 检查是否有子节点可以切换到子模式（切换语义统一委托 EnterMode）
				if len(parts) == len(matchedPath) {
					modeName := parts[len(parts)-1]
					if subMode, exists := s.context.CurrentMode.Children[modeName]; exists {
						s.writerWrite(s.context.EnterMode(subMode))
						s.updateCommands()
						return nil
					}
				}
				// 未输入完整命令，按设备规范提示不完整
				s.writerWrite(fmt.Sprintf("%% Incomplete command. \r\n"))
				return nil
			}
			return nil
		} else if err != nil && errors.Is(err, types.ErrIncompleteCommand) {
			s.mu.RUnlock()
			// FindCommand 返回 incomplete command，说明输入的命令不完整
			// 按设备规范仅提示 % Incomplete command，使用 ? 查看候选
			s.writerWrite(fmt.Sprintf("%% Incomplete command. Use '?' to view options.\r\n"))
			return nil
		}
	}
	s.mu.RUnlock()

	s.writerWrite(fmt.Sprintf("Unknown command: %s\r\n", strings.Join(parts, " ")))
	s.writerWrite("Type '?' for available commands\r\n")
	return nil
}

// writeResult 写入命令处理结果。当 PageSize > 0 时按行分页，每页之间显示
// "--More-- (NN%)" 提示并等待用户按键：Space/Enter/方向键 → 下一页；
// 'q'/'Q' → 中止剩余输出（回到提示符）；Ctrl+C → 中止并关闭连接。
// 写入失败时返回错误，由调用方退出会话。
func (s *Session) writeResult(result string) error {
	normalized := normalizeLineEndings(result)

	// 禁用分页：一次性输出全部内容
	if s.config.PageSize <= 0 {
		return s.writerWrite(normalized)
	}

	// 分页输出：按 \r\n 拆分，逐行写入，每 PageSize 行插入一次 --More--
	lines := strings.Split(normalized, "\r\n")
	hasTrailingNL := strings.HasSuffix(normalized, "\r\n")
	// strings.Split 对 "a\r\n" 返回 ["a",""]，去掉末尾空串以便正确统计行数
	if hasTrailingNL && len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	if len(lines) == 0 {
		return nil
	}

	page := s.config.PageSize
	for idx, line := range lines {
		isLastLine := idx == len(lines)-1

		if err := s.writerWrite(line); err != nil {
			return err
		}
		// 行尾分隔符：除非这是最后一行且原文无尾随换行
		if !(isLastLine && !hasTrailingNL) {
			if err := s.writerWrite("\r\n"); err != nil {
				return err
			}
		}

		// 每页末尾且非最后一行 → 显示 --More-- 并等待按键
		if (idx+1)%page == 0 && !isLastLine {
			percent := (idx + 1) * 100 / len(lines)
			if err := s.writerWrite(fmt.Sprintf("  --More-- (%d%%)", percent)); err != nil {
				return err
			}
			key, err := s.readPagingKey()
			// 无论用户按什么，先清除 --More-- 行
			if werr := s.writerWrite("\r\x1b[K"); werr != nil {
				return werr
			}
			if err != nil {
				return err
			}
			switch key {
			case 'q', 'Q':
				// 中止剩余输出，回到提示符
				return nil
			case 0x03: // Ctrl+C → 中止并关闭连接
				return io.EOF
			}
			// Space / Enter / 方向键 / 其他 → 继续下一页
		}
	}
	return nil
}

// readPagingKey 在分页提示后读取用户的一个按键，跳过 Telnet IAC 序列与 ANSI 方向键序列。
// IAC 处理规则与 readLine 主状态机保持一致：
//   - IAC + 0xF1-0xF9/0xFF（NOP/GA/AYT/EC/EL/IAC-IAC 等）为 2 字节序列，仅再读 1 字节
//   - IAC + 0xFB-0xFE（WILL/WONT/DO/DONT）为 3 字节序列，再读命令与选项 2 字节
//   - IAC SB(0xFA) 进入子协商，跳过至 IAC SE(0xF0)
//
// 方向键（ESC [ X）视为"继续翻页"（返回空格的语义）。
func (s *Session) readPagingKey() (byte, error) {
	for {
		b, err := s.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case 0xFF: // IAC
			cmd, err := s.reader.ReadByte()
			if err != nil {
				return 0, err
			}
			switch {
			case cmd == 0xFA: // SB → 子协商，跳过直到 IAC SE
				for {
					sb, err := s.reader.ReadByte()
					if err != nil {
						return 0, err
					}
					if sb != 0xFF {
						continue
					}
					iac, err := s.reader.ReadByte()
					if err != nil {
						return 0, err
					}
					if iac == 0xF0 { // SE，子协商结束
						break
					}
				}
			case cmd == 0xFF || (cmd >= 0xF1 && cmd <= 0xF9): // 2 字节命令，无选项字节
			case cmd >= 0xFB && cmd <= 0xFE: // WILL/WONT/DO/DONT → 还需选项字节
				if _, err := s.reader.ReadByte(); err != nil {
					return 0, err
				}
			}
			continue
		case 0x1B: // ESC，可能是方向键序列 ESC [ X
			if _, err := s.reader.ReadByte(); err != nil { // 读取 '['
				return 0, err
			}
			if _, err := s.reader.ReadByte(); err != nil { // 读取字母
				return 0, err
			}
			return ' ', nil // 方向键 → 继续翻页
		default:
			return b, nil
		}
	}
}

// validateCommandParameters 验证命令参数数量和值是否正确
func (s *Session) validateCommandParameters(node *commandtree.CommandNode, matchedPath []string, args []string) error {
	// 计算命令需要的参数数量
	requiredParams := 0
	optionalParams := 0

	// 收集路径上的所有参数节点
	var paramNodes []*commandtree.CommandNode
	current := node
	for current != nil {
		if current.Type != types.NodeTypeCommand {
			// 参数节点
			paramNodes = append([]*commandtree.CommandNode{current}, paramNodes...) // 插入到开头，保持顺序
			if current.IsRequired {
				requiredParams++
			} else {
				optionalParams++
			}
		}
		// 移动到父节点
		current = current.Parent
	}

	// 验证参数数量
	if len(args) < requiredParams {
		s.writerWrite(fmt.Sprintf("Error: Too few arguments for command '%s'\r\n", strings.Join(matchedPath, " ")))
		s.writerWrite(fmt.Sprintf("Expected at least %d arguments, got %d\r\n", requiredParams, len(args)))
		return fmt.Errorf("insufficient arguments")
	}

	if len(args) > requiredParams+optionalParams {
		s.writerWrite(fmt.Sprintf("Error: Too many arguments for command '%s'\r\n", strings.Join(matchedPath, " ")))
		s.writerWrite(fmt.Sprintf("Expected at most %d arguments, got %d\r\n", requiredParams+optionalParams, len(args)))
		return fmt.Errorf("too many arguments")
	}

	// 验证参数值的合法性
	for i, arg := range args {
		if i < len(paramNodes) {
			paramNode := paramNodes[i]

			// 使用参数验证函数检查参数值
			if !commandtree.IsParameterMatch(paramNode, arg) {
				// 获取具体的验证错误信息
				errorMsg := s.getParameterValidationError(paramNode, arg)
				s.writerWrite(fmt.Sprintf("Error: Invalid parameter value for command '%s'\r\n", strings.Join(matchedPath, " ")))
				s.writerWrite(fmt.Sprintf("Parameter %d: %s\r\n", i+1, errorMsg))
				return fmt.Errorf("invalid parameter value")
			}
		}
	}

	return nil
}

// getParameterValidationError 获取参数验证错误信息
func (s *Session) getParameterValidationError(node *commandtree.CommandNode, input string) string {
	switch node.Type {
	case types.NodeTypeEnum:
		return commandtree.GetEnumValidationError(node, input)
	case types.NodeTypeNum:
		return commandtree.GetNumberValidationError(node, input)
	default:
		return fmt.Sprintf("无效的参数值: '%s'", input)
	}
}

// redrawLine 重绘当前行
func (s *Session) redrawLine(line string) {
	// 清除当前行并重新显示
	s.writerWrite("\r\x1b[K") // 回到行首并清除整行
	s.writerWrite(s.prompt)
	s.writerWrite(line)
	s.flushWriter()
}

// showCompletions 显示补全选项
func (s *Session) showCompletions(completions []string) {
	s.writerWrite("\r\n")
	for _, comp := range completions {
		s.writerWrite(comp + "\r\n")
	}
	s.flushWriter()
}

// writerWrite 写入数据（自动 flush 以确保即时发送，匹配旧版直接 conn.Write 的行为）。
// 返回写入/flush 过程中的错误，并记入 s.writeErr 使主循环能及时退出。
func (s *Session) writerWrite(data string) error {
	if s.writeErr != nil {
		return s.writeErr
	}
	if _, err := s.writer.WriteString(data); err != nil {
		s.writeErr = err
		if debuglog.Enabled() {
			debuglog.Debug("session", "write_error",
				"remote", s.remoteStr(),
				"err", err,
				"data_len", len(data))
		}
		return err
	}
	if err := s.writer.Flush(); err != nil {
		s.writeErr = err
		if debuglog.Enabled() {
			debuglog.Debug("session", "flush_error",
				"remote", s.remoteStr(),
				"err", err)
		}
		return err
	}
	return nil
}

// flushWriter 刷新写入缓冲区
func (s *Session) flushWriter() error {
	if s.writeErr != nil {
		return s.writeErr
	}
	if err := s.writer.Flush(); err != nil {
		s.writeErr = err
		if debuglog.Enabled() {
			debuglog.Debug("session", "flush_error",
				"remote", s.remoteStr(),
				"err", err)
		}
		return err
	}
	return nil
}

// UpdatePrompt 更新会话的提示符。
// 会话主循环是 bufio.Writer 与 writeErr 的唯一写入者，因此这里只更新字段，
// 由主循环在下一轮 readLine 时绘制新提示符；绝不在此处直接写连接（会与主循环并发写）。
func (s *Session) UpdatePrompt(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.prompt = prompt
}

// normalizeLineEndings 规范化换行符，确保使用 \r\n
func normalizeLineEndings(text string) string {
	// 如果不含 \n，无需处理
	if !strings.Contains(text, "\n") {
		return text
	}

	// 先统一为 \n，再替换为 \r\n，避免重复替换
	result := strings.ReplaceAll(text, "\r\n", "\n")
	result = strings.ReplaceAll(result, "\n", "\r\n")

	return result
}

// sendWelcomeMessage 发送欢迎消息
func (s *Session) sendWelcomeMessage() error {
	return s.writerWrite(s.config.WelcomeMsg)
}

// remoteStr 返回对端地址字符串，连接为 nil 或地址不可用时返回占位符。
func (s *Session) remoteStr() string {
	if s.conn != nil {
		if addr := s.conn.RemoteAddr(); addr != nil {
			return addr.String()
		}
	}
	return "unknown"
}

// enableTelnetCharacterMode 启用telnet字符模式
func (s *Session) enableTelnetCharacterMode() {
	// Telnet选项协商命令
	// IAC WILL ECHO: 告诉客户端我们将处理回显
	// IAC DO SUPPRESS_GO_AHEAD: 禁用前进抑制
	// IAC WILL SUPPRESS_GO_AHEAD: 禁用前进抑制

	telnetCommands := []byte{
		0xFF, 0xFB, 0x01, // IAC WILL ECHO
		0xFF, 0xFD, 0x03, // IAC DO SUPPRESS_GO_AHEAD
		0xFF, 0xFB, 0x03, // IAC WILL SUPPRESS_GO_AHEAD
	}

	if debuglog.Enabled() {
		debuglog.Debug("session", "telnet_negotiation_sent",
			"data_hex", hex.EncodeToString(telnetCommands))
	}

	s.writer.Write(telnetCommands)
	s.writer.Flush()
}

// handleTabCompletion 处理Tab键补全
func (s *Session) handleTabCompletion(buffer *strings.Builder) bool {
	currentInput := buffer.String()
	inputParts := strings.Fields(currentInput)

	if len(inputParts) == 0 {
		suggestions := s.completer.GetCommandTreeSuggestions(currentInput)
		if len(suggestions) > 0 {
			s.showCompletions(suggestions)
			s.redrawLine(currentInput)
		}
		return false
	}

	nextLevelCompletions := s.completer.GetNextLevelCompletions(currentInput)

	switch len(nextLevelCompletions) {
	case 0:
		paramCompletions := s.completer.GetParameterCompletions(currentInput)
		if len(paramCompletions) > 0 {
			s.showCompletions(paramCompletions)
			s.flushWriter()
			s.redrawLine(buffer.String())
		} else {
			s.writerWrite("\x07")
			s.flushWriter()
		}
	case 1:
		buffer.Reset()
		buffer.WriteString(nextLevelCompletions[0])
		s.redrawLine(buffer.String())
	default:
		s.showCompletions(nextLevelCompletions)
		s.flushWriter()
		s.redrawLine(buffer.String())
	}

	return true
}

// showCommandHelp 显示命令帮助（处理?键），遵循网络设备分级语义：
//   - 空输入 ?：显示当前视图所有一级命令
//   - 已输入关键字 ?：显示该关键字的下一级候选（子命令或参数）
//   - 命令已完整且无更多参数：提示 <cr> 表示可直接回车执行
func (s *Session) showCommandHelp(currentInput string) {
	inputParts := strings.Fields(currentInput)
	suggestions := s.completer.GetCommandTreeSuggestions(currentInput)
	if len(suggestions) > 0 {
		s.showCompletions(suggestions)
	} else if len(inputParts) > 0 {
		// 输入已是完整命令且无更多可输入项，提示回车执行
		s.writerWrite("\r\n")
		s.writerWrite("<cr>\r\n")
	} else {
		s.writerWrite("\r\nNo commands available\r\n")
	}
	// 重新绘制当前输入行，使光标回到行尾
	s.redrawLine(currentInput)
}

// Close 关闭会话
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isClosed {
		s.isClosed = true
		s.conn.Close()
	}
}

// IsStale 判断会话空闲时长是否已超过 threshold（自最近一次成功读取输入行起算）。
// 由服务端空闲回收 goroutine 周期性调用；关闭连接（Close）会使阻塞在
// readLine 中的会话读取立即出错返回，从而触发会话退出与资源清理。
func (s *Session) IsStale(threshold time.Duration) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Since(s.lastActive) >= threshold
}
