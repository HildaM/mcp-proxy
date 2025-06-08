package adapter

import (
	"encoding/json"
	"os"
)

// Config 是适配器服务器的根配置结构
type Config struct {
	Server    ServerConfig     `json:"server"`    // 适配器服务器自身的配置
	TargetAPI TargetAPIConfig  `json:"targetAPI"` // 目标 HTTP API 的配置
	Tools     []ToolConfig     `json:"tools"`     // 工具定义和映射规则
	Prompts   []PromptConfig   `json:"prompts"`   // 静态提示定义
	Resources []ResourceConfig `json:"resources"` // 静态资源定义
}

// ServerConfig 定义了适配器服务器的监听地址、名称等信息
type ServerConfig struct {
	Addr    string `json:"addr"`    // 监听地址和端口, e.g., ":9091"
	Name    string `json:"name"`    // 服务器名称
	Version string `json:"version"` // 服务器版本
	BaseURL string `json:"baseURL"` // 服务器对外暴露的基础 URL
}

// TargetAPIConfig 定义了被适配器包装的后端 HTTP API 的信息
type TargetAPIConfig struct {
	BaseURL string            `json:"baseURL"` // 目标 API 的基础 URL
	Headers map[string]string `json:"headers"` // 添加到每个请求的固定头部
	Auth    AuthConfig        `json:"auth"`    // 向目标 API 进行身份验证的方式
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

// LoadConfig 从指定的文件路径加载并解析配置
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	// 替换环境变量
	expandedData := os.ExpandEnv(string(data))

	var config Config
	err = json.Unmarshal([]byte(expandedData), &config)
	if err != nil {
		return nil, err
	}
	return &config, nil
}
