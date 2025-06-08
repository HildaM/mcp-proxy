package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// AdapterInstance 表示一个适配器实例
type AdapterInstance struct {
	Name      string
	Client    *http.Client
	TargetAPI TargetAPIConfig
	Tools     []ToolConfig
	Prompts   []PromptConfig
	Resources []ResourceConfig
	Options   *Options
}

// AddToMCPServer 将适配器实例的功能添加到MCP服务器
func (ai *AdapterInstance) AddToMCPServer(ctx context.Context, mcpServer *server.MCPServer) error {
	return ai.AddToMCPServerWithFilter(ctx, mcpServer, nil)
}

// AddToMCPServerWithFilter 将适配器实例的功能添加到MCP服务器，支持工具过滤
func (ai *AdapterInstance) AddToMCPServerWithFilter(ctx context.Context, mcpServer *server.MCPServer, shouldFilterFunc func(toolName string) bool) error {
	// 添加工具
	for _, toolConfig := range ai.Tools {
		// 检查是否需要过滤此工具
		if shouldFilterFunc != nil && shouldFilterFunc(toolConfig.Name) {
			continue
		}

		// 创建工具处理器
		toolHandler := &ToolHandler{
			ToolConfig:      toolConfig,
			TargetAPIConfig: ai.TargetAPI,
			Client:          ai.Client,
		}

		// 解析输入模式
		inputSchema := mcp.ToolInputSchema{}
		if b, err := json.Marshal(toolConfig.InputSchema); err == nil {
			if err := json.Unmarshal(b, &inputSchema); err != nil {
				return fmt.Errorf("failed to unmarshal input schema for tool %s: %w", toolConfig.Name, err)
			}
		} else {
			return fmt.Errorf("failed to marshal input schema for tool %s: %w", toolConfig.Name, err)
		}

		// 添加工具到MCP服务器
		tool := mcp.Tool{
			Name:        toolConfig.Name,
			Description: toolConfig.Description,
			InputSchema: inputSchema,
		}
		log.Printf("Registering tool from adapter %s: %s", ai.Name, tool.Name)
		mcpServer.AddTool(tool, toolHandler.Handle)
	}

	// 添加提示
	for _, promptConfig := range ai.Prompts {
		prompt := mcp.Prompt{
			Name:        promptConfig.Name,
			Description: promptConfig.Description,
		}
		log.Printf("Registering prompt from adapter %s: %s", ai.Name, prompt.Name)
		mcpServer.AddPrompt(prompt, func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			return &mcp.GetPromptResult{
				Description: promptConfig.Description,
				Messages: []mcp.PromptMessage{
					{
						Role:    mcp.RoleAssistant,
						Content: mcp.TextContent{Text: promptConfig.Content},
					},
				},
			}, nil
		})
	}

	// 添加资源
	for _, resourceConfig := range ai.Resources {
		resource := mcp.Resource{
			Name:        resourceConfig.Name,
			Description: resourceConfig.Description,
		}
		log.Printf("Registering resource from adapter %s: %s", ai.Name, resource.Name)
		mcpServer.AddResource(resource, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      resourceConfig.Name,
					MIMEType: resourceConfig.ContentType,
					Text:     resourceConfig.Content,
				},
			}, nil
		})
	}

	return nil
}

// MultiAdapter 管理多个适配器服务器的结构
type MultiAdapter struct {
	config   *NewConfig
	adapters map[string]*AdapterInstance // 服务器名称到适配器的映射
}

// NewMultiAdapter 创建一个新的多适配器实例
func NewMultiAdapter(configPath string) (*MultiAdapter, error) {
	config, err := LoadNewConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load config: %w", err)
	}

	adapters := make(map[string]*AdapterInstance)

	// 为每个配置的服务器创建适配器
	for name, serverConfig := range config.AdapterServers {
		adapter, err := NewAdapterFromServerConfig(name, serverConfig)
		if err != nil {
			if serverConfig.Options.PanicIfInvalid {
				return nil, fmt.Errorf("failed to create adapter for server '%s': %w", name, err)
			}
			log.Printf("Warning: failed to create adapter for server '%s': %v", name, err)
			continue
		}
		adapters[name] = adapter
		log.Printf("Created adapter for server: %s", name)
	}

	return &MultiAdapter{
		config:   config,
		adapters: adapters,
	}, nil
}

