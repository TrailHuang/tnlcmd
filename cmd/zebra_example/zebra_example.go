package main

import (
	"context"
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/TrailHuang/tnlcmd"
)

func main() {
	// 创建命令行接口配置
	config := tnlcmd.DefaultConfig()

	// 安全能力示例：最多 1 个并发连接，空闲 5 分钟回收。
	// 注意 IdleTimeout 是 time.Duration（此处亦可经 SetConfig("idletimeout", "5m") 设置）
	config.MaxConnections = 10
	config.IdleTimeout = 5 * time.Minute

	// 密码认证示例（默认空 = 关闭；取消注释即可启用）：
	//config.Password = "secret"
	//config.MaxAuthAttempts = 3 // 密码错误次数上限，0 使用内置默认值 3

	// 命令审计日志示例（默认 false = 关闭；也可经 SetConfig("auditlog", "true") 设置）
	config.AuditLog = true

	// 审计日志落盘示例（默认空 = 输出到 stderr；也可经 SetConfig("auditlogfile", "...") 设置）：
	// 设置后审计日志写入该文件，当天使用原路径，历史日志自动重命名为 <name>_YYYYMMDD.log
	//（如 audit_20261125.log），超过 30 天的历史日志自动清理。
	// 需在 Start 之前设置。
	config.AuditLogFile = "/var/log/tnlcmd/audit.log"

	// 支持通过环境变量 TNLCMD_PORT 覆盖端口，便于多实例并行测试
	if p := os.Getenv("TNLCMD_PORT"); p != "" {
		if port, err := strconv.Atoi(p); err == nil {
			config.Port = port
		}
	}

	// 创建命令行接口
	cmdline := tnlcmd.NewCmdLine(config)
	_ = cmdline.SetConfig("prompt", "test")
	_ = cmdline.SetConfig("welcome", "Welcome to  CLI!\r\nType '?' for available commands.\r\n")
	_ = cmdline.SetConfig("maxhistory", "50")

	// 注册根模式命令（特权EXEC模式）
	rootCommands := []struct {
		name, desc   string
		detailedDesc string
		handler      func([]string) string
	}{
		{name: "show running-config", desc: "Show running system information", detailedDesc: "show configuration\ndisplay running config", handler: showHandler},
		{"show config", "Show running system information", "show configuration\ndisplay system config", showHandler},
		{"ping IP", "Send echo messages", "send echo\ntest connectivity", pingHandler},
		{"clear test1", "Reset functions", "clear test\nreset test1", clearHandler},
		{"clear test2", "Reset functions", "clear test\nreset test2", clearHandler},
		{"debug", "Debugging functions", "debug mode\nenable debugging", debugHandler},
		{"set debug <1-10>", "Debugging functions", "set debug level\nconfigure debug", setValueHandler},
		{"set debug2 <1-10> (on|off)", "Debugging functions", "set debug2\nconfigure debug2", setValueHandler},
		{"set debug info STRING", "Debugging functions", "set debug info\nconfigure debug info", setValueHandler},
		{"set name STRING", "Debugging functions", "set name\nconfigure name", setValueHandler},
		{"set filter-switch (on|off)", "Debugging functions", "set filter\nconfigure filter switch", setValueHandler},
		{"set test [STRRING]", "Debugging functions", "set test\nconfigure test", setValueHandler},
	}

	for _, cmd := range rootCommands {
		var err error
		if cmd.detailedDesc != "" {
			err = cmdline.RegisterCommand(cmd.name, cmd.desc, cmd.handler, cmd.detailedDesc)
		} else {
			err = cmdline.RegisterCommand(cmd.name, cmd.desc, cmd.handler)
		}
		if err != nil {
			log.Fatalf("register root command %q: %v", cmd.name, err)
		}
	}

	// 注册支持上下文的命令（新版 ContextHandler 示例）：
	// handler 内通过 ctx 感知连接关闭/服务停止，可提前取消长时间运行的任务
	if err := cmdline.RegisterContextCommand("run task", "Run a cancellable task (ctx demo)", runTaskHandler); err != nil {
		log.Fatalf("register context command %q: %v", "run task", err)
	}

	// 创建配置模式
	if err := cmdline.CreateMode("configure", "global configuration"); err != nil {
		log.Fatalf("create configure mode: %v", err)
	}

	// 注册配置模式命令
	configCommands := []struct {
		mode, name, desc string
		detailedDesc     string
		handler          func([]string) string
	}{
		{"configure", "router PROTOCOL", "Enable a routing process", "enable routing\nconfigure routing protocol", routerHandler},
		{"configure", "hostname HOSTNAME", "Set system's network name", "set hostname\nconfigure system hostname", hostnameHandler},
		{"configure", "banner BANNER", "Define a login banner", "define banner\nconfigure login banner", bannerHandler},
		{"configure", "set debug3 <1-10>", "Debugging functions", "", setValueHandler},
		{"configure", "set debug4 <1-10> (on|off)", "Debugging functions", "", setValueHandler},
		{"configure", "set debug info2 STRING", "Debugging functions", "", setValueHandler},
		{"configure", "set name2 STRING", "Debugging functions", "", setValueHandler},
		{"configure", "set filter-switch3 (on|off)", "Debugging functions", "", setValueHandler},
		{"configure", "set tes3t [STRRING]", "Debugging functions", "", setValueHandler},
		{"configure", "set policy TEST STRING", "Debugging functions", "", setValueHandler},
	}

	for _, cmd := range configCommands {
		var err error
		if cmd.detailedDesc != "" {
			err = cmdline.RegisterModeCommand(cmd.mode, cmd.name, cmd.desc, cmd.handler, cmd.detailedDesc)
		} else {
			err = cmdline.RegisterModeCommand(cmd.mode, cmd.name, cmd.desc, cmd.handler)
		}
		if err != nil {
			log.Fatalf("register %s command %q: %v", cmd.mode, cmd.name, err)
		}
	}
	// 创建接口配置模式
	if err := cmdline.CreateMode("interface", "interface configuration"); err != nil {
		log.Fatalf("create interface mode: %v", err)
	}

	// 注册接口配置模式命令
	interfaceCommands := []struct {
		mode, name, desc string
		detailedDesc     string
		handler          func([]string) string
	}{
		{"interface", "ip IPADDR MASK", "Interface Internet Protocol config commands", "configure ip\nset interface ip address", ipHandler},
		{"interface", "description TEXT", "Interface specific description", "set description\nconfigure interface description", descriptionHandler},
		{"interface", "shutdown", "Shutdown the selected interface", "shutdown interface\ndisable interface", shutdownHandler},
		{"interface", "no COMMAND", "Negate a command or set its defaults", "negate command\nundo configuration", noHandler},
	}

	for _, cmd := range interfaceCommands {
		var err error
		if cmd.detailedDesc != "" {
			err = cmdline.RegisterModeCommand(cmd.mode, cmd.name, cmd.desc, cmd.handler, cmd.detailedDesc)
		} else {
			err = cmdline.RegisterModeCommand(cmd.mode, cmd.name, cmd.desc, cmd.handler)
		}
		if err != nil {
			log.Fatalf("register %s command %q: %v", cmd.mode, cmd.name, err)
		}
	}

	// 启动命令行服务
	err := cmdline.Start()
	if err != nil {
		log.Fatalf("Failed to start cmdline: %v", err)
	}

	fmt.Printf("Zebra-style CLI started on port %d\n", config.Port)

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down...")

	// 停止命令行服务
	_ = cmdline.Stop()

	fmt.Println("Zebra-style CLI stopped")
}

