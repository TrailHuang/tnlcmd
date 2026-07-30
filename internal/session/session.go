package session

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/TrailHuang/tnlcmd/internal/commandtree"
	"github.com/TrailHuang/tnlcmd/internal/completer"
	"github.com/TrailHuang/tnlcmd/internal/history"
	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// Session 会话结构
type Session struct {
	conn       net.Conn
	reader     *bufio.Reader
	writer     *bufio.Writer
	config     *types.Config
	commands   map[string]types.CommandInfo
	mu         sync.RWMutex
	lastActive time.Time
	isClosed   bool
	history    *history.CommandHistory
	completer  *completer.CommandCompleter
	context    *mode.CommandContext
	prompt     string

	// writeErr 记录最近一次写入错误。一旦置位，后续 writerWrite/flushWriter 直接返回该错误，
	// 主循环据此及时退出，避免向已断开的连接继续写入。
	writeErr error
}

// NewSession 创建新的会话
func NewSession(conn net.Conn, config *types.Config, commands map[string]types.CommandInfo) *Session {
	// 创建命令上下文（使用安全类型断言，RootMode 类型不匹配时回退到临时根模式）
	rootMode, ok := config.RootMode.(*mode.CommandMode)
	if !ok || rootMode == nil {
		slog.Warn("invalid RootMode type in NewSession, fallback to empty root mode", "type", fmt.Sprintf("%T", config.RootMode))
		rootMode = mode.NewCommandMode("root", config.Prompt, "fallback root mode")
	}
	context := &mode.CommandContext{
		CurrentMode: rootMode,
	}

	s := &Session{
		conn:     conn,
		reader:   bufio.NewReader(conn),
		writer:   bufio.NewWriter(conn),
		config:   config,
		commands: commands,
		context:  context,
		prompt:   config.Prompt,
	}

	s.history = history.NewCommandHistory(config.MaxHistory)
	s.completer = completer.NewCommandCompleterWithContext(s.context)

	// 启用telnet字符模式
	s.enableTelnetCharacterMode()

	return s
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
	}

	s.history = history.NewCommandHistory(config.MaxHistory)
	s.completer = completer.NewCommandCompleterWithTree(context.CommandTree)

	// 更新命令列表
	s.updateCommands()

	// 启用telnet字符模式
	s.enableTelnetCharacterMode()

	return s
}

// updateCommands 更新当前可用的命令列表
func (s *Session) updateCommands() {
	if s.context != nil {
		s.commands = s.context.GetAvailableCommands()
		s.prompt = s.context.CurrentMode.Prompt
		// 更新补全器的上下文（不再需要更新命令树，因为补全器使用上下文）
		s.completer.UpdateContext(s.context)
	} else {
		s.commands = make(map[string]types.CommandInfo)
		s.prompt = s.config.Prompt
	}
}

