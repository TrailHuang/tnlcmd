package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/TrailHuang/tnlcmd"
)

func main() {
	// 创建命令行接口配置
	config := &tnlcmd.Config{
		Prompt:     "myapp> ",
		Port:       2324,
		WelcomeMsg: "Welcome to My Application!\r\nType 'help' for available commands.\r\n",
		MaxHistory: 50,
	}

	// 创建命令行接口
	cmdline := tnlcmd.NewCmdLine(config)

	// 注册自定义命令
	cmdline.RegisterCommand("echo", "Echo arguments", echoHandler)
	cmdline.RegisterCommand("time", "Show current time", timeHandler)
	cmdline.RegisterCommand("status", "Show application status", statusHandler)
	cmdline.RegisterCommand("show test1", "Show test1 information", showTest1Handler)
	cmdline.RegisterCommand("show test2", "Show test2 information", showTest2Handler)
	cmdline.RegisterCommand("set test3 <1-100>", "Set test3 parameter", setTest3Handler)

	// 启动命令行服务
	err := cmdline.Start()
	if err != nil {
		log.Fatalf("Failed to start cmdline: %v", err)
	}

	fmt.Printf("Command line interface started on port %d\n", config.Port)
	fmt.Println("Connect with: telnet localhost 2324")

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

	fmt.Println("Command line interface stopped")
}

// echoHandler 回声命令处理函数
func echoHandler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: echo <message>\r\n"))
		return nil
	}

	message := fmt.Sprintf("Echo: %s\r\n", strings.Join(args, " "))
	writer.Write([]byte(message))
	return nil
}

// timeHandler 时间命令处理函数
func timeHandler(args []string, writer io.Writer) error {
	currentTime := time.Now().Format("2006-01-02 15:04:05")
	message := fmt.Sprintf("Current time: %s\r\n", currentTime)
	writer.Write([]byte(message))
	return nil
}

// statusHandler 状态命令处理函数
func statusHandler(args []string, writer io.Writer) error {
	status := `Application Status:
  Version: 1.0.0
  Uptime:  Running
  Connections: Active
`
	writer.Write([]byte(status))
	return nil
}

// showTest1Handler 显示test1信息
func showTest1Handler(args []string, writer io.Writer) error {
	message := fmt.Sprintf("Test1 Information:\r\n")
	message += fmt.Sprintf("  Name: Test Configuration 1\r\n")
	message += fmt.Sprintf("  Value: %s\r\n", strings.Join(args, " "))
	writer.Write([]byte(message))
	return nil
}

// showTest2Handler 显示test2信息
func showTest2Handler(args []string, writer io.Writer) error {
	message := fmt.Sprintf("Test2 Information:\r\n")
	message += fmt.Sprintf("  Name: Test Configuration 2\r\n")
	message += fmt.Sprintf("  Value: %s\r\n", strings.Join(args, " "))
	writer.Write([]byte(message))
	return nil
}

// setTest3Handler 设置test3参数
func setTest3Handler(args []string, writer io.Writer) error {
	if len(args) == 0 {
		writer.Write([]byte("Usage: set test3 <value>\r\n"))
		writer.Write([]byte("Example: set test3 50\r\n"))
		return nil
	}

	value := args[0]
	message := fmt.Sprintf("Test3 parameter set to: %s\r\n", value)
	writer.Write([]byte(message))
	return nil
}
