package tnlcmd

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"sync"
	"time"
)

// Session 会话结构
type Session struct {
	conn       net.Conn
	config     *Config
	commands   map[string]CommandInfo
	mu         sync.RWMutex
	lastActive time.Time
	isClosed   bool
	history    *CommandHistory
	completer  *CommandCompleter
	context    *CommandContext
	prompt     string
}

// NewSession 创建新的会话
func NewSession(conn net.Conn, config *Config, commands map[string]CommandInfo) *Session {
	s := &Session{
		conn:       conn,
		config:     config,
		commands:   commands,
		lastActive: time.Now(),
		prompt:     config.Prompt,
	}

	s.history = NewCommandHistory(config.MaxHistory)
	s.completer = NewCommandCompleterWithContext(s.context)

	// 启用telnet字符模式
	s.enableTelnetCharacterMode()

	return s
}

// NewSessionWithContext 创建带上下文的新会话
func NewSessionWithContext(conn net.Conn, config *Config, context *CommandContext) *Session {
	s := &Session{
		conn:       conn,
		config:     config,
		lastActive: time.Now(),
		context:    context,
		prompt:     context.CurrentMode.Prompt,
	}

	s.history = NewCommandHistory(config.MaxHistory)
	s.completer = NewCommandCompleterWithTree(context.commandTree)

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
		s.completer.context = s.context
	} else {
		s.commands = make(map[string]CommandInfo)
		s.prompt = s.config.Prompt
	}
}

// Handle 处理会话
func (s *Session) Handle(ctx context.Context) error {
	// 发送欢迎消息
	s.sendWelcomeMessage()

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
		if err == io.EOF {
			return nil
		}
		if err != nil {
			// 参数验证错误等非致命错误，只记录日志，不关闭连接
			log.Printf("Command execution error: %v", err)
		}
	}
}