// 特权EXEC模式命令处理函数
func showHandler(args []string) string {
	var result strings.Builder

	if len(args) == 0 {
		result.WriteString("Available show commands:\r\n")
		result.WriteString("  show version    - Show system version\r\n")
		result.WriteString("  show interfaces - Show interface information\r\n")
		result.WriteString("  show ip route   - Show IP routing table\r\n")
		return result.String()
	}

	switch args[0] {
	case "version":
		result.WriteString("Zebra CLI Version 1.0\r\n")
		result.WriteString("Build date: 2024-01-01\r\n")
	case "interfaces":
		result.WriteString("Interface Status:\r\n")
		result.WriteString("  eth0: UP, 192.168.1.1/24\r\n")
		result.WriteString("  eth1: DOWN, 0.0.0.0/0\r\n")
	case "ip", "route":
		result.WriteString("IP Routing Table:\r\n")
		result.WriteString("  C    192.168.1.0/24 is directly connected, eth0\r\n")
		result.WriteString("  S*   0.0.0.0/0 [1/0] via 192.168.1.254\r\n")
	default:
		result.WriteString(fmt.Sprintf("Unknown show command: %s\r\n", args[0]))
	}

	return result.String()
}

func setValueHandler(args []string) string {
	if len(args) == 0 {
		return "Usage: set <parameter> <value>\r\n"
	}

	return fmt.Sprintf("Set %s to %s\r\n", args[0], strings.Join(args[1:], " "))
}

