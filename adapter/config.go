package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
)

// AdapterProxyConfig 定义了适配器代理服务器的配置
type AdapterProxyConfig struct {
	BaseURL string   `json:"base_url"`          // 代理服务器的基础URL
	Addr    string   `json:"addr"`              // 监听地址和端口
	Name    string   `json:"name"`              // 代理服务器名称
	Version string   `json:"version"`           // 代理服务器版本
	Options *Options `json:"options,omitempty"` // 代理服务器选项
}

// Options 定义了适配器的通用选项
type Options struct {
	PanicIfInvalid bool              `json:"panicIfInvalid,omitempty"` // 如果适配器无效是否panic
	LogEnabled     bool              `json:"log_enabled,omitempty"`    // 是否启用日志
	AuthTokens     []string          `json:"authTokens,omitempty"`     // 认证令牌列表
	ToolFilter     *ToolFilterConfig `json:"toolFilter,omitempty"`     // 工具过滤配置
}

// ToolFilterMode 是工具过滤模式的枚举
type ToolFilterMode string

// 工具过滤模式常量
const (
	ToolFilterModeAllow ToolFilterMode = "allow" // 白名单模式：只允许列表中的工具
	ToolFilterModeBlock ToolFilterMode = "block" // 黑名单模式：阻止列表中的工具
)

// ToolFilterConfig 工具过滤配置
type ToolFilterConfig struct {
	Mode  ToolFilterMode `json:"mode,omitempty"`  // 过滤模式：allow或block
	Tools []string       `json:"tools,omitempty"` // 工具名称列表
}

// AdapterServerConfig 定义了单个适配器服务器的配置
type AdapterServerConfig struct {
	TargetAPI TargetAPIConfig  `json:"targetAPI"`         // 目标 HTTP API 的配置
	Tools     []ToolConfig     `json:"tools"`             // 工具定义和映射规则
	Prompts   []PromptConfig   `json:"prompts"`           // 静态提示定义
	Resources []ResourceConfig `json:"resources"`         // 静态资源定义
	Options   *Options         `json:"options,omitempty"` // 服务器特定选项
}

// TargetAPIConfig 定义了被适配器包装的后端 HTTP API 的信息
type TargetAPIConfig struct {
	BaseURL string            `json:"base_url"`          // 目标 API 的基础 URL
	Headers map[string]string `json:"headers"`           // 添加到每个请求的固定头部
	Auth    AuthConfig        `json:"auth"`              // 向目标 API 进行身份验证的方式
	Timeout time.Duration     `json:"timeout,omitempty"` // 请求超时时间
}

// AuthConfig 定义了身份验证的类型和具体配置
type AuthConfig struct {
	Type   string            `json:"type"`   // 认证类型，如 "apiKey", "bearer"
	Config map[string]string `json:"config"` // 认证参数，如 "key", "value", "location"
}

// ToolConfig 定义了一个 MCP 工具以及如何将其映射到 HTTP 请求
type ToolConfig struct {
	Name         string                 `json:"name"`         // MCP 工具名称
	Description  string                 `json:"description"`  // 工具描述
	InputSchema  map[string]interface{} `json:"inputSchema"`  // 工具的输入参数 JSON Schema
	HTTPMapping  HTTPMapping            `json:"httpMapping"`  // HTTP 请求映射规则
	ErrorMapping map[int]string         `json:"errorMapping"` // HTTP 状态码到错误消息的映射
}

// PromptConfig 定义了一个静态的 MCP 提示
type PromptConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Content     string `json:"content"`
}

// ResourceConfig 定义了一个静态的 MCP 资源
type ResourceConfig struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	ContentType string `json:"contentType"`
	Content     string `json:"content"`
}

// HTTPMapping 定义了从 MCP 工具调用到具体 HTTP 请求的转换规则
type HTTPMapping struct {
	Method           string             `json:"method"`           // HTTP 方法 (e.g., "GET", "POST")
	Path             string             `json:"path"`             // 请求路径, 支持路径参数, e.g., "/users/{userId}"
	ParameterMapping []ParameterMapping `json:"parameterMapping"` // 参数映射规则
	BodyTemplate     *json.RawMessage   `json:"bodyTemplate"`     // (可选) POST/PUT 请求体的 JSON 模板
}

