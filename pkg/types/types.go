// Package types 定义 TNLCMD 库的公共类型
package types

// CommandHandler 命令处理函数类型
type CommandHandler func(args []string) string

// CommandInfo 命令信息
type CommandInfo struct {
	Name        string
	Description string
	Handler     CommandHandler
}

// CommandNodeType 命令节点类型
type CommandNodeType int

const (
	NodeTypeCommand    CommandNodeType = iota // 命令节点
	NodeTypeOptional                          // 可选参数节点 []
	NodeTypeEnum                              // 枚举参数节点 ()
	NodeTypeNum                               // 数值范围节点 <>
	NodeTypeString                            // 字符串参数节点（大写字母）
	NodeTypeModeSwitch                        // 视图切换节点
	NodeTypeExit                              // 退出节点
)

// Config 命令行配置
type Config struct {
	Prompt     string
	Port       int
	WelcomeMsg string
	MaxHistory int
	RootMode   interface{} // 使用 interface{} 避免循环导入
}
