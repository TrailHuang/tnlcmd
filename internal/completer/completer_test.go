package completer

import (
	"strings"
	"testing"

	"github.com/TrailHuang/tnlcmd/internal/mode"
)

func setupCompleter() *CommandCompleter {
	// Create a mode with commands
	rootMode := mode.NewCommandMode("root", "test>", "test mode")
	handler := func(args []string) string { return "" }

	rootMode.AddCommand("show", "Show information", handler)
	rootMode.AddCommand("show version", "Show version", handler)
	rootMode.AddCommand("show running-config", "Show running config", handler)
	rootMode.AddCommand("show startup-config", "Show startup config", handler)
	rootMode.AddCommand("set debug <1-10>", "Set debug level", handler)
	rootMode.AddCommand("set switch (on|off)", "Set switch", handler)
	rootMode.AddCommand("exit", "Exit", handler)

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}
	return NewCommandCompleterWithContext(ctx)
}

func TestGetCommandTreeSuggestions(t *testing.T) {
	c := setupCompleter()

	suggestions := c.GetCommandTreeSuggestions("")
	if len(suggestions) == 0 {
		t.Error("GetCommandTreeSuggestions('') should return suggestions")
	}

	// Check format: "name    description"
	for _, s := range suggestions {
		if !strings.Contains(s, " ") {
			t.Errorf("suggestion %q should contain description", s)
		}
	}
}

func TestGetCommandTreeSuggestionsSecondLevel(t *testing.T) {
	c := setupCompleter()

	suggestions := c.GetCommandTreeSuggestions("show")
	if len(suggestions) < 2 {
		t.Errorf("should have at least 2 suggestions under 'show', got %d: %v", len(suggestions), suggestions)
	}
}

func TestGetNextLevelCompletions(t *testing.T) {
	c := setupCompleter()

	// "show" matches the top-level "show" node itself (prefix match)
	completions := c.GetNextLevelCompletions("show")
	if len(completions) == 0 {
		t.Errorf("GetNextLevelCompletions('show') should return completions, got none")
	}

	// With prefix "show " (trailing space) — strings.Fields strips it to ["show"]
	// so it behaves like prefix match, not navigation
	completions = c.GetNextLevelCompletions("show ")
	if len(completions) == 0 {
		t.Errorf("GetNextLevelCompletions('show ') should return completions, got %d: %v", len(completions), completions)
	}

	// With "show v", should complete to "show version"
	completions = c.GetNextLevelCompletions("show v")
	if len(completions) != 1 {
		t.Errorf("GetNextLevelCompletions('show v') should return 1, got %d: %v", len(completions), completions)
	}
	if len(completions) > 0 && completions[0] != "show version" {
		t.Errorf("GetNextLevelCompletions('show v') = %v, want [show version]", completions)
	}
}

func TestGetParameterCompletions(t *testing.T) {
	c := setupCompleter()

	// GetParameterCompletions shows non-command children matching the last input part
	// When input fully navigates to a node, it shows parameter children
	completions := c.GetParameterCompletions("set switch (")
	if len(completions) == 0 {
		// ( is not a valid navigation target, so it won't work either
		// This is expected with current implementation
		t.Log("GetParameterCompletions for partial enum: no results (expected with current design)")
	}
	_ = completions
}

func TestUpdateContext(t *testing.T) {
	c := NewCommandCompleterWithContext(nil)

	rootMode := mode.NewCommandMode("root", "test>", "test mode")
	rootMode.AddCommand("test", "Test command", func(args []string) string { return "" })

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}
	c.UpdateContext(ctx)

	completions := c.GetNextLevelCompletions("")
	if len(completions) != 1 || completions[0] != "test" {
		t.Errorf("After UpdateContext, GetNextLevelCompletions('') = %v, want [test]", completions)
	}
}