func pingHandler(args []string) string {
	target := "8.8.8.8"
	if len(args) > 0 {
		target = args[0]
	}

	output := fmt.Sprintf("PING %s: 64 data bytes\r\n"+
		"64 bytes from 8.8.8.8: icmp_seq=0 ttl=57 time=25.3 ms\r\n"+
		"64 bytes from 8.8.8.8: icmp_seq=1 ttl=57 time=24.8 ms\r\n"+
		"--- 8.8.8.8 ping statistics ---\r\n"+
		"2 packets transmitted, 2 packets received, 0%% packet loss\r\n", target)

	return output
}

func clearHandler(args []string) string {
	return "Functions cleared\r\n"
}

// runTaskHandler 新版 ContextHandler 示例：演示通过 ctx 取消长时间运行的任务。
// 对比旧版 pingHandler：ctx 版可在连接关闭（Ctrl+D）或服务 Stop 时提前退出，
// 而旧签名 handler 只能等任务自然跑完。
func runTaskHandler(ctx context.Context, args []string) string {
	steps := 5
	if len(args) > 0 {
		if n, err := strconv.Atoi(args[0]); err == nil && n > 0 && n <= 60 {
			steps = n
		}
	}

	var result strings.Builder
	fmt.Fprintf(&result, "Task started: %d steps (about %d seconds)\r\n", steps, steps)

	for i := 1; i <= steps; i++ {
		select {
		case <-ctx.Done():
			// 连接已关闭或服务正在 Stop，立即中止任务释放资源
			result.WriteString("Task canceled by context\r\n")
			return result.String()
		case <-time.After(1 * time.Second):
			fmt.Fprintf(&result, "  Step %d/%d done\r\n", i, steps)
		}
	}

	result.WriteString("Task completed\r\n")
	return result.String()
}

func debugHandler(args []string) string {
	return "Debugging enabled\r\n"
}

// 全局配置模式命令处理函数
func interfaceHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: interface <interface-name>\r\n"))
		return nil
	}
	writer.Write([]byte(fmt.Sprintf("Configuring interface %s\r\n", args[0])))
	return nil
}

func routerHandler(args []string) string {
	if len(args) == 0 {
		return "Usage: router <protocol>\r\n"
	}
	return fmt.Sprintf("Enabling %s routing\r\n", args[0])
}

func hostnameHandler(args []string) string {
	if len(args) == 0 {
		return "Usage: hostname <name>\r\n"
	}
	return fmt.Sprintf("Hostname set to %s\r\n", args[0])
}

func bannerHandler(args []string) string {
	if len(args) == 0 {
		return "Usage: banner <message>\r\n"
	}
	return "Banner set\r\n"
}

// 接口配置模式命令处理函数
func ipHandler(args []string) string {
	if len(args) < 2 {
		return "Usage: ip address <ip-address> <subnet-mask>\r\n"
	}
	return fmt.Sprintf("IP address %s/%s configured\r\n", args[0], args[1])
}

func descriptionHandler(args []string) string {
	if len(args) == 0 {
		return "Usage: description <text>\r\n"
	}
	return fmt.Sprintf("Description set %s\r\n", args[0])
}

func shutdownHandler(args []string) string {
	return "Interface shutdown\r\n"
}

func noHandler(args []string) string {
	if len(args) == 0 {
		return "Usage: no <command>\r\n"
	}
	return fmt.Sprintf("Command '%s' negated\r\n", strings.Join(args, " "))
}
