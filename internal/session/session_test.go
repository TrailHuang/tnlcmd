package session

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/TrailHuang/tnlcmd/internal/mode"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// pipeTest 封装一对 net.Pipe 连接与一个运行中的 Session，便于驱动交互式测试。
type pipeTest struct {
	t      *testing.T
	client net.Conn
	cr     *bufio.Reader
	server net.Conn
	sess   *Session
	done   chan error // Session.Handle 的返回值
}

// newPipeTest 创建一对 pipe，启动 Session.Handle，并读取初始输出直到出现 prompt。
// pageSize<=0 表示禁用分页。
func newPipeTest(t *testing.T, pageSize int, rootMode *mode.CommandMode) *pipeTest {
	t.Helper()
	return newPipeTestWithHistory(t, pageSize, 10, rootMode)
}

// newPipeTestWithHistory 与 newPipeTest 相同，但可指定历史容量上限。
func newPipeTestWithHistory(t *testing.T, pageSize, maxHistory int, rootMode *mode.CommandMode) *pipeTest {
	t.Helper()
	return newPipeTestWithConfig(t, pageSize, maxHistory, rootMode, nil)
}

// newPipeTestWithConfig 在 newPipeTestWithHistory 基础上允许定制 Config
// （回调在会话创建前执行），供审计等开关类测试使用。
func newPipeTestWithConfig(t *testing.T, pageSize, maxHistory int, rootMode *mode.CommandMode, customize func(*types.Config)) *pipeTest {
	t.Helper()
	clientConn, serverConn := net.Pipe()

	config := &types.Config{
		Prompt:     "PROMPT_END>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: maxHistory,
		PageSize:   pageSize,
	}
	if customize != nil {
		customize(config)
	}
	ctx := &mode.CommandContext{CurrentMode: rootMode, Path: []string{}}
	sess := NewSessionWithContext(serverConn, config, ctx)

	pt := &pipeTest{
		t:      t,
		client: clientConn,
		cr:     bufio.NewReader(clientConn),
		server: serverConn,
		sess:   sess,
		done:   make(chan error, 1),
	}
	go func() {
		pt.done <- sess.Handle(context.Background())
		serverConn.Close()
	}()

	// 消费初始的 telnet 协商字节 + 首个提示符
	if _, err := pt.readUntil("PROMPT_END>", 3*time.Second); err != nil {
		t.Fatalf("failed to read initial prompt: %v", err)
	}
	return pt
}

// readUntil 不断读取字节，直到累积内容包含 target 或超时。返回累积内容。
func (pt *pipeTest) readUntil(target string, timeout time.Duration) (string, error) {
	pt.t.Helper()
	deadline := time.Now().Add(timeout)
	pt.client.SetReadDeadline(deadline)
	var buf strings.Builder
	for {
		b, err := pt.cr.ReadByte()
		if err != nil {
			return buf.String(), err
		}
		buf.WriteByte(b)
		if strings.Contains(buf.String(), target) {
			return buf.String(), nil
		}
	}
}

// write 向客户端连接写入数据。
func (pt *pipeTest) write(data string) error {
	pt.t.Helper()
	pt.client.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, err := pt.client.Write([]byte(data))
	return err
}

// close 关闭客户端连接并等待 Session.Handle 返回。
func (pt *pipeTest) close() {
	pt.client.Close()
	select {
	case <-pt.done:
	case <-time.After(3 * time.Second):
	}
}

// TestSession_AuditLogOnOff 验证审计开关：开启后每条命令输出含 remote/command/
// duration_ms/result 的结构化审计记录；关闭时无 audit 输出（工单 02）。
func TestSession_AuditLogOnOff(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo", func(args []string) string {
		return "RESULT\r\n"
	})

	for _, tc := range []struct {
		name      string
		auditOn   bool
		wantAudit bool
	}{
		{name: "on", auditOn: true, wantAudit: true},
		{name: "off", auditOn: false, wantAudit: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// 重定向 slog 默认 logger 捕获审计输出
			var logBuf safeLogBuffer
			prev := slog.Default()
			slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
			defer slog.SetDefault(prev)

			pt := newPipeTestWithConfig(t, 0, 10, rootMode, func(cfg *types.Config) {
				cfg.AuditLog = tc.auditOn
			})

			if err := pt.write("echo\r\n"); err != nil {
				t.Fatalf("write echo failed: %v", err)
			}
			if _, err := pt.readUntil("RESULT", 3*time.Second); err != nil {
				t.Fatalf("read result failed: %v", err)
			}
			// Handler 内 slog 输出与回显并行发生，稍作等待确保审计落盘
			time.Sleep(50 * time.Millisecond)
			pt.close()

			logs := logBuf.String()
			if !tc.wantAudit {
				if strings.Contains(logs, "audit") {
					t.Errorf("audit disabled but got audit log: %s", logs)
				}
				return
			}
			for _, key := range []string{"audit", "command=echo", "duration_ms=", "result=ok"} {
				if !strings.Contains(logs, key) {
					t.Errorf("audit log missing %q, got: %s", key, logs)
				}
			}
		})
	}
}

