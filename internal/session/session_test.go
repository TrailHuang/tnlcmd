package session

import (
	"bufio"
	"context"
	"io"
	"net"
	"strings"
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
	clientConn, serverConn := net.Pipe()

	config := &types.Config{
		Prompt:     "PROMPT_END>",
		Port:       0,
		WelcomeMsg: "",
		MaxHistory: 10,
		PageSize:   pageSize,
		RootMode:   rootMode,
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
