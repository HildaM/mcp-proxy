// config.go 文件定义了程序配置的数据结构和配置加载逻辑。
package main

import (
	"errors"
	"time"

	"github.com/TBXark/confstore"
	"github.com/TBXark/optional-go"
)

// StdioMCPClientConfig 定义了标准输入/输出类型MCP客户端的配置
type StdioMCPClientConfig struct {
	Command string            `json:"command"` // 要执行的命令
	Env     map[string]string `json:"env"`     // 命令的环境变量
	Args    []string          `json:"args"`    // 命令的参数
}

// SSEMCPClientConfig 定义了SSE(服务器发送事件)类型MCP客户端的配置
type SSEMCPClientConfig struct {
	URL     string            `json:"url"`     // SSE服务器的URL
	Headers map[string]string `json:"headers"` // 请求头
}

// StreamableMCPClientConfig 定义了可流式HTTP类型MCP客户端的配置
type StreamableMCPClientConfig struct {
	URL     string            `json:"url"`     // HTTP服务器的URL
	Headers map[string]string `json:"headers"` // 请求头
	Timeout time.Duration     `json:"timeout"` // 请求超时时间
}

// MCPClientType 是MCP客户端类型的枚举
type MCPClientType string

// MCP客户端类型常量
const (
	MCPClientTypeStdio      MCPClientType = "stdio"           // 标准输入/输出类型
	MCPClientTypeSSE        MCPClientType = "sse"             // 服务器发送事件类型
	MCPClientTypeStreamable MCPClientType = "streamable-http" // 可流式HTTP类型
)

// ToolFilterMode 是工具过滤模式的枚举
type ToolFilterMode string

// 工具过滤模式常量
const (
	ToolFilterModeAllow ToolFilterMode = "allow" // 白名单模式：只允许列表中的工具
	ToolFilterModeBlock ToolFilterMode = "block" // 黑名单模式：阻止列表中的工具
)

// ToolFilterConfig 定义了工具过滤的配置
type ToolFilterConfig struct {
	Mode ToolFilterMode `json:"mode,omitempty"` // 过滤模式：allow或block
	List []string       `json:"list,omitempty"` // 工具名称列表
}

// Options 定义了MCP客户端或代理服务器的通用选项
type Options struct {
	PanicIfInvalid optional.Field[bool] `json:"panicIfInvalid,omitempty"` // 如果客户端无效是否panic
	LogEnabled     optional.Field[bool] `json:"logEnabled,omitempty"`     // 是否启用日志
	AuthTokens     []string             `json:"authTokens,omitempty"`     // 认证令牌列表
	ToolFilter     *ToolFilterConfig    `json:"toolFilter,omitempty"`     // 工具过滤配置
}

// MCPProxyConfig 定义了MCP代理服务器的配置
type MCPProxyConfig struct {
	BaseURL string   `json:"baseURL"`           // 代理服务器的基础URL
	Addr    string   `json:"addr"`              // 监听地址和端口
	Name    string   `json:"name"`              // 代理服务器名称
	Version string   `json:"version"`           // 代理服务器版本
	Options *Options `json:"options,omitempty"` // 代理服务器选项
}

// MCPClientConfig 定义了MCP客户端的配置
// 它采用了扁平化的设计，不同类型的客户端共用一个结构体，
// 但在实际使用时会根据填充的字段来确定具体类型
type MCPClientConfig struct {
	TransportType MCPClientType `json:"transportType,omitempty"` // 传输类型，可选

	// Stdio类型的配置字段
	Command string            `json:"command,omitempty"` // 命令
	Args    []string          `json:"args,omitempty"`    // 参数
	Env     map[string]string `json:"env,omitempty"`     // 环境变量

	// SSE或Streamable HTTP类型的配置字段
	URL     string            `json:"url,omitempty"`     // URL
	Headers map[string]string `json:"headers,omitempty"` // 请求头
	Timeout time.Duration     `json:"timeout,omitempty"` // 超时时间，仅用于Streamable HTTP

	Options *Options `json:"options,omitempty"` // 客户端选项
}

// parseMCPClientConfig 根据配置信息确定MCP客户端的具体类型，并返回相应的配置结构体
// 它会根据填充的字段自动推断客户端类型，无需显式指定TransportType
func parseMCPClientConfig(conf *MCPClientConfig) (any, error) {
	// 如果Command字段存在或显式指定了Stdio类型
	if conf.Command != "" || conf.TransportType == MCPClientTypeStdio {
		if conf.Command == "" {
			return nil, errors.New("command is required for stdio transport")
		}
		return &StdioMCPClientConfig{
			Command: conf.Command,
			Env:     conf.Env,
			Args:    conf.Args,
		}, nil
	}
	// 如果URL字段存在
	if conf.URL != "" {
		// 根据TransportType区分是Streamable HTTP还是SSE
		if conf.TransportType == MCPClientTypeStreamable {
			return &StreamableMCPClientConfig{
				URL:     conf.URL,
				Headers: conf.Headers,
				Timeout: conf.Timeout,
			}, nil
		} else {
			// 默认为SSE类型
			return &SSEMCPClientConfig{
				URL:     conf.URL,
				Headers: conf.Headers,
			}, nil
		}
	}
	return nil, errors.New("invalid server type")
}

// Config 是整个应用程序的配置结构体
type Config struct {
	McpProxy   *MCPProxyConfig             `json:"mcpProxy"`   // 代理服务器配置
	McpServers map[string]*MCPClientConfig `json:"mcpServers"` // 后端服务器配置映射，键为服务器名称
}

// load 从指定的路径加载配置文件
// 路径可以是本地文件路径或HTTP(S) URL
func load(path string) (*Config, error) {
	// 使用confstore库加载配置，它支持从文件或URL加载
	conf, err := confstore.Load[Config](path)
	if err != nil {
		return nil, err
	}

	// 确保必须的配置项存在
	if conf.McpProxy == nil {
		return nil, errors.New("mcpProxy is required")
	}

	// 为代理服务器设置默认选项
	if conf.McpProxy.Options == nil {
		conf.McpProxy.Options = &Options{}
	}

	// 遍历所有后端服务器配置，应用默认值和继承规则
	for _, clientConfig := range conf.McpServers {
		// 如果客户端没有设置选项，创建一个空的选项对象
		if clientConfig.Options == nil {
			clientConfig.Options = &Options{}
		}
		// 认证令牌继承：如果客户端没有设置认证令牌，使用代理的全局令牌
		if clientConfig.Options.AuthTokens == nil {
			clientConfig.Options.AuthTokens = conf.McpProxy.Options.AuthTokens
		}
		// PanicIfInvalid继承：如果客户端没有显式设置此选项，继承代理的设置
		if !clientConfig.Options.PanicIfInvalid.Present() {
			clientConfig.Options.PanicIfInvalid = conf.McpProxy.Options.PanicIfInvalid
		}
		// LogEnabled继承：如果客户端没有显式设置此选项，继承代理的设置
		if !clientConfig.Options.LogEnabled.Present() {
			clientConfig.Options.LogEnabled = conf.McpProxy.Options.LogEnabled
		}
	}

	return conf, nil
}