// TestSession_AuditLogOnCommandError 验证 auditCommand 在命令出错时仍输出审计，
// result 记录错误信息而非 ok（工单 02）。
// 说明：processCommand 的参数验证错误分支经公开输入不可达（findCommand 对
// 参数不匹配直接返回 ErrUnknownCommand 且最终输出后返回 nil），故此处白盒直测。
func TestSession_AuditLogOnCommandError(t *testing.T) {
	var logBuf safeLogBuffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logBuf, nil)))
	defer slog.SetDefault(prev)

	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	pt := newPipeTestWithConfig(t, 0, 10, rootMode, func(cfg *types.Config) {
		cfg.AuditLog = true
	})
	defer pt.close()

	s := pt.sess
	s.mu.RLock()
	s.auditCommand("set threshold 99", 12*time.Millisecond, errors.New("invalid parameter value"))
	s.mu.RUnlock()
	time.Sleep(50 * time.Millisecond)

	logs := logBuf.String()
	if !strings.Contains(logs, "audit") {
		t.Fatalf("expected audit log for failed command, got: %s", logs)
	}
	// slog TextHandler 对含空格值输出带引号：result="invalid parameter value"
	if !strings.Contains(logs, `result="invalid parameter value"`) {
		t.Errorf("audit log should record error result, got: %s", logs)
	}
	if strings.Contains(logs, "result=ok") {
		t.Errorf("failed command should not audit result=ok, got: %s", logs)
	}
}

// safeLogBuffer 并发安全的日志缓冲（slog 输出与测试主线程并行写入）。
type safeLogBuffer struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *safeLogBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *safeLogBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestSession_BasicCommand 验证注册的命令能被正确执行并返回结果。
func TestSession_BasicCommand(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo", func(args []string) string {
		return "RESULT_MARKER\r\n"
	})

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	if err := pt.write("echo\r\n"); err != nil {
		t.Fatalf("write command failed: %v", err)
	}
	got, err := pt.readUntil("RESULT_MARKER", 3*time.Second)
	if err != nil {
		t.Fatalf("did not see command result: %v (buf=%q)", err, got)
	}
}

