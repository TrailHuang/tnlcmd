package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/TrailHuang/tnlcmd"
)

func main() {
	// 创建命令行接口配置
	config := &tnlcmd.Config{
		Prompt:     "root",
		Port:       2331,
		WelcomeMsg: "Welcome to Zebra-style Router CLI!\r\nType '?' for available commands.\r\n",
		MaxHistory: 50,
	}

	// 创建命令行接口
	cmdline := tnlcmd.NewCmdLine(config)
	cmdline.SetConfig("prompt", "test")
	cmdline.SetConfig("welcome", "Welcome to  CLI!\r\nType '?' for available commands.\r\n")

	// 注册根模式命令（特权EXEC模式）
	rootCommands := []struct {
		name, desc string
		handler    func([]string, io.Writer) error
	}{
		{"show running-config", "Show running system information", showHandler},
		{"show config", "Show running system information", showHandler},
		{"ping IP", "Send echo messages", pingHandler},
		{"clear test1", "Reset functions", clearHandler},
		{"clear test2", "Reset functions", clearHandler},
		{"debug", "Debugging functions", debugHandler},
		{"set debug <1-10>", "Debugging functions", setValueHandler},
		{"set debug2 <1-10> (on|off)", "Debugging functions", setValueHandler},
		{"set debug info STRING", "Debugging functions", setValueHandler},
		{"set name STRING", "Debugging functions", setValueHandler},
		{"set filter-switch (on|off)", "Debugging functions", setValueHandler},
		{"set test [STRRING]", "Debugging functions", setValueHandler},
	}

	for _, cmd := range rootCommands {
		cmdline.RegisterCommand(cmd.name, cmd.desc, cmd.handler)
	}

	// 创建配置模式
	cmdline.CreateMode("configure", "global configuration")

	// 注册配置模式命令
	configCommands := []struct {
		mode, name, desc string
		handler          func([]string, io.Writer) error
	}{
		{"configure", "router PROTOCOL", "Enable a routing process", routerHandler},
		{"configure", "hostname HOSTNAME", "Set system's network name", hostnameHandler},
		{"configure", "banner BANNER", "Define a login banner", bannerHandler},
		{"configure", "set debug3 <1-10>", "Debugging functions", setValueHandler},
		{"configure", "set debug4 <1-10> (on|off)", "Debugging functions", setValueHandler},
		{"configure", "set debug info2 STRING", "Debugging functions", setValueHandler},
		{"configure", "set name2 STRING", "Debugging functions", setValueHandler},
		{"configure", "set filter-switch3 (on|off)", "Debugging functions", setValueHandler},
		{"configure", "set tes3t [STRRING]", "Debugging functions", setValueHandler},
	}

	for _, cmd := range configCommands {
		cmdline.RegisterModeCommand(cmd.mode, cmd.name, cmd.desc, cmd.handler)
	}
	// 创建接口配置模式
	cmdline.CreateMode("interface", "interface configuration")

	// 注册接口配置模式命令
	interfaceCommands := []struct {
		mode, name, desc string
		handler          func([]string, io.Writer) error
	}{
		{"interface", "ip IPADDR MASK", "Interface Internet Protocol config commands", ipHandler},
		{"interface", "description TEXT", "Interface specific description", descriptionHandler},
		{"interface", "shutdown", "Shutdown the selected interface", shutdownHandler},
		{"interface", "no COMMAND", "Negate a command or set its defaults", noHandler},
	}

	for _, cmd := range interfaceCommands {
		cmdline.RegisterModeCommand(cmd.mode, cmd.name, cmd.desc, cmd.handler)
	}

	// 启动命令行服务
	err := cmdline.Start()
	if err != nil {
		log.Fatalf("Failed to start cmdline: %v", err)
	}

	fmt.Printf("Zebra-style CLI started on port %d\n", config.Port)
	fmt.Println("Connect with: telnet localhost 2331")

	// 等待中断信号
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	fmt.Println("\nShutting down...")

	// 停止命令行服务
	err = cmdline.Stop()
	if err != nil {
		log.Printf("Error stopping cmdline: %v", err)
	}

	fmt.Println("Zebra-style CLI stopped")
}