// Handle 处理会话
func (s *Session) Handle(ctx context.Context) error {
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
			if err == io.EOF {
				return nil
			}
			return err
		}

		s.lastActive = time.Now()

		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		s.history.Add(line)
		err = s.processCommand(line)
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

	// pending 用于缓存跨 read 批次的 Telnet IAC 序列与 ANSI ESC 序列。
	// 旧实现仅在「序列完整且位于同一批次」时才跳过，序列跨批次时后续字节会被
	// 误当作普通输入处理，导致协议状态错乱（多次交互后 ?/方向键等失灵）。
	var pending []byte

	for {
		if s.writeErr != nil {
			return "", s.writeErr
		}

		// 使用缓冲区读取，更好地处理telnet协议
		data := make([]byte, 1024)
		n, err := s.reader.Read(data)
		if err != nil {
			return "", err
		}

		if n == 0 {
			continue
		}

		// 处理接收到的数据
		for i := 0; i < n; i++ {
			b := data[i]

			// 处理telnet协议选项协商：IAC + 命令 + 选项（3字节）
			if b == 0xFF { // IAC (Interpret As Command)
				// 收集本批剩余 + pending 凑齐 3 字节
				seq := append(pending, data[i:]...)
				if len(seq) >= 3 {
					// 完整 IAC 序列，静默跳过
					pending = nil
					i += 2 // 跳过命令、选项两个字节（外层 for 还会 +1）
					continue
				}
				// 不完整，缓存等待下一批
				pending = seq
				i = n - 1 // 跳出内层 for，进入下一次 read
				break
			}

			// 若上一批遗留了不完整的 IAC，则当前字节是其延续
			if len(pending) > 0 && pending[0] == 0xFF {
				pending = append(pending, b)
				if len(pending) >= 3 {
					pending = nil // 完整，丢弃
				}
				continue
			}

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
				s.writerWrite("\r\n")
				s.flushWriter()
				return buffer.String(), nil
			case 0x1B: // Escape sequence - 可能是箭头键
				// 收集本批剩余 + pending 凑齐 ESC [ X（3字节）
				seq := append(pending, data[i:]...)
				if len(seq) >= 3 {
					pending = nil
					if seq[1] == '[' {
						switch seq[2] {
						case 'A': // Up arrow - 浏览更早的历史命令
							if s.history.Len() == 0 {
								// 没有历史命令时，保持当前输入为空
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
							i += 2 // 跳过 '['、字母（外层 for 还会 +1）
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
							i += 2 // 跳过 '['、字母
							continue
						}
					}
					// 其他 ESC 序列（非方向键）也跳过，避免污染输入
					i += 2
					continue
				}
				// 不完整，缓存等待下一批
				pending = seq
				i = n - 1
				break
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
func (s *Session) processCommand(cmd string) error {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return nil
	}

	s.mu.RLock()
	defer s.mu.RUnlock()

	// 首先检查当前视图的命令树
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
			// 处理视图切换命令
			if node.Type == types.NodeTypeModeSwitch {
				if s.context != nil && len(parts) == len(matchedPath) {
					// 查找要切换到的视图
					modeName := node.ModeName
					rootMode := s.context.GetRootMode()
					if subMode, exists := rootMode.Children[modeName]; exists {
						s.context.ChangeMode(subMode)
						s.writerWrite(fmt.Sprintf("Entering %s mode\r\n", subMode.Description))
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

				result := node.Handler(args)
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

			if s.context != nil && len(parts) == len(matchedPath) {
				modeName := parts[len(parts)-1]
				if subMode, exists := s.context.CurrentMode.Children[modeName]; exists {
					s.context.ChangeMode(subMode)
					s.writerWrite(fmt.Sprintf("Entering %s mode\r\n", subMode.Description))
					s.updateCommands()
					return nil
				}
			}
		}
	}

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
// 方向键（ESC [ X）视为"继续翻页"（返回空格的语义）。
func (s *Session) readPagingKey() (byte, error) {
	for {
		b, err := s.reader.ReadByte()
		if err != nil {
			return 0, err
		}
		switch b {
		case 0xFF: // IAC，跳过 2 字节 telnet 协商序列
			if _, err := s.reader.ReadByte(); err != nil {
				return 0, err
			}
			if _, err := s.reader.ReadByte(); err != nil {
				return 0, err
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
		return err
	}
	if err := s.writer.Flush(); err != nil {
		s.writeErr = err
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
		return err
	}
	return nil
}

// UpdatePrompt 更新会话的提示符
func (s *Session) UpdatePrompt(prompt string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.prompt = prompt

	// 如果当前有活动连接，重新显示提示符
	if s.conn != nil && !s.isClosed {
		// 清除当前行并显示新的提示符
		s.writerWrite("\r\x1b[K")
		s.writerWrite(s.prompt)
		s.flushWriter()
	}
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

	s.writer.Write(telnetCommands)
	s.writer.Flush()
}

// IsStale 检查会话是否过期
func (s *Session) IsStale() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Since(s.lastActive) > 10*time.Minute
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

// showCommandHelp 显示命令帮助（处理?键）
func (s *Session) showCommandHelp(currentInput string) {
	// 分析输入，按空格拆分
	inputParts := strings.Fields(currentInput)

	// 使用命令树进行智能提示
	if len(inputParts) == 0 {
		// 空输入，显示所有一级命令
		completions := s.completer.GetCommandTreeSuggestions("")
		if len(completions) > 0 {
			s.showCompletions(completions)
			s.redrawLine(currentInput)
		}
	} else {
		// 获取下一级补全选项
		nextLevelCompletions := s.completer.GetCommandTreeSuggestions(currentInput)
		if len(nextLevelCompletions) > 0 {
			s.showCompletions(nextLevelCompletions)
			s.redrawLine(currentInput)
		} else {
			// 没有可用命令，显示提示信息
			s.writerWrite("\r\nNo commands available\r\n")
			s.redrawLine(currentInput)
		}
	}
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
