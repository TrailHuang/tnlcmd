// Package tnlcmd 提供一个功能完整的 Telnet 命令行程序库
// 支持命令 tab 补全、历史命令管理和上下翻页功能
package tnlcmd

import (
	"github.com/TrailHuang/tnlcmd/internal/cmdline"
	"github.com/TrailHuang/tnlcmd/pkg/types"
)

// CommandHandler 命令处理函数类型
type CommandHandler = types.CommandHandler

// ContextHandler 支持上下文的命令处理函数类型
type ContextHandler = types.ContextHandler

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

// RegisterCommand 注册命令到根模式。
// Start 之后调用会返回错误：命令树在各活动会话间共享且只读。
func (c *CmdLine) RegisterCommand(name, description string, handler CommandHandler, detailedDescription ...string) error {
	return c.CmdLine.RegisterCommand(name, description, handler, detailedDescription...)
}

// RegisterModeCommand 注册命令到指定模式。
// Start 之后调用会返回错误：命令树在各活动会话间共享且只读。
func (c *CmdLine) RegisterModeCommand(modePath string, name, description string, handler CommandHandler, detailedDescription ...string) error {
	return c.CmdLine.RegisterModeCommand(modePath, name, description, handler, detailedDescription...)
}

// RegisterContextCommand 注册支持上下文的命令到根模式。
// handler 可通过 ctx 感知连接关闭/服务停止并提前退出。
// Start 之后调用会返回错误：命令树在各活动会话间共享且只读。
func (c *CmdLine) RegisterContextCommand(name, description string, handler ContextHandler, detailedDescription ...string) error {
	return c.CmdLine.RegisterContextCommand(name, description, handler, detailedDescription...)
}

// CreateMode 创建新的命令模式。
// Start 之后调用会返回错误：模式树在各活动会话间共享且只读。
func (c *CmdLine) CreateMode(modePath string, description string) error {
	return c.CmdLine.CreateMode(modePath, description)
}

// Start 启动命令行服务
func (c *CmdLine) Start() error {
	return c.CmdLine.Start()
}

// Stop 停止命令行服务
func (c *CmdLine) Stop() error {
	return c.CmdLine.Stop()
}

// SetConfig 设置配置项
func (c *CmdLine) SetConfig(key, value string) error {
	return c.CmdLine.SetConfig(key, value)
}

// DefaultConfig 返回默认配置
func DefaultConfig() *Config {
	return &Config{
		Prompt:     "cmdline",
		Port:       2323,
		WelcomeMsg: "Welcome to Command Line Interface!\r\nType '?' for available commands.\r\n",
		MaxHistory: 100,
		PageSize:   24,
	}
}
