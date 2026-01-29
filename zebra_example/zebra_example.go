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

	// 注册根模式命令（特权EXEC模式）
	cmdline.RegisterCommand("show running-config", "Show running system information", showHandler)
	cmdline.RegisterCommand("show config", "Show running system information", showHandler)
	cmdline.RegisterCommand("ping IP", "Send echo messages", pingHandler)
	cmdline.RegisterCommand("clear test1", "Reset functions", clearHandler)
	cmdline.RegisterCommand("clear test2", "Reset functions", clearHandler)
	cmdline.RegisterCommand("debug", "Debugging functions", debugHandler)
	cmdline.RegisterCommand("set debug <1-10>", "Debugging functions", setValueHandler)
	cmdline.RegisterCommand("set debug2 <1-10> (on|off)", "Debugging functions", setValueHandler)
	cmdline.RegisterCommand("set debug info STRING", "Debugging functions", setValueHandler)
	cmdline.RegisterCommand("set name STRING", "Debugging functions", setValueHandler)
	cmdline.RegisterCommand("set filter-switch (on|off)", "Debugging functions", setValueHandler)
	cmdline.RegisterCommand("set test [STRRING]", "Debugging functions", setValueHandler)

	// 创建配置模式
	cmdline.CreateMode("configure", "global configuration")

	// 注册配置模式命令（configure 命令应该作为模式切换命令自动处理）
	cmdline.RegisterModeCommand("configure", "interface", "Select an interface to configure", interfaceHandler)
	cmdline.RegisterModeCommand("configure", "router", "Enable a routing process", routerHandler)
	cmdline.RegisterModeCommand("configure", "hostname", "Set system's network name", hostnameHandler)
	cmdline.RegisterModeCommand("configure", "banner", "Define a login banner", bannerHandler)
	cmdline.RegisterModeCommand("configure", "set debug <1-10>", "Debugging functions", setValueHandler)
	cmdline.RegisterModeCommand("configure", "set debug2 <1-10> (on|off)", "Debugging functions", setValueHandler)
	cmdline.RegisterModeCommand("configure", "set debug info STRING", "Debugging functions", setValueHandler)
	cmdline.RegisterModeCommand("configure", "set name STRING", "Debugging functions", setValueHandler)
	cmdline.RegisterModeCommand("configure", "set filter-switch (on|off)", "Debugging functions", setValueHandler)
	cmdline.RegisterModeCommand("configure", "set test [STRRING]", "Debugging functions", setValueHandler)
	// 创建接口配置模式
	cmdline.CreateMode("interface", "interface configuration")

	// 注册接口配置模式命令
	cmdline.RegisterModeCommand("interface", "ip", "Interface Internet Protocol config commands", ipHandler)
	cmdline.RegisterModeCommand("interface", "description", "Interface specific description", descriptionHandler)
	cmdline.RegisterModeCommand("interface", "shutdown", "Shutdown the selected interface", shutdownHandler)
	cmdline.RegisterModeCommand("interface", "no", "Negate a command or set its defaults", noHandler)

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
		writer.Write([]byte("RouterOS Version 7.8\r\n"))
		writer.Write([]byte("Build: 2024-01-20\r\n"))
	case "interfaces":
		writer.Write([]byte("Interface Status:\r\n"))
		writer.Write([]byte("  eth0: UP, 1000Mbps\r\n"))
		writer.Write([]byte("  eth1: DOWN, 100Mbps\r\n"))
	case "ip", "route":
		writer.Write([]byte("IP Routing Table:\r\n"))
		writer.Write([]byte("  C 192.168.1.0/24 is directly connected, eth0\r\n"))
		writer.Write([]byte("  S 0.0.0.0/0 [1/0] via 192.168.1.1\r\n"))
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
	writer.Write([]byte(fmt.Sprintf("PING %s: 64 data bytes\r\n", target)))
	writer.Write([]byte("64 bytes from 8.8.8.8: icmp_seq=0 ttl=57 time=25.3 ms\r\n"))
	writer.Write([]byte("64 bytes from 8.8.8.8: icmp_seq=1 ttl=57 time=24.8 ms\r\n"))
	writer.Write([]byte("--- 8.8.8.8 ping statistics ---\r\n"))
	writer.Write([]byte("2 packets transmitted, 2 packets received, 0% packet loss\r\n"))
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
