package session

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync"
	"time"
)

type Session struct {
	conn       net.Conn
	mu         sync.RWMutex
	lastActive time.Time
	isClosed   bool
	history    *CommandHistory
	completer  *CommandCompleter
	prompt     string
}

func NewSession(conn net.Conn) *Session {
	s := &Session{
		conn:       conn,
		lastActive: time.Now(),
		prompt:     "cmdline> ",
	}

	s.history = NewCommandHistory(100)
	s.completer = NewCommandCompleter()

	// 启用telnet字符模式，让按键立即发送
	s.enableTelnetCharacterMode()

	return s
}

func (s *Session) Handle(ctx context.Context) error {
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

		if line == "exit" || line == "quit" {
			s.writerWrite("Goodbye!\r\n")
			return nil
		}

		s.history.Add(line)
		s.processCommand(line)
	}
}

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
				// 立即处理Tab键，不需要等待Enter
				currentInput := buffer.String()
				completions := s.completer.Complete(currentInput)

				if len(completions) == 1 {
					// 单个匹配，直接补全
					buffer.Reset()
					buffer.WriteString(completions[0])
					s.redrawLine(buffer.String())
					// 补全后不换行，光标停留在命令末尾
					// 继续等待用户输入（按Enter执行或继续编辑）
					continue
				} else if len(completions) > 1 {
					// 多个匹配，显示选项
					s.showCompletions(completions)
					// 重新显示当前输入，光标停留在当前位置
					s.redrawLine(currentInput)
					// 继续等待用户输入
					continue
				} else {
					// 没有匹配项，发出提示音
					s.writerWrite("\x07")
					s.flushWriter()
					// 继续等待用户输入
					continue
				}
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

func (s *Session) redrawLine(line string) {
	// 清除当前行并重新显示
	s.writerWrite("\r\x1b[K") // 回到行首并清除整行
	s.writerWrite(s.prompt)
	s.writerWrite(line)
	s.flushWriter()
}

func (s *Session) showCompletions(completions []string) {
	s.writerWrite("\r\n")
	for _, comp := range completions {
		s.writerWrite(comp + "\r\n")
	}
	s.flushWriter()
}

func (s *Session) writerWrite(data string) {
	s.conn.Write([]byte(data))
}

func (s *Session) flushWriter() {
	// 创建临时的writer来刷新缓冲区
	writer := bufio.NewWriter(s.conn)
	writer.Flush()
}

func (s *Session) processCommand(cmd string) {
	parts := strings.Fields(cmd)
	if len(parts) == 0 {
		return
	}

	switch parts[0] {
	case "help":
		s.showHelp()
	case "?":
		s.showHelp()
	case "history":
		s.showHistory()
	case "clear":
		s.clearScreen()
	case "echo":
		s.echoCommand(parts[1:])
	case "time":
		s.showTime()
	default:
		s.writerWrite(fmt.Sprintf("Unknown command: %s\r\n", parts[0]))
		s.writerWrite("Type 'help' for available commands\r\n")
	}
}

func (s *Session) showHelp() {
	// 逐行输出help信息，避免多行字符串的格式问题
	s.writerWrite("Available commands:\r\n")
	s.writerWrite("  help     - Show this help message\r\n")
	s.writerWrite("  history  - Show command history\r\n")
	s.writerWrite("  clear    - Clear the screen\r\n")
	s.writerWrite("  echo     - Echo arguments\r\n")
	s.writerWrite("  time     - Show current time\r\n")
	s.writerWrite("  exit/quit - Exit the session\r\n")
}

func (s *Session) showHistory() {
	history := s.history.GetAll()
	for i, cmd := range history {
		s.writerWrite(fmt.Sprintf("%d: %s\r\n", i+1, cmd))
	}
}

func (s *Session) clearScreen() {
	s.writerWrite("\x1b[2J\x1b[H")
}

func (s *Session) echoCommand(args []string) {
	if len(args) > 0 {
		s.writerWrite(strings.Join(args, " ") + "\r\n")
	} else {
		s.writerWrite("\r\n")
	}
}

func (s *Session) showTime() {
	s.writerWrite(time.Now().Format("2006-01-02 15:04:05\r\n"))
}

func (s *Session) sendWelcomeMessage() {
	// 逐行输出欢迎消息，避免多行字符串的格式问题
	s.writerWrite("Welcome to Telnet Command Line Interface!\r\n")
	s.writerWrite("Type 'help' for available commands.\r\n")
}

func (s *Session) IsStale() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return time.Since(s.lastActive) > 10*time.Minute
}

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

func (s *Session) Close() {
	s.mu.Lock()
	defer s.mu.Unlock()

	if !s.isClosed {
		s.isClosed = true
		s.conn.Close()
	}
}
