package debuglog

import (
	"bytes"
	"os"
	"strings"
	"testing"
)

// capture 返回一个缓冲写入器，并在测试结束时恢复关闭状态。
func capture(t *testing.T) *bytes.Buffer {
	t.Helper()
	buf := &bytes.Buffer{}
	t.Cleanup(func() {
		configure("off", "")
	})
	return buf
}

// TestConfigureOff 验证默认与 off 时关闭且不输出日志。
func TestConfigureOff(t *testing.T) {
	buf := capture(t)
	configureWithWriter("off", buf)
	configureWithWriter("", buf)
	configureWithWriter("info", buf) // 不支持的级别视为关闭

	if Enabled() {
		t.Error("debuglog should be disabled for off/empty/unsupported levels")
	}
	Debug("session", "read_line", "n", 1)
	Trace("session", "trace_event")
	if buf.Len() != 0 {
		t.Errorf("disabled debuglog must not write output, got %q", buf.String())
	}
}

// TestConfigureDebug 验证 debug 级别输出 Debug、过滤 Trace。
func TestConfigureDebug(t *testing.T) {
	buf := capture(t)
	configureWithWriter("debug", buf)

	if !Enabled() {
		t.Fatal("debuglog should be enabled for level debug")
	}
	Debug("server", "conn_accepted", "remote", "1.2.3.4:5")
	Trace("server", "trace_event")
	got := buf.String()
	for _, key := range []string{"level=DEBUG", "conn_accepted", "component=server", "remote=1.2.3.4:5"} {
		if !strings.Contains(got, key) {
			t.Errorf("debug output missing %q, got %q", key, got)
		}
	}
	if strings.Contains(got, "trace_event") {
		t.Errorf("trace event must be filtered at debug level, got %q", got)
	}
}

// TestConfigureTrace 验证 trace 级别同时输出 Debug 与 Trace。
func TestConfigureTrace(t *testing.T) {
	buf := capture(t)
	configureWithWriter("trace", buf)

	if !Enabled() {
		t.Fatal("debuglog should be enabled for level trace")
	}
	Debug("session", "debug_event")
	Trace("session", "trace_event", "n", 2)
	got := buf.String()
	if !strings.Contains(got, "debug_event") {
		t.Errorf("trace level must include Debug events, got %q", got)
	}
	if !strings.Contains(got, "trace_event") || !strings.Contains(got, "n=2") {
		t.Errorf("trace level must include Trace events with fields, got %q", got)
	}
}

// TestConfigureDebugFile 验证文件输出目标（集成工单 08 的边界：文件权限仅属主可写）。
func TestConfigureDebugFile(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/debug.log"
	configure("debug", path)
	defer configure("off", "")

	Debug("session", "file_event", "n", 1)
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("debug log file not created: %v", err)
	}
	if !strings.Contains(string(got), "file_event") {
		t.Errorf("debug log file missing event, got %q", got)
	}
}

// TestUnknownLevelTreatedAsOff 验证大小写不敏感的级别解析与未知值关闭语义。
func TestUnknownLevelTreatedAsOff(t *testing.T) {
	buf := capture(t)
	configureWithWriter("DEBUG", buf)
	if !Enabled() {
		t.Error("level parsing should be case-insensitive")
	}
	configureWithWriter("warn", buf)
	if Enabled() {
		t.Error("unsupported level 'warn' must be treated as off")
	}
	configureWithWriter("error", buf)
	if Enabled() {
		t.Error("unsupported level 'error' must be treated as off")
	}
	configureWithWriter("TRACE", buf)
	if !Enabled() {
		t.Error("level parsing should be case-insensitive")
	}
	if buf.Len() != 0 {
		t.Errorf("reconfiguration must not write output, got %q", buf.String())
	}
}