// TestSession_ExitClosesConnection 验证 SentinelExit 触发 "Goodbye!" 并关闭连接。
func TestSession_ExitClosesConnection(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("exit", "Exit", func(args []string) string {
		return types.SentinelExit
	})

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	if err := pt.write("exit\r\n"); err != nil {
		t.Fatalf("write exit failed: %v", err)
	}
	got, err := pt.readUntil("Goodbye!", 3*time.Second)
	if err != nil {
		t.Fatalf("did not see Goodbye: %v (buf=%q)", err, got)
	}
	// Handle 返回 nil（正常关闭）
	select {
	case err := <-pt.done:
		if err != nil {
			t.Errorf("Handle should return nil for exit, got %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timeout waiting for Handle to return after exit")
	}
}

// TestSession_UnknownCommand 验证未知命令返回提示信息。
func TestSession_UnknownCommand(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	if err := pt.write("nonsense\r\n"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	got, err := pt.readUntil("Unknown command", 3*time.Second)
	if err != nil {
		t.Fatalf("did not see Unknown command message: %v (buf=%q)", err, got)
	}
}

// TestSession_Pagination 验证 PageSize 生效时输出分页，Space 翻页后能看到后续内容。
func TestSession_Pagination(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	// 5 行输出，PageSize=2 → 应出现两次 --More--
	rootMode.AddCommand("list", "List", func(args []string) string {
		return "L1\r\nL2\r\nL3\r\nL4\r\nL5\r\n"
	})

	pt := newPipeTest(t, 2, rootMode)
	defer pt.close()

	if err := pt.write("list\r\n"); err != nil {
		t.Fatalf("write list failed: %v", err)
	}

	// 第一页后应出现 --More--
	if got, err := pt.readUntil("--More--", 3*time.Second); err != nil {
		t.Fatalf("did not see first --More--: %v (buf=%q)", err, got)
	}
	// 发送 Space 翻到第二页
	if err := pt.write(" "); err != nil {
		t.Fatalf("write space failed: %v", err)
	}
	if got, err := pt.readUntil("--More--", 3*time.Second); err != nil {
		t.Fatalf("did not see second --More--: %v (buf=%q)", err, got)
	}
	// 再翻页，应能看到最后一行 L5
	if err := pt.write(" "); err != nil {
		t.Fatalf("write space failed: %v", err)
	}
	if got, err := pt.readUntil("L5", 3*time.Second); err != nil {
		t.Fatalf("did not see L5 after paging: %v (buf=%q)", err, got)
	}
}

// TestSession_Pagination_QuitWithQ 验证在 --More-- 处按 q 中止剩余输出。
func TestSession_Pagination_QuitWithQ(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("list", "List", func(args []string) string {
		return "L1\r\nL2\r\nL3\r\nL4\r\nL5\r\n"
	})

	pt := newPipeTest(t, 2, rootMode)
	defer pt.close()

	if err := pt.write("list\r\n"); err != nil {
		t.Fatalf("write list failed: %v", err)
	}
	if got, err := pt.readUntil("--More--", 3*time.Second); err != nil {
		t.Fatalf("did not see --More--: %v (buf=%q)", err, got)
	}
	// 按 q 中止
	if err := pt.write("q"); err != nil {
		t.Fatalf("write q failed: %v", err)
	}
	// 中止后应回到提示符；后续内容 L3/L4/L5 不应再出现。
	// 读取到下一个提示符，并检查其中不包含 L3。
	got, err := pt.readUntil("PROMPT_END>", 3*time.Second)
	if err != nil {
		t.Fatalf("did not return to prompt after q: %v (buf=%q)", err, got)
	}
	// got 包含从 --More-- 之后到提示符的内容；验证未输出 L3/L4/L5
	// （L1/L2 在第一页已输出，可能在缓冲区中，但 L3 不应出现）
	if strings.Contains(got, "L3") || strings.Contains(got, "L4") || strings.Contains(got, "L5") {
		t.Errorf("remaining lines should be aborted after q, got buf=%q", got)
	}
}

// TestSession_Pagination_DisabledWhenPageSizeZero 验证 PageSize<=0 时一次性输出，不出现 --More--。
func TestSession_Pagination_DisabledWhenPageSizeZero(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("list", "List", func(args []string) string {
		return "L1\r\nL2\r\nL3\r\nL4\r\nL5\r\n"
	})

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	if err := pt.write("list\r\n"); err != nil {
		t.Fatalf("write list failed: %v", err)
	}
	// 应一次性看到最后一行 L5
	got, err := pt.readUntil("L5", 3*time.Second)
	if err != nil {
		t.Fatalf("did not see L5: %v (buf=%q)", err, got)
	}
	if strings.Contains(got, "--More--") {
		t.Errorf("pagination should be disabled when PageSize<=0, got --More-- in buf=%q", got)
	}
}

// TestSession_WriteErrorPropagates 验证客户端断开后写入失败会使 Handle 及时退出。
func TestSession_WriteErrorPropagates(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("big", "Big output", func(args []string) string {
		return "X1\r\nX2\r\nX3\r\n"
	})

	pt := newPipeTest(t, 0, rootMode)
	// 注意：不 defer pt.close()，这里手动关闭客户端

	if err := pt.write("big\r\n"); err != nil {
		t.Fatalf("write big failed: %v", err)
	}
	// 关闭客户端连接，使服务端的回显写入失败
	pt.client.Close()

	select {
	case err := <-pt.done:
		// Handle 应因写入错误返回非 nil 错误（io.EOF 表示正常关闭，这里期望非 EOF 的写入错误或 EOF 也算退出）
		// 关键是 Handle 必须退出，不能卡住
		if err != nil && err != io.EOF {
			t.Logf("Handle returned expected write error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout: Handle did not return after client disconnect (write error not propagated)")
	}
	pt.server.Close()
}

// TestSession_IAC_SplitAcrossReads 验证 Telnet IAC 协商序列跨 read 批次时
// 不会被误解析为普通输入（修复"运行几次后 ? 失灵"的根因）。
//
// 机制：旧实现在 IAC 序列不完整（i+2 < n 不成立）时不缓冲，导致后续字节
// 被当作普通输入处理，协议状态错乱累积后 ?/方向键等失灵。
func TestSession_IAC_SplitAcrossReads(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo", func(args []string) string {
		return "ECHO_OK\r\n"
	})

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// 先发一个 IAC 序列的前 1 字节，再发剩余 2 字节 + 正常命令 echo
	// 第一片：仅 IAC 字节
	if err := pt.write(string([]byte{0xFF})); err != nil {
		t.Fatalf("write IAC head failed: %v", err)
	}
	time.Sleep(80 * time.Millisecond) // 确保服务端 read 到这一字节并缓存
	// 第二片：IAC 命令 + 选项 + 命令 echo\r\n
	if err := pt.write(string([]byte{0xFB, 0x01}) + "echo\r\n"); err != nil {
		t.Fatalf("write IAC tail + echo failed: %v", err)
	}
	// 应能正确收到 echo 结果，IAC 被静默丢弃
	if got, err := pt.readUntil("ECHO_OK", 3*time.Second); err != nil {
		t.Fatalf("IAC split across reads broke input parsing: %v (buf=%q)", err, got)
	}
}

// TestSession_ESC_SplitAcrossReads 验证方向键 ESC 序列跨 read 批次时
// 不会被误解析为普通输入。
func TestSession_ESC_SplitAcrossReads(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("cmd1", "cmd1", func(args []string) string { return "R1\r\n" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// 先输入一条命令建立历史
	if err := pt.write("cmd1\r\n"); err != nil {
		t.Fatalf("write cmd1 failed: %v", err)
	}
	if _, err := pt.readUntil("R1", 3*time.Second); err != nil {
		t.Fatalf("did not see R1: %v", err)
	}
	// 消费掉 prompt，避免后续写入与服务器的 prompt 写入死锁（net.Pipe 无缓冲）
	if _, err := pt.readUntil("PROMPT_END>", 3*time.Second); err != nil {
		t.Fatalf("did not see prompt after R1: %v", err)
	}

	// 发送跨批次的上方向键：ESC，然后 [A
	if err := pt.write(string([]byte{0x1B})); err != nil {
		t.Fatalf("write ESC head failed: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if err := pt.write("[A"); err != nil {
		t.Fatalf("write ESC tail failed: %v", err)
	}
	// 消费掉 ESC 处理后的重绘输出（\r\x1b[KPROMPT_END>cmd1），避免 \r\n
	// 写入时服务端 redrawLine 的 flush("cmd1") 还在阻塞（net.Pipe 无缓冲）
	if _, err := pt.readUntil("PROMPT_END>cmd1", 3*time.Second); err != nil {
		t.Fatalf("did not see redraw output after ESC: %v", err)
	}
	// 上方向键应回溯历史，输入行变为 cmd1；按回车执行应再次得到 R1
	if err := pt.write("\r\n"); err != nil {
		t.Fatalf("write enter failed: %v", err)
	}
	if got, err := pt.readUntil("R1", 3*time.Second); err != nil {
		t.Fatalf("ESC split across reads broke history navigation: %v (buf=%q)", err, got)
	}
}

// TestSession_HistoryNavigationWithWrapAround 验证历史容量回绕后，
// 上/下方向键浏览到的命令顺序与改动前一致（工单 01 的集成验证）。
func TestSession_HistoryNavigationWithWrapAround(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("cmd1", "cmd1", func(args []string) string { return "R1\r\n" })
	rootMode.AddCommand("cmd2", "cmd2", func(args []string) string { return "R2\r\n" })
	rootMode.AddCommand("cmd3", "cmd3", func(args []string) string { return "R3\r\n" })

	// 历史容量为 2：执行 cmd1、cmd2、cmd3 后，最旧的 cmd1 应被淘汰
	pt := newPipeTestWithHistory(t, 0, 2, rootMode)
	defer pt.close()

	for i, cmd := range []string{"cmd1", "cmd2", "cmd3"} {
		if err := pt.write(cmd + "\r\n"); err != nil {
			t.Fatalf("write %s failed: %v", cmd, err)
		}
		want := fmt.Sprintf("R%d", i+1)
		if _, err := pt.readUntil(want, 3*time.Second); err != nil {
			t.Fatalf("did not see %s: %v", want, err)
		}
		if _, err := pt.readUntil("PROMPT_END>", 3*time.Second); err != nil {
			t.Fatalf("did not see prompt after %s: %v", want, err)
		}
	}

	// 上方向键一次 → cmd3（最近一条，bash 式行为），再上一次 → cmd2（cmd1 已被淘汰）
	if err := pt.write("\x1b[A"); err != nil {
		t.Fatalf("write up arrow failed: %v", err)
	}
	if _, err := pt.readUntil("PROMPT_END>cmd3", 3*time.Second); err != nil {
		t.Fatalf("first up arrow should recall cmd3: %v", err)
	}

	if err := pt.write("\x1b[A"); err != nil {
		t.Fatalf("write second up arrow failed: %v", err)
	}
	if _, err := pt.readUntil("PROMPT_END>cmd2", 3*time.Second); err != nil {
		t.Fatalf("second up arrow should recall cmd2 (cmd1 evicted): %v", err)
	}

	// 再次上移应停在最旧的一条（cmd2）
	if err := pt.write("\x1b[A"); err != nil {
		t.Fatalf("write third up arrow failed: %v", err)
	}
	if _, err := pt.readUntil("PROMPT_END>cmd2", 3*time.Second); err != nil {
		t.Fatalf("third up arrow should stay at cmd2 (oldest): %v", err)
	}

	// 下方向键一次 → cmd3（最新一条）
	if err := pt.write("\x1b[B"); err != nil {
		t.Fatalf("write down arrow failed: %v", err)
	}
	if _, err := pt.readUntil("PROMPT_END>cmd3", 3*time.Second); err != nil {
		t.Fatalf("down arrow should recall cmd3: %v", err)
	}

	// 再按一次下方向键 → 回到空输入行
	if err := pt.write("\x1b[B"); err != nil {
		t.Fatalf("write second down arrow failed: %v", err)
	}
	if _, err := pt.readUntil("PROMPT_END>", 3*time.Second); err != nil {
		t.Fatalf("down arrow past end should clear the line: %v", err)
	}
}

// TestSession_QuestionMarkAfterIAC 验证 IAC 协商之后 ? 仍能正常触发帮助。
// 这是用户报告的场景：多次交互后 ? 失效。
func TestSession_QuestionMarkAfterIAC(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo arguments", func(args []string) string { return "" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// 注入若干跨批次 IAC 序列，模拟客户端周期性协商
	for k := 0; k < 3; k++ {
		if err := pt.write(string([]byte{0xFF})); err != nil {
			t.Fatalf("write IAC head #%d failed: %v", k, err)
		}
		time.Sleep(30 * time.Millisecond)
		if err := pt.write(string([]byte{0xFB, 0x01})); err != nil {
			t.Fatalf("write IAC tail #%d failed: %v", k, err)
		}
		time.Sleep(30 * time.Millisecond)
	}

	// 发送 ? 应能看到命令列表（含 echo 描述）
	if err := pt.write("?"); err != nil {
		t.Fatalf("write ? failed: %v", err)
	}
	if got, err := pt.readUntil("Echo arguments", 3*time.Second); err != nil {
		t.Fatalf("? did not trigger help after IAC sequences (regression): %v (buf=%q)", err, got)
	}
}

// TestSession_FullWidthQuestionMark 验证中文输入法下输入全角问号 '？'
// （UTF-8 编码 EF BC 9F）同样能触发帮助。这是用户报告的场景：Terminal
// 输入全角 '？' 后无任何反应。
func TestSession_FullWidthQuestionMark(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo arguments", func(args []string) string { return "" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// 发送全角问号 '？' = UTF-8 EF BC 9F，应触发帮助显示命令列表
	if err := pt.write(string([]byte{0xEF, 0xBC, 0x9F})); err != nil {
		t.Fatalf("write full-width question mark failed: %v", err)
	}
	if got, err := pt.readUntil("Echo arguments", 3*time.Second); err != nil {
		t.Fatalf("full-width '?' did not trigger help: %v (buf=%q)", err, got)
	}
}

// TestSession_FullWidthQuestionMark_SplitAcrossReads 验证全角问号跨批次
// 发送（EF / BC / 9F 分三次）时仍能正确识别并触发帮助。
func TestSession_FullWidthQuestionMark_SplitAcrossReads(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo arguments", func(args []string) string { return "" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	for _, b := range []byte{0xEF, 0xBC, 0x9F} {
		if err := pt.write(string([]byte{b})); err != nil {
			t.Fatalf("write byte %02x failed: %v", b, err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if got, err := pt.readUntil("Echo arguments", 3*time.Second); err != nil {
		t.Fatalf("split full-width '?' did not trigger help: %v (buf=%q)", err, got)
	}
}

// TestSession_TwoByteIAC_DoesNotSwallowQuestionMark 验证 2 字节 IAC 命令
// （如 IAC NOP = FF F1）不会吞掉紧随其后的 ? 字符。
//
// 这是用户报告的精确场景：? 间歇性失效，其他字符正常，多输几次又恢复。
// 根因：旧代码假设 IAC 恒为 3 字节，2 字节命令后第一个用户字节被误当作
// IAC 第三字节吞掉。若该字节恰好是 ?，帮助就不触发。
func TestSession_TwoByteIAC_DoesNotSwallowQuestionMark(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo arguments", func(args []string) string { return "" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// 发送 IAC NOP（2 字节）+ ? 在同一批次
	// 旧代码：FF F1 被当作 3 字节 IAC 的前 2 字节，? 被当作第三字节吞掉
	// 修复后：FF F1 识别为完整的 2 字节命令，? 正常触发帮助
	if err := pt.write(string([]byte{0xFF, 0xF1, 0x3F})); err != nil {
		t.Fatalf("write IAC NOP + ? failed: %v", err)
	}
	if got, err := pt.readUntil("Echo arguments", 3*time.Second); err != nil {
		t.Fatalf("2-byte IAC NOP swallowed ? (regression): %v (buf=%q)", err, got)
	}
}

// TestSession_TwoByteIAC_SplitAcrossReads 验证 2 字节 IAC 命令跨批次时
// 仍能正确识别为 2 字节序列，不等待第三字节。
func TestSession_TwoByteIAC_SplitAcrossReads(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo", func(args []string) string { return "OK\r\n" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// IAC NOP 分两次发送：FF，然后 F1 + echo\r\n
	if err := pt.write(string([]byte{0xFF})); err != nil {
		t.Fatalf("write IAC head failed: %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	// 第二片：NOP 命令字节 + 正常命令
	if err := pt.write(string([]byte{0xF1}) + "echo\r\n"); err != nil {
		t.Fatalf("write NOP cmd + echo failed: %v", err)
	}
	if got, err := pt.readUntil("OK", 3*time.Second); err != nil {
		t.Fatalf("2-byte IAC split broke input: %v (buf=%q)", err, got)
	}
}

// TestSession_IAC_IAC_EscapedByte 验证 IAC IAC（转义的 0xFF）被正确识别为
// 2 字节序列，不吞掉后续字节。
func TestSession_IAC_IAC_EscapedByte(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo", func(args []string) string { return "OK\r\n" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// IAC IAC (FF FF) + echo\r\n
	if err := pt.write(string([]byte{0xFF, 0xFF}) + "echo\r\n"); err != nil {
		t.Fatalf("write IAC IAC + echo failed: %v", err)
	}
	if got, err := pt.readUntil("OK", 3*time.Second); err != nil {
		t.Fatalf("IAC IAC broke input: %v (buf=%q)", err, got)
	}
}

// TestSession_MultipleTwoByteIACThenQuestionMark 验证连续多个 2 字节 IAC 命令
// 后 ? 仍正常工作（模拟客户端周期性发送 NOP/GA 等）。
func TestSession_MultipleTwoByteIACThenQuestionMark(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("show", "Show status", func(args []string) string { return "" })

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// 连续发送 5 个 2 字节 IAC 命令（NOP + GA 交替），每次后跟一个普通字符
	// 模拟客户端在交互中周期性发送 keep-alive / GA
	for k := 0; k < 5; k++ {
		cmd := byte(0xF1) // NOP
		if k%2 == 1 {
			cmd = 0xF9 // GA
		}
		// 发送 2 字节 IAC + 一个普通字符（不应被吞）
		if err := pt.write(string([]byte{0xFF, cmd})); err != nil {
			t.Fatalf("write 2-byte IAC #%d failed: %v", k, err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 最后发送 ?，应正常触发帮助
	if err := pt.write("?"); err != nil {
		t.Fatalf("write ? failed: %v", err)
	}
	if got, err := pt.readUntil("Show status", 3*time.Second); err != nil {
		t.Fatalf("? failed after multiple 2-byte IAC (regression): %v (buf=%q)", err, got)
	}
}

// TestSession_Pagination_TwoByteIACBeforeKey 验证分页等待按键时 2 字节 IAC 命令
// （如客户端 keep-alive NOP/GA）不会被误吞后续用户字节——修复 S3 的回归测试。
// 旧实现对 IAC 一律多读 2 字节，会吞掉 NOP 后紧跟的翻页键。
func TestSession_Pagination_TwoByteIACBeforeKey(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("list", "List", func(args []string) string {
		return "L1\r\nL2\r\nL3\r\nL4\r\nL5\r\n"
	})

	pt := newPipeTest(t, 2, rootMode)
	defer pt.close()

	if err := pt.write("list\r\n"); err != nil {
		t.Fatalf("write list failed: %v", err)
	}
	if got, err := pt.readUntil("--More--", 3*time.Second); err != nil {
		t.Fatalf("did not see --More--: %v (buf=%q)", err, got)
	}
	// 先发送一个 2 字节 IAC（NOP），再发送 Space 翻页。
	// 修复前：NOP 会额外吞掉 Space，导致会话继续等待、测试超时。
	if err := pt.write(string([]byte{0xFF, 0xF1})); err != nil {
		t.Fatalf("write NOP failed: %v", err)
	}
	time.Sleep(20 * time.Millisecond)
	if err := pt.write(" "); err != nil {
		t.Fatalf("write space failed: %v", err)
	}
	if got, err := pt.readUntil("L3", 3*time.Second); err != nil {
		t.Fatalf("paging key lost after 2-byte IAC (S3 regression): %v (buf=%q)", err, got)
	}
}

// TestSession_UpdatePromptConcurrent 验证会话运行期间并发调用 UpdatePrompt
// 不会引发数据竞争或破坏输出流（-race 下运行）——修复 S1 的回归测试。
// 修复前 UpdatePrompt 直接写 bufio.Writer，与主循环并发写同一连接。
func TestSession_UpdatePromptConcurrent(t *testing.T) {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("echo", "Echo", func(args []string) string {
		return "RESULT_MARKER\r\n"
	})

	pt := newPipeTest(t, 0, rootMode)
	defer pt.close()

	// 会话运行期间从外部 goroutine 并发更新提示符
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			pt.sess.UpdatePrompt(fmt.Sprintf("P%d_END>", i%3))
			time.Sleep(time.Millisecond)
		}
	}()

	// 期间正常执行命令
	if err := pt.write("echo\r\n"); err != nil {
		t.Fatalf("write command failed: %v", err)
	}
	if got, err := pt.readUntil("RESULT_MARKER", 3*time.Second); err != nil {
		t.Fatalf("command failed during concurrent UpdatePrompt: %v (buf=%q)", err, got)
	}
	close(stop)
	wg.Wait()

	// 提示符字段最终被更新为某次调用写入的值
	pt.sess.mu.RLock()
	final := pt.sess.prompt
	pt.sess.mu.RUnlock()
	if !strings.HasPrefix(final, "P") || !strings.HasSuffix(final, "_END>") {
		t.Errorf("prompt not updated by UpdatePrompt: %q", final)
	}
}

// scriptedConn 是一个同步的内存 net.Conn：按预置脚本返回读取字节、丢弃写入。
// 用于在单 goroutine 内驱动 readLine，避免 net.Pipe 的并发噪声影响分配测量。
type scriptedConn struct {
	data []byte
	pos  int
}

func (c *scriptedConn) Read(p []byte) (int, error) {
	if c.pos >= len(c.data) {
		return 0, io.EOF
	}
	n := copy(p, c.data[c.pos:])
	c.pos += n
	return n, nil
}

func (c *scriptedConn) Write(p []byte) (int, error)      { return len(p), nil }
func (c *scriptedConn) Close() error                     { return nil }
func (c *scriptedConn) LocalAddr() net.Addr              { return scriptedAddr{} }
func (c *scriptedConn) RemoteAddr() net.Addr             { return scriptedAddr{} }
func (c *scriptedConn) SetDeadline(time.Time) error      { return nil }
func (c *scriptedConn) SetReadDeadline(time.Time) error  { return nil }
func (c *scriptedConn) SetWriteDeadline(time.Time) error { return nil }

type scriptedAddr struct{}

func (scriptedAddr) Network() string { return "scripted" }
func (scriptedAddr) String() string  { return "scripted" }

// newScriptedSession 构造一个由 scriptedConn 驱动的会话，供分配测量使用。
func newScriptedSession(input string) (*Session, *scriptedConn) {
	rootMode := mode.NewCommandMode("root", "P>", "test")
	config := &types.Config{
		Prompt:     "P>",
		MaxHistory: 10,
	}
	conn := &scriptedConn{data: []byte(input)}
	sess := NewSessionWithContext(conn, config, &mode.CommandContext{CurrentMode: rootMode})
	return sess, conn
}

// TestSession_ReadBufferReused 验证 readLine 使用会话级读缓冲并在多次调用间复用同一底层数组，
// 不再每轮 make([]byte, 1024)（工单 02 的核心约束）。
// scriptedConn 为同步实现，readLine 与断言在同一 goroutine 内顺序执行，无数据竞争。
func TestSession_ReadBufferReused(t *testing.T) {
	sess, conn := newScriptedSession("cmd\r")

	if len(sess.readBuf) != readBufSize {
		t.Fatalf("readBuf len = %d, want %d", len(sess.readBuf), readBufSize)
	}
	first := &sess.readBuf[0]

	for i := 0; i < 3; i++ {
		conn.pos = 0
		if line, err := sess.readLine(); err != nil || line != "cmd" {
			t.Fatalf("readLine #%d = (%q, %v), want (%q, nil)", i, line, err, "cmd")
		}
		if &sess.readBuf[0] != first {
			t.Fatalf("readBuf reallocated on iteration #%d: buffer must be reused across reads", i)
		}
	}
}

// BenchmarkReadLine 测量单行输入从读取到返回的分配情况。
// 通过 -benchmem 观察 allocs/op 与 B/op：读缓冲复用后，每轮读取不再额外分配 1KB。
func BenchmarkReadLine(b *testing.B) {
	sess, conn := newScriptedSession("show version\r")

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		conn.pos = 0
		if _, err := sess.readLine(); err != nil {
			b.Fatalf("readLine failed: %v", err)
		}
	}
}

// newCursorTest 构造带 say 命令（回显第一个参数）的 pipeTest，
// 供光标编辑系列测试使用。
func newCursorTest(t *testing.T) *pipeTest {
	rootMode := mode.NewCommandMode("root", "PROMPT_END>", "test")
	rootMode.AddCommand("say WORD", "Echo first arg", func(args []string) string {
		return fmt.Sprintf("SAID=%s\r\n", args[0])
	})
	return newPipeTest(t, 0, rootMode)
}

// TestSession_CursorLeftInsert 验证 ← 移动光标后输入插入到光标处（工单 07）。
// "say helo" + ← + "l" → "say hello"。
// 注意：net.Pipe 同步传输，每次写入后必须排空服务端输出（回显/重绘）再写下一批。
func TestSession_CursorLeftInsert(t *testing.T) {
	pt := newCursorTest(t)
	defer pt.close()

	// 输入并排空回显
	if err := pt.write("say helo"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := pt.readUntil("say helo", 3*time.Second); err != nil {
		t.Fatalf("did not see echo: %v", err)
	}

	// ←：光标移到 'o' 前，服务端整行重绘并以 ESC[1D 定位光标
	if err := pt.write("\x1b[D"); err != nil {
		t.Fatalf("write left arrow failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[1D", 3*time.Second); err != nil {
		t.Fatalf("did not see redraw after left arrow: %v", err)
	}

	// 行中插入 'l' → "say hello"，光标仍偏移 1 列
	if err := pt.write("l"); err != nil {
		t.Fatalf("write insert failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[KPROMPT_END>say hello\x1b[1D", 3*time.Second); err != nil {
		t.Fatalf("mid-line insert did not redraw 'say hello': %v", err)
	}

	if err := pt.write("\r\n"); err != nil {
		t.Fatalf("write enter failed: %v", err)
	}
	if _, err := pt.readUntil("SAID=hello", 3*time.Second); err != nil {
		t.Fatalf("left+insert did not produce 'say hello': %v", err)
	}
}

// TestSession_CtrlAHomeInsert 验证 Ctrl+A 跳行首后输入插入到行首（工单 07）。
// "ay hello" + Ctrl+A + "s" → "say hello"。
func TestSession_CtrlAHomeInsert(t *testing.T) {
	pt := newCursorTest(t)
	defer pt.close()

	if err := pt.write("ay hello"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := pt.readUntil("ay hello", 3*time.Second); err != nil {
		t.Fatalf("did not see echo: %v", err)
	}

	// Ctrl+A：跳行首，重绘后光标左移 8 列（回到行首）
	if err := pt.write("\x01"); err != nil {
		t.Fatalf("write Ctrl+A failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[8D", 3*time.Second); err != nil {
		t.Fatalf("did not see redraw after Ctrl+A: %v", err)
	}

	// 行首插入 's' → "say hello"，光标在 1 → 仍需左移 8 列
	if err := pt.write("s"); err != nil {
		t.Fatalf("write insert failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[KPROMPT_END>say hello\x1b[8D", 3*time.Second); err != nil {
		t.Fatalf("home insert did not redraw 'say hello': %v", err)
	}

	if err := pt.write("\r\n"); err != nil {
		t.Fatalf("write enter failed: %v", err)
	}
	if _, err := pt.readUntil("SAID=hello", 3*time.Second); err != nil {
		t.Fatalf("Ctrl+A+insert did not produce 'say hello': %v", err)
	}
}

// TestSession_CtrlAHomeCtrlEEnd 验证 Ctrl+A / Ctrl+E 组合跳转后输入落在行尾（工单 07）。
// "say hel" + Ctrl+A + Ctrl+E + "lo" → "say hello"。
func TestSession_CtrlAHomeCtrlEEnd(t *testing.T) {
	pt := newCursorTest(t)
	defer pt.close()

	if err := pt.write("say hel"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := pt.readUntil("say hel", 3*time.Second); err != nil {
		t.Fatalf("did not see echo: %v", err)
	}

	// Ctrl+A：跳行首（重绘含 ESC[7D 光标定位）
	if err := pt.write("\x01"); err != nil {
		t.Fatalf("write Ctrl+A failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[7D", 3*time.Second); err != nil {
		t.Fatalf("did not see redraw after Ctrl+A: %v", err)
	}

	// Ctrl+E：跳行尾（光标已在行尾则重绘无光标移动序列）
	if err := pt.write("\x05"); err != nil {
		t.Fatalf("write Ctrl+E failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[KPROMPT_END>say hel", 3*time.Second); err != nil {
		t.Fatalf("did not see redraw after Ctrl+E: %v", err)
	}

	if err := pt.write("lo\r\n"); err != nil {
		t.Fatalf("write insert+enter failed: %v", err)
	}
	if _, err := pt.readUntil("SAID=hello", 3*time.Second); err != nil {
		t.Fatalf("Ctrl+A/Ctrl+E navigation did not produce 'say hello': %v", err)
	}
}

// TestSession_DeleteKeyAtCursor 验证 Delete 键（ESC[3~）删除光标处字符（工单 07）。
// "say axbc" + ←×3（光标停在 'x' 上）+ Delete → "say abc"。
func TestSession_DeleteKeyAtCursor(t *testing.T) {
	pt := newCursorTest(t)
	defer pt.close()

	if err := pt.write("say axbc"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := pt.readUntil("say axbc", 3*time.Second); err != nil {
		t.Fatalf("did not see echo: %v", err)
	}

	// ←×3：光标从行尾移到 'x' 前，每次重绘的左移列数递增 1→2→3
	for i := 1; i <= 3; i++ {
		if err := pt.write("\x1b[D"); err != nil {
			t.Fatalf("write left arrow #%d failed: %v", i, err)
		}
		if _, err := pt.readUntil(fmt.Sprintf("\x1b[%dD", i), 3*time.Second); err != nil {
			t.Fatalf("did not see redraw #%d after left arrow: %v", i, err)
		}
	}

	// Delete（ESC[3~）删除 'x' → "say abc"，光标偏移 2 列
	if err := pt.write("\x1b[3~"); err != nil {
		t.Fatalf("write delete key failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[KPROMPT_END>say abc\x1b[2D", 3*time.Second); err != nil {
		t.Fatalf("Delete key did not remove char under cursor: %v", err)
	}

	if err := pt.write("\r\n"); err != nil {
		t.Fatalf("write enter failed: %v", err)
	}
	if _, err := pt.readUntil("SAID=abc", 3*time.Second); err != nil {
		t.Fatalf("Delete key edit did not produce 'say abc': %v", err)
	}
}

// TestSession_BackspaceAtCursor 验证 Backspace 在光标位于行中时删除前一字符
// 并保持右侧内容（工单 07：非仅行尾删除）。
// "say aXbc" + ←×2 + Backspace → "say abc"。
func TestSession_BackspaceAtCursor(t *testing.T) {
	pt := newCursorTest(t)
	defer pt.close()

	if err := pt.write("say aXbc"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := pt.readUntil("say aXbc", 3*time.Second); err != nil {
		t.Fatalf("did not see echo: %v", err)
	}

	// ←×2：光标移到 'X' 之后
	for i := 1; i <= 2; i++ {
		if err := pt.write("\x1b[D"); err != nil {
			t.Fatalf("write left arrow #%d failed: %v", i, err)
		}
		if _, err := pt.readUntil(fmt.Sprintf("\x1b[%dD", i), 3*time.Second); err != nil {
			t.Fatalf("did not see redraw #%d after left arrow: %v", i, err)
		}
	}

	// Backspace 删除 'X' → "say abc"，光标偏移 2 列
	if err := pt.write("\x7f"); err != nil {
		t.Fatalf("write backspace failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[KPROMPT_END>say abc\x1b[2D", 3*time.Second); err != nil {
		t.Fatalf("mid-line backspace did not produce 'say abc': %v", err)
	}

	if err := pt.write("\r\n"); err != nil {
		t.Fatalf("write enter failed: %v", err)
	}
	if _, err := pt.readUntil("SAID=abc", 3*time.Second); err != nil {
		t.Fatalf("mid-line backspace edit did not produce 'say abc': %v", err)
	}
}

// TestSession_HistoryResetsCursor 验证历史浏览后编辑光标复位到行尾（工单 07）。
// ↑ 回显 "say second" → ← 移动光标 → ↑ 回显 "say first"（光标必须复位行尾）
// → 输入 "X" 应追加到行尾得到 "say firstX"。
func TestSession_HistoryResetsCursor(t *testing.T) {
	pt := newCursorTest(t)
	defer pt.close()

	// 建立两条历史记录
	for i, cmd := range []string{"first", "second"} {
		if err := pt.write("say " + cmd + "\r\n"); err != nil {
			t.Fatalf("write command #%d failed: %v", i+1, err)
		}
		if _, err := pt.readUntil("SAID="+cmd, 3*time.Second); err != nil {
			t.Fatalf("did not see result #%d: %v", i+1, err)
		}
		if _, err := pt.readUntil("PROMPT_END>", 3*time.Second); err != nil {
			t.Fatalf("did not see prompt #%d: %v", i+1, err)
		}
	}

	// ↑ 回显 "say second"（光标在行尾，重绘无移动序列）
	if err := pt.write("\x1b[A"); err != nil {
		t.Fatalf("write up arrow failed: %v", err)
	}
	if _, err := pt.readUntil("PROMPT_END>say second", 3*time.Second); err != nil {
		t.Fatalf("first up arrow should recall 'say second': %v", err)
	}

	// ←：光标移入行中（重绘含 ESC[1D）
	if err := pt.write("\x1b[D"); err != nil {
		t.Fatalf("write left arrow failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[1D", 3*time.Second); err != nil {
		t.Fatalf("did not see redraw after left arrow: %v", err)
	}

	// ↑：回显 "say first"，光标必须复位行尾（重绘无移动序列）
	if err := pt.write("\x1b[A"); err != nil {
		t.Fatalf("write second up arrow failed: %v", err)
	}
	if _, err := pt.readUntil("PROMPT_END>say first", 3*time.Second); err != nil {
		t.Fatalf("second up arrow should recall 'say first': %v", err)
	}

	// 输入 "X"：光标若已复位行尾则追加得 "say firstX"（普通回显），
	// 未复位则会插到行中（如 "say fiXrst"）
	if err := pt.write("X\r\n"); err != nil {
		t.Fatalf("write X+enter failed: %v", err)
	}
	if _, err := pt.readUntil("SAID=firstX", 3*time.Second); err != nil {
		t.Fatalf("cursor was not reset to end after history recall: %v", err)
	}
}

// TestSession_CursorWithIACMixedInput 验证 IAC 序列混入光标编辑时状态保持正确（工单 07）。
// "say hel" + 2 字节 IAC(NOP) + ← + "X" → "say heXl"。
func TestSession_CursorWithIACMixedInput(t *testing.T) {
	pt := newCursorTest(t)
	defer pt.close()

	if err := pt.write("say hel"); err != nil {
		t.Fatalf("write failed: %v", err)
	}
	if _, err := pt.readUntil("say hel", 3*time.Second); err != nil {
		t.Fatalf("did not see echo: %v", err)
	}

	// 混入一个 2 字节 IAC（NOP），服务端静默丢弃，无输出
	if err := pt.write(string([]byte{0xFF, 0xF1})); err != nil {
		t.Fatalf("write IAC NOP failed: %v", err)
	}

	// ←：光标移到 'l' 前
	if err := pt.write("\x1b[D"); err != nil {
		t.Fatalf("write left arrow failed: %v", err)
	}
	if _, err := pt.readUntil("\x1b[1D", 3*time.Second); err != nil {
		t.Fatalf("did not see redraw after left arrow: %v", err)
	}

	// 行中插入 'X' → "say heXl"，光标偏移 1 列
	if err := pt.write("X"); err != nil {
		t.Fatalf("write insert failed: %v", err)
	}
	// 标记必须包含重绘尾部（\x1b[1D），否则服务端 flush 会因客户端
	// 未读完整块而阻塞，后续写入超时
	if _, err := pt.readUntil("\x1b[KPROMPT_END>say heXl\x1b[1D", 3*time.Second); err != nil {
		t.Fatalf("mid-line insert after IAC did not redraw 'say heXl': %v", err)
	}

	if err := pt.write("\r\n"); err != nil {
		t.Fatalf("write enter failed: %v", err)
	}
	if _, err := pt.readUntil("SAID=heXl", 3*time.Second); err != nil {
		t.Fatalf("IAC mixed with cursor editing broke input: %v", err)
	}
}