// NewAdapterFromServerConfig 从服务器配置创建适配器
func NewAdapterFromServerConfig(name string, serverConfig *AdapterServerConfig) (*AdapterInstance, error) {
	// 创建HTTP客户端（禁用连接缓存）
	client := &http.Client{
		Timeout: serverConfig.TargetAPI.Timeout,
		Transport: &http.Transport{
			DisableKeepAlives: true,
		},
	}

	// 创建适配器
	adapter := &AdapterInstance{
		Name:      name,
		Client:    client,
		TargetAPI: serverConfig.TargetAPI,
		Tools:     serverConfig.Tools,
		Prompts:   serverConfig.Prompts,
		Resources: serverConfig.Resources,
		Options:   serverConfig.Options,
	}

	return adapter, nil
}

// AddToMCPServer 将适配器的功能添加到MCP服务器
func (ma *MultiAdapter) AddToMCPServer(mcpServer *server.MCPServer, adapterName string) error {
	adapter, exists := ma.adapters[adapterName]
	if !exists {
		return fmt.Errorf("adapter '%s' not found", adapterName)
	}

	// 创建过滤函数
	filterFunc := func(toolName string) bool {
		return ma.shouldFilterTool(adapterName, toolName)
	}

	// 调用 AdapterInstance 的统一实现
	return adapter.AddToMCPServerWithFilter(context.Background(), mcpServer, filterFunc)
}

// shouldFilterTool 检查工具是否应该被过滤
func (ma *MultiAdapter) shouldFilterTool(adapterName string, toolName string) bool {
	// 获取适配器配置
	adapterConfig, exists := ma.config.AdapterServers[adapterName]
	if !exists {
		return false // 适配器不存在，不过滤
	}

	// 首先检查服务器级别的过滤器
	if adapterConfig.Options != nil && adapterConfig.Options.ToolFilter != nil {
		switch adapterConfig.Options.ToolFilter.Mode {
		case "allow":
			// 允许模式：只有在列表中的工具才被允许
			for _, allowedTool := range adapterConfig.Options.ToolFilter.Tools {
				if allowedTool == toolName {
					return false // 不过滤
				}
			}
			return true // 过滤掉
		case "deny":
			// 拒绝模式：列表中的工具被拒绝
			for _, deniedTool := range adapterConfig.Options.ToolFilter.Tools {
				if deniedTool == toolName {
					return true // 过滤掉
				}
			}
			return false // 不过滤
		case "block":
			// 阻止模式：列表中的工具被阻止
			for _, blockedTool := range adapterConfig.Options.ToolFilter.Tools {
				if blockedTool == toolName {
					return true // 过滤掉
				}
			}
			return false // 不过滤
		}
	}

	// 如果没有服务器级别的过滤器，检查全局过滤器
	if ma.config.AdapterProxy != nil && ma.config.AdapterProxy.Options != nil && ma.config.AdapterProxy.Options.ToolFilter != nil {
		switch ma.config.AdapterProxy.Options.ToolFilter.Mode {
		case "allow":
			for _, allowedTool := range ma.config.AdapterProxy.Options.ToolFilter.Tools {
				if allowedTool == toolName {
					return false // 不过滤
				}
			}
			return true // 过滤掉
		case "deny":
			for _, deniedTool := range ma.config.AdapterProxy.Options.ToolFilter.Tools {
				if deniedTool == toolName {
					return true // 过滤掉
				}
			}
			return false // 不过滤
		case "block":
			// 阻止模式：列表中的工具被阻止
			for _, blockedTool := range ma.config.AdapterProxy.Options.ToolFilter.Tools {
				if blockedTool == toolName {
					return true // 过滤掉
				}
			}
			return false // 不过滤
		}
	}

	// 默认不过滤
	return false
}

// GetConfig 返回配置
func (ma *MultiAdapter) GetConfig() *NewConfig {
	return ma.config
}

// GetAdapters 返回所有适配器
func (ma *MultiAdapter) GetAdapters() map[string]*AdapterInstance {
	return ma.adapters
}