// ParameterMapping 定义了单个 MCP 输入参数如何映射到 HTTP 请求中
type ParameterMapping struct {
	Source   string      `json:"source"`   // MCP 输入参数的名称
	Target   string      `json:"target"`   // HTTP 请求中的参数名称
	In       string      `json:"in"`       // 参数位置: "query", "header", "path"
	Default  interface{} `json:"default"`  // 如果源参数不存在，使用的默认值
	Required bool        `json:"required"` // 该参数是否必需
}

// NewConfig 是新的适配器配置结构体，支持多服务器代理
type NewConfig struct {
	AdapterProxy   *AdapterProxyConfig             `json:"adapter_proxy"`   // 代理服务器配置
	AdapterServers map[string]*AdapterServerConfig `json:"adapter_servers"` // 后端适配器服务器配置映射，键为服务器名称
}

// LoadNewConfig 加载新的配置文件
func LoadNewConfig(filename string) (*NewConfig, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	// 展开环境变量
	expandedData := os.ExpandEnv(string(data))

	var config NewConfig
	err = json.Unmarshal([]byte(expandedData), &config)
	if err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// 验证必需字段
	if config.AdapterProxy == nil {
		return nil, fmt.Errorf("adapter_proxy is required")
	}
	if config.AdapterProxy.BaseURL == "" {
		return nil, fmt.Errorf("adapter_proxy.base_url is required")
	}
	if config.AdapterProxy.Addr == "" {
		return nil, fmt.Errorf("adapter_proxy.addr is required")
	}

	// 应用默认值
	if config.AdapterProxy.Options == nil {
		config.AdapterProxy.Options = &Options{
			PanicIfInvalid: false,
			LogEnabled:     true,
			AuthTokens:     []string{},
			ToolFilter: &ToolFilterConfig{
				Mode:  "block",
				Tools: []string{},
			},
		}
	}

	// 为每个服务器配置应用继承和默认值
	for name, serverConfig := range config.AdapterServers {
		if serverConfig.Options == nil {
			serverConfig.Options = &Options{
				PanicIfInvalid: config.AdapterProxy.Options.PanicIfInvalid,
				LogEnabled:     config.AdapterProxy.Options.LogEnabled,
				AuthTokens:     make([]string, len(config.AdapterProxy.Options.AuthTokens)),
			}
			copy(serverConfig.Options.AuthTokens, config.AdapterProxy.Options.AuthTokens)

			if config.AdapterProxy.Options.ToolFilter != nil {
				serverConfig.Options.ToolFilter = &ToolFilterConfig{
					Mode:  config.AdapterProxy.Options.ToolFilter.Mode,
					Tools: make([]string, len(config.AdapterProxy.Options.ToolFilter.Tools)),
				}
				copy(serverConfig.Options.ToolFilter.Tools, config.AdapterProxy.Options.ToolFilter.Tools)
			}
		} else {
			// 从代理配置继承选项（如果服务器配置中未设置）
			if len(serverConfig.Options.AuthTokens) == 0 && len(config.AdapterProxy.Options.AuthTokens) > 0 {
				serverConfig.Options.AuthTokens = make([]string, len(config.AdapterProxy.Options.AuthTokens))
				copy(serverConfig.Options.AuthTokens, config.AdapterProxy.Options.AuthTokens)
			}

			if serverConfig.Options.ToolFilter == nil && config.AdapterProxy.Options.ToolFilter != nil {
				serverConfig.Options.ToolFilter = &ToolFilterConfig{
					Mode:  config.AdapterProxy.Options.ToolFilter.Mode,
					Tools: make([]string, len(config.AdapterProxy.Options.ToolFilter.Tools)),
				}
				copy(serverConfig.Options.ToolFilter.Tools, config.AdapterProxy.Options.ToolFilter.Tools)
			}
		}

		// 更新映射中的配置
		config.AdapterServers[name] = serverConfig
	}

	return &config, nil
}
