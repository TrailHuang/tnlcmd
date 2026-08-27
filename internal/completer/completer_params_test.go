package completer

import (
	"strings"
	"testing"

	"github.com/TrailHuang/tnlcmd/internal/mode"
)

// setupCompleterWithParams 构造一个包含必选参数和可选参数的命令树，
// 用于验证 ? 帮助的多级参数提示语义。
//
// 命令结构：
//
//	set policy TEST STRING      两个必选字符串参数
//	set policy2 TEST STRING [OPT] 两个必选参数 + 一个可选参数
func setupCompleterWithParams() *CommandCompleter {
	rootMode := mode.NewCommandMode("configure", "CONF#", "configure mode")
	handler := func(args []string) string { return "" }

	rootMode.AddCommand("set policy TEST STRING", "Debugging functions", handler)
	rootMode.AddCommand("set policy2 TEST STRING [OPT]", "Debugging functions", handler)

	return NewCommandCompleterWithContext(&mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	})
}

func TestGetCommandTreeSuggestions_RequiredParam(t *testing.T) {
	c := setupCompleterWithParams()

	// 未填任何参数时，提示第一个必选参数 TEST
	got := c.GetCommandTreeSuggestions("set policy")
	if len(got) != 1 || !strings.Contains(got[0], "TEST") {
		t.Errorf(`GetCommandTreeSuggestions("set policy") = %v, want [TEST]`, got)
	}

	// 已填第一个必选参数后，应提示第二个必选参数 STRING，而不是 <cr>
	got = c.GetCommandTreeSuggestions("set policy df")
	if len(got) != 1 || !strings.Contains(got[0], "STRING") {
		t.Errorf(`GetCommandTreeSuggestions("set policy df") = %v, want [STRING]`, got)
	}
	if strings.Contains(strings.Join(got, " "), "<cr>") {
		t.Errorf(`GetCommandTreeSuggestions("set policy df") should NOT include <cr> (required param missing), got %v`, got)
	}
}

func TestGetCommandTreeSuggestions_OptionalParamAndCR(t *testing.T) {
	c := setupCompleterWithParams()

	// 两个必选参数都已填，仍剩可选参数时，应同时提示可选参数和 <cr>
	got := c.GetCommandTreeSuggestions("set policy2 df str1")
	joined := strings.Join(got, " ")
	if !strings.Contains(joined, "OPT") {
		t.Errorf(`GetCommandTreeSuggestions("set policy2 df str1") = %v, want to include OPT`, got)
	}
	if !strings.Contains(joined, "<cr>") {
		t.Errorf(`GetCommandTreeSuggestions("set policy2 df str1") = %v, want to include <cr> (executable)`, got)
	}
}

func TestGetCommandTreeSuggestions_CompleteParamCR(t *testing.T) {
	c := setupCompleterWithParams()

	// 两个必选参数都已填（无可选参数），此时命令完整可执行，
	// 无更多候选，session 层据此提示 <cr>
	got := c.GetCommandTreeSuggestions("set policy df str1")
	if len(got) != 0 {
		t.Errorf(`GetCommandTreeSuggestions("set policy df str1") = %v, want [] (command complete)`, got)
	}
}
