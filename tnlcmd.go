// Package tnlcmd 提供一个功能完整的 Telnet 命令行程序库
// 支持命令 tab 补全、历史命令管理和上下翻页功能
package tnlcmd

import (
	"fmt"
	"io"
	"log"

	"github.com/TrailHuang/tnlcmd/internal/cmdline"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandHandler 命令处理函数类型
type CommandHandler = types.CommandHandler

// Config 命令行配置
type Config = types.Config

// CmdLine 命令行接口
type CmdLine struct {
	*cmdline.CmdLine
}

// NewCmdLine 创建新的命令行接口
func NewCmdLine(config *Config) *CmdLine {
	return &CmdLine{
		CmdLine: cmdline.NewCmdLine(config),
	}
}

// RegisterCommand 注册命令到根模式
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler, detailedDescription ...string) {
	c.CmdLine.RegisterCommand(name, description, handler, detailedDescription...)
}

// RegisterModeCommand 注册命令到指定模式
func (c *CmdLine) RegisterModeCommand(modePath string, name, description string, handler CommandHandler, detailedDescription ...string) {
	c.CmdLine.RegisterModeCommand(modePath, name, description, handler, detailedDescription...)
}

// CreateMode 创建新的命令模式
func (c *CmdLine) CreateMode(modePath string, description string) {
	c.CmdLine.CreateMode(modePath, description)
}

// Start 启动命令行服务
func (c *CmdLine) Start() error {
	return c.CmdLine.Start()
}

// Stop 停止命令行服务
func (c *CmdLine) Stop() {
	c.CmdLine.Stop()
}

// SetConfig 设置配置项
func (c *CmdLine) SetConfig(key, value string) {
	c.CmdLine.SetConfig(key, value)
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Prompt:     "cmdline",
		Port:       2323,
		WelcomeMsg: "Welcome to Command Line Interface!\r\nType '?' for available commands.\r\n",
		MaxHistory: 100,
	}
}

// SimpleExample 简单的使用示例
func SimpleExample() {
	// 创建默认配置
	config := DefaultConfig()

	// 创建命令行接口
	cmdline := NewCmdLine(config)

	// 注册一些基本命令
	cmdline.RegisterCommand("help", "Show help information", func(args []string, writer io.Writer) error {
		fmt.Fprintf(writer, "Available commands:\r\n")
		fmt.Fprintf(writer, "  help    - Show this help\r\n")
		fmt.Fprintf(writer, "  version - Show version\r\n")
		fmt.Fprintf(writer, "  exit    - Exit the program\r\n")
		return nil
	})

	cmdline.RegisterCommand("version", "Show version information", func(args []string, writer io.Writer) error {
		fmt.Fprintf(writer, "TNLCMD v1.0.0\r\n")
		return nil
	})

	// 启动服务
	go func() {
		if err := cmdline.Start(); err != nil {
			log.Fatalf("Failed to start cmdline: %v", err)
		}
	}()

	fmt.Printf("TNLCMD server started on port %d\n", config.Port)
	fmt.Println("Press Ctrl+C to stop")

	// 等待中断信号
	select {}
}