// readLine 读取一行输入
func (s *Session) readLine() (string, error) {
	reader := bufio.NewReader(s.conn)

	var buffer strings.Builder
	var historyIndex int = -1

	// 显示初始提示符
	s.writerWrite(s.prompt)
	s.flushWriter()

	for {
		// 使用缓冲区读取，更好地处理telnet协议
		data := make([]byte, 1024)
		n, err := reader.Read(data)
		if err != nil {
			return "", err
		}

		if n == 0 {
			continue
		}

		// 处理接收到的数据
		for i := 0; i < n; i++ {
			b := data[i]

			// 处理telnet协议选项协商
			if b == 0xFF { // IAC (Interpret As Command)
				// 跳过telnet命令序列（3字节）
				if i+2 < n {
					i += 2
					continue
				}
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
				// 立即处理?键，显示当前可用的命令选项
				currentInput := buffer.String()

				// 分析输入，按空格拆分
				inputParts := strings.Fields(currentInput)

				// 首先尝试使用命令树进行智能提示（如果可用）
				if s.context != nil && s.context.commandTree != nil {
					node := s.context.commandTree.Root

					// 遍历到当前层级
					for i := 0; i < len(inputParts); i++ {
						if child, exists := node.Children[inputParts[i]]; exists {
							node = child
						} else {
							// 找不到匹配节点，使用默认提示
							node = nil
							break
						}
					}

					if node != nil {
						// 显示当前节点的所有子节点（包括参数节点）
						var suggestions []string
						for name := range node.Children {
							suggestions = append(suggestions, name)
						}

						if len(suggestions) > 0 {
							s.showCompletions(suggestions)
							s.redrawLine(currentInput)
							continue
						}
					}
				}

				// 向后兼容：使用旧的补全逻辑
				if len(inputParts) == 0 {
					// 空输入，显示所有一级命令
					completions := s.completer.Complete("")
					if len(completions) > 0 {
						s.showCompletions(completions)
						s.redrawLine(currentInput)
					}
				} else {
					// 获取下一级补全选项
					nextLevelCompletions := s.completer.GetNextLevelCompletions(currentInput)
					if len(nextLevelCompletions) > 0 {
						s.showCompletions(nextLevelCompletions)
						s.redrawLine(currentInput)
					} else {
						// 没有下一级选项，显示当前可用的完整命令
						completions := s.completer.GetCommandSuggestions(currentInput)
						if len(completions) > 0 {
							s.showCompletions(completions)
							s.redrawLine(currentInput)
						} else {
							// 没有可用命令，显示提示信息
							s.writerWrite("\r\nNo commands available\r\n")
							s.redrawLine(currentInput)
						}
					}
				}

				// 继续等待用户输入
				continue
			case 0x0D, 0x0A: // Enter
				s.writerWrite("\r\n")
				s.flushWriter()
				return buffer.String(), nil
			case 0x1B: // Escape sequence - 可能是箭头键
				// 检查是否有足够的字节用于转义序列
				if i+2 < n {
					if data[i+1] == '[' {
						switch data[i+2] {
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
							i += 2 // 跳过已处理的转义序列字节
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
							i += 2 // 跳过已处理的转义序列字节
							continue
						}
					}
				}
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
	if s.context != nil && s.context.CurrentMode != nil && s.context.CurrentMode.commandTree != nil {
		node, matchedPath, err := s.context.CurrentMode.commandTree.FindCommand(parts)
		if err == nil && node != nil {
			// 处理视图切换命令
			if node.Type == NodeTypeModeSwitch {
				if s.context != nil && len(parts) == len(matchedPath) {
					// 查找要切换到的视图
					modeName := node.ModeName
					rootMode := s.context.getRootMode()
					if subMode, exists := rootMode.Children[modeName]; exists {
						s.context.ChangeMode(subMode)
						s.writerWrite(fmt.Sprintf("Entering %s mode\r\n", subMode.Description))
						s.updateCommands()
						return nil
					}
				}
			}

			if node.Handler != nil {
				args := parts[len(matchedPath):]
				if err := s.validateCommandParameters(node, matchedPath, args); err != nil {
					return err
				}

				writer := bufio.NewWriter(s.conn)
				err := node.Handler(args, writer)
				writer.Flush()

				s.updateCommands()
				return err
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

	// 如果在当前视图的命令树中找不到命令，且当前不是根视图，尝试在根视图的命令树中查找视图切换命令
	if s.context != nil && s.context.CurrentMode != nil && s.context.CurrentMode.Parent != nil {
		rootMode := s.context.getRootMode()
		if rootMode.commandTree != nil {
			node, matchedPath, err := rootMode.commandTree.FindCommand(parts)
			if err == nil && node != nil && node.Type == NodeTypeModeSwitch {
				// 处理视图切换命令
				if len(parts) == len(matchedPath) {
					modeName := node.ModeName
					if subMode, exists := rootMode.Children[modeName]; exists {
						s.context.ChangeMode(subMode)
						s.writerWrite(fmt.Sprintf("Entering %s mode\r\n", subMode.Description))
						s.updateCommands()
						return nil
					}
				}
			}
		}
	}

	// 如果在命令树中找不到命令，尝试在平面命令存储中查找（向后兼容）
	if len(parts) == 1 {
		cmdName := parts[0]
		if cmdInfo, exists := s.commands[cmdName]; exists {
			// 执行平面命令存储中的命令
			writer := bufio.NewWriter(s.conn)
			err := cmdInfo.Handler(nil, writer)
			writer.Flush()

			s.updateCommands()
			return err
		}
	}

	s.writerWrite(fmt.Sprintf("Unknown command: %s\r\n", strings.Join(parts, " ")))
	s.writerWrite("Type 'help' for available commands\r\n")
	return nil
}

// validateCommandParameters 验证命令参数数量是否正确
func (s *Session) validateCommandParameters(node *CommandNode, matchedPath []string, args []string) error {
	// 计算命令需要的参数数量
	requiredParams := 0
	optionalParams := 0

	// 从匹配的节点开始，遍历到叶子节点，统计参数数量
	current := node
	for current != nil {
		if current.Type != NodeTypeCommand {
			// 参数节点
			if current.IsRequired {
				requiredParams++
			} else {
				optionalParams++
			}
		}
		// 移动到下一个节点（如果有子节点）
		if len(current.Children) > 0 {
			// 取第一个子节点继续遍历
			for _, child := range current.Children {
				current = child
				break
			}
		} else {
			current = nil
		}
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

	return nil
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

// writerWrite 写入数据
func (s *Session) writerWrite(data string) {
	s.conn.Write([]byte(data))
}

// flushWriter 刷新写入器
func (s *Session) flushWriter() {
	// 创建临时的writer来刷新缓冲区
	writer := bufio.NewWriter(s.conn)
	writer.Flush()
}

// sendWelcomeMessage 发送欢迎消息
func (s *Session) sendWelcomeMessage() {
	s.writerWrite(s.config.WelcomeMsg)
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

	s.conn.Write(telnetCommands)
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
		completions := s.completer.GetRootCommands()
		if len(completions) > 0 {
			s.showCompletions(completions)
			s.flushWriter()
			s.redrawLine("")
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

// Close 关闭会话
func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isClosed {
		s.isClosed = true
		s.conn.Close()
	}
}