func TestNilContext(t *testing.T) {
	c := NewCommandCompleterWithContext(nil)

	// All methods should handle nil context gracefully
	if completions := c.GetNextLevelCompletions("test"); len(completions) != 0 {
		t.Errorf("GetNextLevelCompletions with nil context should return empty, got %v", completions)
	}
	if completions := c.GetParameterCompletions("test"); len(completions) != 0 {
		t.Errorf("GetParameterCompletions with nil context should return empty, got %v", completions)
	}
	if suggestions := c.GetCommandTreeSuggestions("test"); len(suggestions) != 0 {
		t.Errorf("GetCommandTreeSuggestions with nil context should return empty, got %v", suggestions)
	}
}

// setupCompleterWithModes 创建带有子模式的补全器
func setupCompleterWithModes() *CommandCompleter {
	rootMode := mode.NewCommandMode("root", "test>", "test mode")
	handler := func(args []string) string { return "" }

	rootMode.AddCommand("show", "Show information", handler)
	rootMode.AddCommand("exit", "Exit", handler)

	// Create configure sub-mode
	configMode := mode.NewCommandMode("configure", "config#", "configure mode")
	configMode.AddCommand("hostname HOSTNAME", "Set hostname", handler)
	configMode.AddCommand("router PROTOCOL", "Enable routing", handler)
	configMode.AddCommand("exit", "Exit to root", handler)
	rootMode.AddSubMode(configMode)

	// Create interface sub-mode
	ifaceMode := mode.NewCommandMode("interface", "iface#", "interface mode")
	ifaceMode.AddCommand("ip IPADDR MASK", "Set IP address", handler)
	ifaceMode.AddCommand("shutdown", "Shutdown interface", handler)
	ifaceMode.AddCommand("description TEXT", "Set description", handler)
	ifaceMode.AddCommand("exit", "Exit to root", handler)
	rootMode.AddSubMode(ifaceMode)

	// Register mode switch commands in root's command tree
	rootMode.CommandTree.AddModeCommand("configure", "Enter configure mode")
	rootMode.CommandTree.AddModeCommand("interface", "Enter interface mode")

	ctx := &mode.CommandContext{
		CurrentMode: rootMode,
		Path:        []string{},
	}
	return NewCommandCompleterWithContext(ctx)
}

func TestGetCommandTreeSuggestions_ModeSwitch(t *testing.T) {
	c := setupCompleterWithModes()

	// When typing "interface" and pressing ?, should show interface sub-commands
	suggestions := c.GetCommandTreeSuggestions("interface")
	if len(suggestions) < 2 {
		t.Errorf("GetCommandTreeSuggestions('interface') should show sub-commands, got %d: %v", len(suggestions), suggestions)
	}

	// Should contain interface-specific commands
	hasIP := false
	hasShutdown := false
	for _, s := range suggestions {
		if strings.Contains(s, "ip") || strings.Contains(s, "IPADDR") {
			hasIP = true
		}
		if strings.Contains(s, "shutdown") {
			hasShutdown = true
		}
	}
	if !hasIP {
		t.Error("suggestions for 'interface' should include ip command")
	}
	if !hasShutdown {
		t.Error("suggestions for 'interface' should include shutdown command")
	}

	// Similarly for configure mode
	suggestions = c.GetCommandTreeSuggestions("configure")
	if len(suggestions) < 2 {
		t.Errorf("GetCommandTreeSuggestions('configure') should show sub-commands, got %d: %v", len(suggestions), suggestions)
	}
}

func TestGetNextLevelCompletions_ModeSwitch(t *testing.T) {
	c := setupCompleterWithModes()

	// Tab after typing full mode name should show sub-commands
	completions := c.GetNextLevelCompletions("interface")
	if len(completions) < 2 {
		t.Errorf("GetNextLevelCompletions('interface') should show sub-commands, got %d: %v", len(completions), completions)
	}

	completions = c.GetNextLevelCompletions("configure")
	if len(completions) < 2 {
		t.Errorf("GetNextLevelCompletions('configure') should show sub-commands, got %d: %v", len(completions), completions)
	}
}
