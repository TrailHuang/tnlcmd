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
	conn        net.Conn
	reader      *bufio.Reader
	writer      *bufio.Writer
	config      *types.Config
	mu          sync.RWMutex
	lastActive  time.Time
	isClosed    bool
	history     *history.CommandHistory
	completer   *completer.CommandCompleter
	context     *mode.CommandContext
	prompt      string
	auditLogger *slog.Logger // 审计专用 logger；由服务器在 Handle 前注入，nil 表示不审计

	// readBuf 为 readLine 的读取缓冲，会话创建时分配一次并在整个会话期间复用。
	// 仅由 Handle→readLine 所在的单一 goroutine 访问，无需额外加锁。
	readBuf []byte

	// inputFilter/pendingInput 为三条读取路径共用的输入过滤状态与未消费字节。
	// editor 为会话级行编辑缓冲，避免每行重新分配。
	inputFilter  telnetInput
	pendingInput []byte
	editor       lineEditor

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

// maxLineLength 返回本会话的单行输入字节上限（0 或未配置时使用默认值）。
func (s *Session) maxLineLength() int {
	if s.config.MaxLineLength > 0 {
		return s.config.MaxLineLength
	}
	return defaultMaxLineLength
}

// SetAuditLogger 注入审计专用 logger。必须在 Handle 启动前调用；
// nil 表示本会话不输出审计记录。
func (s *Session) SetAuditLogger(logger *slog.Logger) {
	s.auditLogger = logger
}