// 特权EXEC模式命令处理函数
func showHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Available show commands:\r\n"))
		writer.Write([]byte("  show version    - Show system version\r\n"))
		writer.Write([]byte("  show interfaces - Show interface information\r\n"))
		writer.Write([]byte("  show ip route   - Show IP routing table\r\n"))
		return nil
	}

	switch args[0] {
	case "version":
		writer.Write([]byte("RouterOS Version 7.8\r\nBuild: 2024-01-20\r\n"))
	case "interfaces":
		writer.Write([]byte("Interface Status:\r\n  eth0: UP, 1000Mbps\r\n  eth1: DOWN, 100Mbps\r\n"))
	case "ip", "route":
		writer.Write([]byte("IP Routing Table:\r\n  C 192.168.1.0/24 is directly connected, eth0\r\n  S 0.0.0.0/0 [1/0] via 192.168.1.1\r\n"))
	default:
		writer.Write([]byte("Unknown show command\r\n"))
	}
	return nil
}

func setValueHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: set <parameter> <value>\r\n"))
		writer.Write([]byte("Available parameters: debug, name\r\n"))
		return nil
	}

	writer.Write([]byte(fmt.Sprintf("arg count %d,  '%v'\r\n", len(args), args)))
	return nil

}

func pingHandler(args []string, writer io.Writer) error {
	target := "8.8.8.8"
	if len(args) > 0 {
		target = args[0]
	}

	output := fmt.Sprintf("PING %s: 64 data bytes\r\n"+
		"64 bytes from 8.8.8.8: icmp_seq=0 ttl=57 time=25.3 ms\r\n"+
		"64 bytes from 8.8.8.8: icmp_seq=1 ttl=57 time=24.8 ms\r\n"+
		"--- 8.8.8.8 ping statistics ---\r\n"+
		"2 packets transmitted, 2 packets received, 0%% packet loss\r\n", target)

	writer.Write([]byte(output))
	return nil
}

func clearHandler(args []string, writer io.Writer) error {
	writer.Write([]byte("Functions cleared\r\n"))
	return nil
}

func debugHandler(args []string, writer io.Writer) error {
	writer.Write([]byte("Debugging enabled\r\n"))
	return nil
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

func routerHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: router <protocol>\r\n"))
		return nil
	}
	writer.Write([]byte(fmt.Sprintf("Enabling %s routing\r\n", args[0])))
	return nil
}

func hostnameHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: hostname <name>\r\n"))
		return nil
	}
	writer.Write([]byte(fmt.Sprintf("Hostname set to %s\r\n", args[0])))
	return nil
}

func bannerHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: banner <message>\r\n"))
		return nil
	}
	writer.Write([]byte("Banner set\r\n"))
	return nil
}

// 接口配置模式命令处理函数
func ipHandler(args []string, writer io.Writer) error {
	if len(args) < 2 {
		writer.Write([]byte("Usage: ip address <ip-address> <subnet-mask>\r\n"))
		return nil
	}
	writer.Write([]byte(fmt.Sprintf("IP address %s/%s configured\r\n", args[0], args[1])))
	return nil
}

func descriptionHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: description <text>\r\n"))
		return nil
	}
	writer.Write([]byte("Description set\r\n"))
	return nil
}

func shutdownHandler(args []string, writer io.Writer) error {
	writer.Write([]byte("Interface shutdown\r\n"))
	return nil
}

func noHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: no <command>\r\n"))
		return nil
	}
	writer.Write([]byte(fmt.Sprintf("Command '%s' negated\r\n", strings.Join(args, " "))))
	return nil
}