// audit 通过审计专用通道输出一条记录，不触碰进程全局 logger。
func (s *Session) audit(kv ...any) {
	if s.auditLogger == nil {
		return
	}
	args := make([]any, 0, len(kv)+2)
	args = append(args, "remote", s.remoteStr())
	args = append(args, kv...)
	s.auditLogger.Info("audit", args...)
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

// defaultMaxAuthAttempts MaxAuthAttempts 未配置（0/负数）时的默认密码错误次数上限
const defaultMaxAuthAttempts = 3

// authTimeout 认证阶段的读取超时，防止客户端连上后不发送密码，
// 无限期占用连接槽位（连接数上限场景下尤为危险）。认证成功后清除。
const authTimeout = 60 * time.Second

// defaultMaxLineLength 未配置 MaxLineLength 时单行输入与密码的字节上限
const defaultMaxLineLength = 4096

// maxSeqLen 等待 Telnet/ANSI 序列终止字节时的最大中间字节数，
// 超限后回退到正常输入态，防止未闭合序列永久挂起状态机
const maxSeqLen = 64

// 认证流程提示与结果消息（Cisco 风格 % 前缀）
const (
	authPrompt     = "\r\nUser Access Verification\r\n\r\nPassword: "
	authFailedMsg  = "\r\n% Password incorrect\r\n"
	authTooManyMsg = "\r\n% Too many failed authentication attempts. Disconnecting.\r\n"
)

// Handle 处理会话
func (s *Session) Handle(ctx context.Context) error {
	// 启用telnet字符模式
	s.enableTelnetCharacterMode()

	// 可选密码认证：Password 为空（默认）时完全跳过，行为与未开启认证时一致
	if s.config.Password != "" {
		if !s.authenticate(ctx) {
			return io.EOF
		}
	}

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

		// 认证成功后按配置刷新读超时：慢客户端不能无限占用会话槽位
		if s.config.ReadTimeout > 0 {
			_ = s.conn.SetReadDeadline(time.Now().Add(s.config.ReadTimeout))
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
		start := time.Now()
		err = s.processCommand(ctx, line)
		if s.config.AuditLog {
			s.auditCommand(line, time.Since(start), err)
		}
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
	stESCTilde                     // 收到 ESC [ <参数>，等待终止字节 '~' 或修饰变体字母
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

// lineEditor 会话行编辑缓冲：维护输入内容与光标位置，
// 支持 ←/→ 移动、Home/End（Ctrl+A/E）跳转、按光标位置插入/删除。
// 仅存储可打印 ASCII 字节（0x20-0x7E），按字节编辑即可。
type lineEditor struct {
	buf    []byte
	cursor int
}

func (e *lineEditor) String() string { return string(e.buf) }
func (e *lineEditor) Len() int       { return len(e.buf) }

// reset 替换全部内容，光标置于行尾
func (e *lineEditor) reset(s string) {
	e.buf = append(e.buf[:0], s...)
	e.cursor = len(e.buf)
}

// insert 在光标处插入一个可打印字节
func (e *lineEditor) insert(b byte) {
	e.buf = append(e.buf, 0)
	copy(e.buf[e.cursor+1:], e.buf[e.cursor:])
	e.buf[e.cursor] = b
	e.cursor++
}

// backspace 删除光标前一个字符，已到行首返回 false
func (e *lineEditor) backspace() bool {
	if e.cursor == 0 {
		return false
	}
	copy(e.buf[e.cursor-1:], e.buf[e.cursor:])
	e.buf = e.buf[:len(e.buf)-1]
	e.cursor--
	return true
}

// delete 删除光标处一个字符（Delete 键），光标在行尾返回 false
func (e *lineEditor) delete() bool {
	if e.cursor >= len(e.buf) {
		return false
	}
	copy(e.buf[e.cursor:], e.buf[e.cursor+1:])
	e.buf = e.buf[:len(e.buf)-1]
	return true
}

// left 光标左移一格，已在行首返回 false
func (e *lineEditor) left() bool {
	if e.cursor == 0 {
		return false
	}
	e.cursor--
	return true
}

// right 光标右移一格，已在行尾返回 false
func (e *lineEditor) right() bool {
	if e.cursor >= len(e.buf) {
		return false
	}
	e.cursor++
	return true
}

// home 光标跳行首
func (e *lineEditor) home() { e.cursor = 0 }

// end 光标跳行尾
func (e *lineEditor) end() { e.cursor = len(e.buf) }

// authenticate 密码认证流程：提示输入（不回显）→ 校验 → 错误计数。
// 超过错误次数上限后发送断开提示并返回 false（调用方关闭连接）。
// 认证阶段设置读取超时，防止客户端连上后不发密码而无限占用连接槽位。
func (s *Session) authenticate(ctx context.Context) bool {
	maxAttempts := s.config.MaxAuthAttempts
	if maxAttempts <= 0 {
		maxAttempts = defaultMaxAuthAttempts
	}

	// 认证阶段读取超时；成功/失败退出时均清除，恢复正常阻塞读
	_ = s.conn.SetReadDeadline(time.Now().Add(authTimeout))
	defer func() { _ = s.conn.SetReadDeadline(time.Time{}) }()

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return false
		default:
		}

		pw, err := s.readPasswordLine(authPrompt)
		if err != nil {
			// 读取错误（客户端断开、超时、Ctrl+C/D）
			if debuglog.Enabled() {
				debuglog.Debug("session", "auth_read_error",
					"remote", s.remoteStr(),
					"attempt", attempt,
					"err", err)
			}
			return false
		}
		if pw == s.config.Password {
			return true
		}

		slog.Info("authentication failed",
			"remote", s.remoteStr(),
			"attempt", attempt,
			"max_attempts", maxAttempts)
		if s.config.AuditLog {
			s.audit(
				"event", "auth_failed",
				"attempt", attempt,
				"max_attempts", maxAttempts)
		}
		s.writerWrite(authFailedMsg)
		s.flushWriter()
	}

	s.writerWrite(authTooManyMsg)
	s.flushWriter()
	slog.Info("connection closed: too many failed authentication attempts",
		"remote", s.remoteStr(),
		"attempts", maxAttempts)
	if s.config.AuditLog {
		s.audit(
			"event", "auth_exhausted",
			"attempts", maxAttempts,
			"result", "disconnect")
	}
	return false
}

// readPasswordLine 读取一行不回显的密码输入（认证专用）。
// 与 readLine 共用 telnetInput 过滤器，但不做历史/补全/帮助处理，
// 且不回显任何输入字符（服务器已协商 WILL ECHO，由服务端控制回显）。
func (s *Session) readPasswordLine(prompt string) (string, error) {
	if err := s.writerWrite(prompt); err != nil {
		return "", err
	}
	if err := s.flushWriter(); err != nil {
		return "", err
	}

	var buf strings.Builder
	s.inputFilter.reset()
	for {
		if s.writeErr != nil {
			return "", s.writeErr
		}

		var result string
		var done bool
		var eventErr error
		err := s.readEvents(&s.inputFilter, func(ev inputEvent) bool {
			switch ev.kind {
			case evCtrlC, evCtrlD:
				eventErr = io.EOF
				return false
			case evBackspace:
				if buf.Len() > 0 {
					current := buf.String()
					buf.Reset()
					buf.WriteString(current[:len(current)-1])
				}
			case evEnter:
				result = buf.String()
				done = true
				return false
			case evChar:
				if buf.Len() >= s.maxLineLength() {
					eventErr = fmt.Errorf("password exceeds maximum length of %d bytes", s.maxLineLength())
					return false
				}
				buf.WriteByte(ev.b) // 不回显
			}
			return true
		})
		if err != nil {
			return "", err
		}
		if eventErr != nil {
			return "", eventErr
		}
		if done {
			s.writerWrite("\r\n")
			s.flushWriter()
			return result, nil
		}
	}
}

// readLine 读取一行输入（支持光标级行编辑：←/→ 移动、Ctrl+A/E 跳行首/行尾、
// Backspace/Delete 按光标位置删字）
func (s *Session) readLine() (string, error) {
	// 会话级编辑缓冲复用：reset 只回退长度，底层数组保留容量
	e := &s.editor
	e.reset("")
	historyIndex := -1
	lineOverflow := false
	skipNextEnter := false // 超长行拒绝时跳过 CRLF 中的 LF

	// 上一次读取可能停在 CRLF 的 LF 上，直接跳过
	if len(s.pendingInput) > 0 && s.pendingInput[0] == '\n' {
		s.pendingInput = s.pendingInput[1:]
	}

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

	s.inputFilter.reset()
	for {
		if s.writeErr != nil {
			return "", s.writeErr
		}

		var result string
		var done bool
		var eventErr error

		err := s.readEvents(&s.inputFilter, func(ev inputEvent) bool {
			switch ev.kind {
			case evCtrlC, evCtrlD:
				eventErr = io.EOF
				return false
			case evCtrlA:
				e.home()
				s.redrawLineCursor(e)
			case evCtrlE:
				e.end()
				s.redrawLineCursor(e)
			case evBackspace:
				if e.backspace() {
					s.redrawLineCursor(e)
				}
			case evTab:
				s.handleTabCompletion(e)
			case evQuestion:
				s.showCommandHelp(e)
			case evUp:
				if s.history.Len() == 0 {
					e.reset("")
					s.redrawLineCursor(e)
				} else {
					if historyIndex < 0 {
						historyIndex = s.history.Len() - 1
					} else if historyIndex > 0 {
						historyIndex--
					}
					e.reset(s.history.Get(historyIndex))
					s.redrawLineCursor(e)
				}
			case evDown:
				if historyIndex >= 0 && historyIndex < s.history.Len()-1 {
					historyIndex++
					e.reset(s.history.Get(historyIndex))
					s.redrawLineCursor(e)
				} else if historyIndex == s.history.Len()-1 {
					historyIndex = -1
					e.reset("")
					s.redrawLineCursor(e)
				}
			case evLeft:
				if e.left() {
					s.redrawLineCursor(e)
				}
			case evRight:
				if e.right() {
					s.redrawLineCursor(e)
				}
			case evHome:
				e.home()
				s.redrawLineCursor(e)
			case evEnd:
				e.end()
				s.redrawLineCursor(e)
			case evDelete:
				if e.delete() {
					s.redrawLineCursor(e)
				}
			case evEnter:
				if skipNextEnter {
					skipNextEnter = false
					return true
				}
				if lineOverflow {
					s.writerWrite("\r\n% Line too long\r\n" + s.prompt)
					s.flushWriter()
					e.reset("")
					lineOverflow = false
					skipNextEnter = true
					historyIndex = -1
					return true
				}
				if debuglog.Enabled() {
					debuglog.Debug("session", "input_line_ready",
						"remote", s.remoteStr(),
						"line", e.String(),
						"len", e.Len())
				}
				result = e.String()
				done = true
				return false
			case evChar:
				if e.Len() >= s.maxLineLength() {
					lineOverflow = true
					return true
				}
				e.insert(ev.b)
				if e.cursor == e.Len() {
					// 光标在行尾：仅回显该字节
					s.writerWrite(string([]byte{ev.b}))
					s.flushWriter()
				} else {
					// 插入到行中：整行重绘并定位光标
					s.redrawLineCursor(e)
				}
			}
			return true
		})
		if err != nil {
			return "", err
		}
		if eventErr != nil {
			return "", eventErr
		}
		if done {
			s.writerWrite("\r\n")
			s.flushWriter()
			return result, nil
		}
	}
}

// auditCommand 输出命令审计记录：远端地址、命令原文、耗时与结果。
// err 为 nil 时 result=ok，否则记录错误信息。
func (s *Session) auditCommand(command string, elapsed time.Duration, err error) {
	result := "ok"
	if err != nil {
		result = err.Error()
	}
	s.audit(
		"command", command,
		"duration_ms", elapsed.Milliseconds(),
		"result", result)
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
	// 按页聚合写入，避免逐行 flush 产生多余系统调用
	var batch strings.Builder
	flushBatch := func() error {
		if batch.Len() == 0 {
			return nil
		}
		if err := s.writerWrite(batch.String()); err != nil {
			return err
		}
		batch.Reset()
		return s.flushWriter()
	}

	for idx, line := range lines {
		isLastLine := idx == len(lines)-1

		batch.WriteString(line)
		// 行尾分隔符：除非这是最后一行且原文无尾随换行
		if !(isLastLine && !hasTrailingNL) {
			batch.WriteString("\r\n")
		}

		// 每页末尾且非最后一行 → 显示 --More-- 并等待按键
		if (idx+1)%page == 0 && !isLastLine {
			// 先把本页内容整批写出，再单独写 --More--：提示必须是独立写入，
			// 便于客户端读到提示时写入已全部完成（也避免 net.Pipe 类客户端阻塞）
			if err := flushBatch(); err != nil {
				return err
			}
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
	return flushBatch()
}

// readPagingKey 在分页提示后读取用户的一个按键，跳过 Telnet IAC 序列与 ANSI 方向键序列。
// IAC 处理规则与 readLine 主状态机保持一致：
//   - IAC + 0xF1-0xF9/0xFF（NOP/GA/AYT/EC/EL/IAC-IAC 等）为 2 字节序列，仅再读 1 字节
//   - IAC + 0xFB-0xFE（WILL/WONT/DO/DONT）为 3 字节序列，再读命令与选项 2 字节
//   - IAC SB(0xFA) 进入子协商，跳过至 IAC SE(0xF0)
//
// 方向键（ESC [ X）视为"继续翻页"（返回空格的语义）。
func (s *Session) readPagingKey() (byte, error) {
	// 跳过命令行 CRLF 遗留的 LF，避免把它当成用户的翻页按键
	if len(s.pendingInput) > 0 && s.pendingInput[0] == '\n' {
		s.pendingInput = s.pendingInput[1:]
	}
	// 与命令/密码读取共用输入过滤器，IAC、ESC 与长度边界规则完全一致
	s.inputFilter.reset()
	for {
		var key byte
		var done bool
		err := s.readEvents(&s.inputFilter, func(ev inputEvent) bool {
			switch ev.kind {
			case evCtrlC, evCtrlD:
				// 原样返回控制字节，交由 writeResult 转为 io.EOF
				key = ev.b
				done = true
				return false
			case evChar:
				key = ev.b
				done = true
				return false
			case evEnter, evTab, evQuestion, evUp, evDown, evLeft, evRight, evHome, evEnd, evDelete:
				key = ' ' // 方向键等功能键视为继续翻页
				done = true
				return false
			}
			return true
		})
		if err != nil {
			return 0, err
		}
		if done {
			return key, nil
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
		return fmt.Errorf("%w: expected at least %d, got %d", types.ErrTooFewArguments, requiredParams, len(args))
	}

	if len(args) > requiredParams+optionalParams {
		s.writerWrite(fmt.Sprintf("Error: Too many arguments for command '%s'\r\n", strings.Join(matchedPath, " ")))
		s.writerWrite(fmt.Sprintf("Expected at most %d arguments, got %d\r\n", requiredParams+optionalParams, len(args)))
		return fmt.Errorf("%w: expected at most %d, got %d", types.ErrTooManyArguments, requiredParams+optionalParams, len(args))
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
				return fmt.Errorf("%w: parameter %d (%q) of command %q: %s",
					types.ErrInvalidParameterValue, i+1, arg, strings.Join(matchedPath, " "), errorMsg)
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
	case types.NodeTypeIPV4, types.NodeTypeIPV4Prefix,
		types.NodeTypeIPV6, types.NodeTypeIPV6Prefix,
		types.NodeTypeMAC, types.NodeTypeMACPrefix:
		return commandtree.GetPlaceholderValidationError(node.Type, input)
	default:
		return fmt.Sprintf("无效的参数值: '%s'", input)
	}
}

// redrawLine 重绘当前行（光标位于行尾）
func (s *Session) redrawLine(line string) {
	// 清除当前行并重新显示
	s.writerWrite("\r\x1b[K") // 回到行首并清除整行
	s.writerWrite(s.prompt)
	s.writerWrite(line)
	s.flushWriter()
}

// redrawLineCursor 重绘当前行并把终端光标定位到编辑光标位置：
// 行首清行 → 提示符 → 整行内容 →（光标不在行尾时）发送 ESC[{n}D 左移 n 列。
func (s *Session) redrawLineCursor(e *lineEditor) {
	s.writerWrite("\r\x1b[K")
	s.writerWrite(s.prompt)
	s.writerWrite(e.String())
	if back := e.Len() - e.cursor; back > 0 {
		s.writerWrite(fmt.Sprintf("\x1b[%dD", back))
	}
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

	// 走统一写入路径：写入失败会记入 writeErr 并由主循环短路退出
	if err := s.writerWrite(string(telnetCommands)); err != nil {
		if debuglog.Enabled() {
			debuglog.Debug("session", "telnet_negotiation_failed", "err", err)
		}
	}
}

// handleTabCompletion 处理Tab键补全
func (s *Session) handleTabCompletion(e *lineEditor) bool {
	currentInput := e.String()
	inputParts := strings.Fields(currentInput)

	if len(inputParts) == 0 {
		suggestions := s.completer.GetCommandTreeSuggestions(currentInput)
		if len(suggestions) > 0 {
			s.showCompletions(suggestions)
			s.redrawLineCursor(e)
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
			s.redrawLineCursor(e)
		} else {
			s.writerWrite("\x07")
			s.flushWriter()
		}
	case 1:
		// 唯一匹配：整行替换为补全结果，光标落行尾
		e.reset(nextLevelCompletions[0])
		s.redrawLineCursor(e)
	default:
		s.showCompletions(nextLevelCompletions)
		s.flushWriter()
		s.redrawLineCursor(e)
	}

	return true
}

// showCommandHelp 显示命令帮助（处理?键），遵循网络设备分级语义：
//   - 空输入 ?：显示当前视图所有一级命令
//   - 已输入关键字 ?：显示该关键字的下一级候选（子命令或参数）
//   - 命令已完整且无更多参数：提示 <cr> 表示可直接回车执行
//
// 帮助显示后按编辑光标位置重绘输入行。
func (s *Session) showCommandHelp(e *lineEditor) {
	inputParts := strings.Fields(e.String())
	suggestions := s.completer.GetCommandTreeSuggestions(e.String())
	if len(suggestions) > 0 {
		s.showCompletions(suggestions)
	} else if len(inputParts) > 0 {
		// 输入已是完整命令且无更多可输入项，提示回车执行
		s.writerWrite("\r\n")
		s.writerWrite("<cr>\r\n")
	} else {
		s.writerWrite("\r\nNo commands available\r\n")
	}
	// 重绘当前输入行，使光标回到编辑位置
	s.redrawLineCursor(e)
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
