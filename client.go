// client.go 文件负责与后端 MCP 服务器的通信和集成。
// 它实现了不同类型的 MCP 客户端（stdio、sse、streamable-http），
// 并将这些客户端的功能通过代理暴露出去。
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

// Client 是对 MCP 客户端的封装，添加了一些代理相关的元数据和方法
type Client struct {
	name            string         // 客户端名称，用于日志和路由
	needPing        bool           // 是否需要定期发送 ping 请求
	needManualStart bool           // 是否需要手动启动客户端（对于 SSE 和 HTTP 客户端）
	client          *client.Client // 底层 MCP 客户端实例
	options         *Options       // 客户端选项
}

// newMCPClient 创建一个新的 MCP 客户端实例
// 根据配置，它会创建合适类型的客户端（stdio、sse 或 streamable-http）
func newMCPClient(name string, conf *MCPClientConfig) (*Client, error) {
	// 解析客户端配置，确定具体的客户端类型
	clientInfo, pErr := parseMCPClientConfig(conf)
	if pErr != nil {
		return nil, pErr
	}

	// 根据具体的客户端类型创建对应的客户端实例
	switch v := clientInfo.(type) {
	case *StdioMCPClientConfig:
		// 处理 Stdio 类型的客户端
		// 将环境变量映射转换为字符串切片格式
		envs := make([]string, 0, len(v.Env))
		for kk, vv := range v.Env {
			envs = append(envs, fmt.Sprintf("%s=%s", kk, vv))
		}
		// 创建 Stdio MCP 客户端
		mcpClient, err := client.NewStdioMCPClient(v.Command, envs, v.Args...)
		if err != nil {
			return nil, err
		}

		// 返回封装后的客户端
		return &Client{
			name:    name,
			client:  mcpClient,
			options: conf.Options,
		}, nil
	case *SSEMCPClientConfig:
		// 处理 SSE 类型的客户端
		var options []transport.ClientOption
		if len(v.Headers) > 0 {
			options = append(options, client.WithHeaders(v.Headers))
		}
		// 创建 SSE MCP 客户端
		mcpClient, err := client.NewSSEMCPClient(v.URL, options...)
		if err != nil {
			return nil, err
		}
		// 返回封装后的客户端，注意 SSE 客户端需要手动启动和定期 ping
		return &Client{
			name:            name,
			needPing:        true,
			needManualStart: true,
			client:          mcpClient,
			options:         conf.Options,
		}, nil
	case *StreamableMCPClientConfig:
		// 处理 Streamable HTTP 类型的客户端
		var options []transport.StreamableHTTPCOption
		if len(v.Headers) > 0 {
			options = append(options, transport.WithHTTPHeaders(v.Headers))
		}
		if v.Timeout > 0 {
			options = append(options, transport.WithHTTPTimeout(v.Timeout))
		}
		// 创建 Streamable HTTP MCP 客户端
		mcpClient, err := client.NewStreamableHttpClient(v.URL, options...)
		if err != nil {
			return nil, err
		}
		// 返回封装后的客户端，注意 HTTP 客户端也需要手动启动和定期 ping
		return &Client{
			name:            name,
			needPing:        true,
			needManualStart: true,
			client:          mcpClient,
			options:         conf.Options,
		}, nil
	}
	return nil, errors.New("invalid client type")
}

// addToMCPServer 是代理核心功能的实现
// 它连接到后端 MCP 服务，获取其能力（工具、提示、资源等），
// 并将这些能力注册到代理的 MCP 服务器实例上
func (c *Client) addToMCPServer(ctx context.Context, clientInfo mcp.Implementation, mcpServer *server.MCPServer) error {
	// 如果需要手动启动客户端（对于 SSE 和 HTTP 客户端），先启动它
	if c.needManualStart {
		err := c.client.Start(ctx)
		if err != nil {
			return err
		}
	}

	// 准备 MCP 初始化请求
	initRequest := mcp.InitializeRequest{}
	initRequest.Params.ProtocolVersion = mcp.LATEST_PROTOCOL_VERSION
	initRequest.Params.ClientInfo = clientInfo
	initRequest.Params.Capabilities = mcp.ClientCapabilities{
		Experimental: make(map[string]interface{}),
		Roots:        nil,
		Sampling:     nil,
	}

	// 向后端 MCP 服务发送初始化请求
	_, err := c.client.Initialize(ctx, initRequest)
	if err != nil {
		return err
	}
	log.Printf("<%s> Successfully initialized MCP client", c.name)

	// 获取后端服务提供的各种能力，并添加到代理的 MCP 服务器
	// 首先添加工具，这是必须成功的
	err = c.addToolsToServer(ctx, mcpServer)
	if err != nil {
		return err
	}

	// 尝试添加提示、资源和资源模板，即使这些操作失败也不会影响整体功能
	_ = c.addPromptsToServer(ctx, mcpServer)
	_ = c.addResourcesToServer(ctx, mcpServer)
	_ = c.addResourceTemplatesToServer(ctx, mcpServer)

	// 如果需要定期 ping，启动 ping 任务
	if c.needPing {
		go c.startPingTask(ctx)
	}
	return nil
}

// startPingTask 启动一个定期 ping 后端服务的任务
// 每 30 秒发送一次 ping 请求，确保连接保持活跃
func (c *Client) startPingTask(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
PingLoop:
	for {
		select {
		case <-ctx.Done():
			log.Printf("<%s> Context done, stopping ping", c.name)
			break PingLoop
		case <-ticker.C:
			_ = c.client.Ping(ctx)
		}
	}
}

// addToolsToServer 从后端服务获取可用的工具列表，并将它们添加到代理的 MCP 服务器
// 同时应用工具过滤逻辑，决定哪些工具可以被添加
func (c *Client) addToolsToServer(ctx context.Context, mcpServer *server.MCPServer) error {
	toolsRequest := mcp.ListToolsRequest{}
	// 默认的过滤函数允许所有工具
	filterFunc := func(toolName string) bool {
		return true
	}

	// 如果配置了工具过滤器，根据过滤模式构建过滤函数
	if c.options != nil && c.options.ToolFilter != nil && len(c.options.ToolFilter.List) > 0 {
		filterSet := make(map[string]struct{})
		mode := ToolFilterMode(strings.ToLower(string(c.options.ToolFilter.Mode)))
		for _, toolName := range c.options.ToolFilter.List {
			filterSet[toolName] = struct{}{}
		}

		// 根据过滤模式选择不同的过滤策略
		switch mode {
		case ToolFilterModeAllow:
			// 白名单模式：只允许列表中的工具
			filterFunc = func(toolName string) bool {
				_, inList := filterSet[toolName]
				if !inList {
					log.Printf("<%s> Ignoring tool %s as it is not in allow list", c.name, toolName)
				}
				return inList
			}
		case ToolFilterModeBlock:
			// 黑名单模式：阻止列表中的工具
			filterFunc = func(toolName string) bool {
				_, inList := filterSet[toolName]
				if inList {
					log.Printf("<%s> Ignoring tool %s as it is in block list", c.name, toolName)
				}
				return !inList
			}
		default:
			log.Printf("<%s> Unknown tool filter mode: %s, skipping tool filter", c.name, mode)
		}
	}

	// 支持分页获取工具列表
	for {
		tools, err := c.client.ListTools(ctx, toolsRequest)
		if err != nil {
			return err
		}
		if len(tools.Tools) == 0 {
			break
		}
		log.Printf("<%s> Successfully listed %d tools", c.name, len(tools.Tools))

		// 遍历每个工具，应用过滤函数，并将符合条件的工具添加到代理服务器
		for _, tool := range tools.Tools {
			if filterFunc(tool.Name) {
				log.Printf("<%s> Adding tool %s", c.name, tool.Name)
				// 注意：这里的第二个参数是一个回调函数，当代理收到工具调用请求时，
				// 它会调用这个函数，从而将请求转发到真正的后端服务
				mcpServer.AddTool(tool, c.client.CallTool)
			}
		}

		// 检查是否有更多页面
		if tools.NextCursor == "" {
			break
		}
		toolsRequest.Params.Cursor = tools.NextCursor
	}

	return nil
}

// addPromptsToServer 从后端服务获取可用的提示列表，并将它们添加到代理的 MCP 服务器
func (c *Client) addPromptsToServer(ctx context.Context, mcpServer *server.MCPServer) error {
	promptsRequest := mcp.ListPromptsRequest{}
	// 支持分页获取提示列表
	for {
		prompts, err := c.client.ListPrompts(ctx, promptsRequest)
		if err != nil {
			return err
		}
		if len(prompts.Prompts) == 0 {
			break
		}
		log.Printf("<%s> Successfully listed %d prompts", c.name, len(prompts.Prompts))

		// 遍历每个提示，并将其添加到代理服务器
		for _, prompt := range prompts.Prompts {
			log.Printf("<%s> Adding prompt %s", c.name, prompt.Name)
			mcpServer.AddPrompt(prompt, c.client.GetPrompt)
		}

		// 检查是否有更多页面
		if prompts.NextCursor == "" {
			break
		}
		promptsRequest.Params.Cursor = prompts.NextCursor
	}
	return nil
}

// addResourcesToServer 从后端服务获取可用的资源列表，并将它们添加到代理的 MCP 服务器
func (c *Client) addResourcesToServer(ctx context.Context, mcpServer *server.MCPServer) error {
	resourcesRequest := mcp.ListResourcesRequest{}
	// 支持分页获取资源列表
	for {
		resources, err := c.client.ListResources(ctx, resourcesRequest)
		if err != nil {
			return err
		}
		if len(resources.Resources) == 0 {
			break
		}
		log.Printf("<%s> Successfully listed %d resources", c.name, len(resources.Resources))

		// 遍历每个资源，并将其添加到代理服务器
		for _, resource := range resources.Resources {
			log.Printf("<%s> Adding resource %s", c.name, resource.Name)
			// 为每个资源创建一个读取函数，用于处理读取请求
			mcpServer.AddResource(resource, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
				readResource, e := c.client.ReadResource(ctx, request)
				if e != nil {
					return nil, e
				}
				return readResource.Contents, nil
			})
		}

		// 检查是否有更多页面
		if resources.NextCursor == "" {
			break
		}
		resourcesRequest.Params.Cursor = resources.NextCursor
	}
	return nil
}

// addResourceTemplatesToServer 从后端服务获取可用的资源模板列表，并将它们添加到代理的 MCP 服务器
func (c *Client) addResourceTemplatesToServer(ctx context.Context, mcpServer *server.MCPServer) error {
	resourceTemplatesRequest := mcp.ListResourceTemplatesRequest{}
	// 支持分页获取资源模板列表
	for {
		resourceTemplates, err := c.client.ListResourceTemplates(ctx, resourceTemplatesRequest)
		if err != nil {
			return err
		}
		if len(resourceTemplates.ResourceTemplates) == 0 {
			break
		}
		log.Printf("<%s> Successfully listed %d resource templates", c.name, len(resourceTemplates.ResourceTemplates))

		// 遍历每个资源模板，并将其添加到代理服务器
		for _, resourceTemplate := range resourceTemplates.ResourceTemplates {
			log.Printf("<%s> Adding resource template %s", c.name, resourceTemplate.Name)
			// 为每个资源模板创建一个读取函数，用于处理读取请求
			mcpServer.AddResourceTemplate(resourceTemplate, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
				readResource, e := c.client.ReadResource(ctx, request)
				if e != nil {
					return nil, e
				}
				return readResource.Contents, nil
			})
		}

		// 检查是否有更多页面
		if resourceTemplates.NextCursor == "" {
			break
		}
		resourceTemplatesRequest.Params.Cursor = resourceTemplates.NextCursor
	}
	return nil
}

// Close 关闭客户端连接
func (c *Client) Close() error {
	if c.client != nil {
		return c.client.Close()
	}
	return nil
}

// Server 是代理中的服务器组件，封装了 MCP 服务器和 SSE 服务器
type Server struct {
	tokens    []string          // 认证令牌列表
	mcpServer *server.MCPServer // MCP 服务器实例，处理 MCP 协议逻辑
	sseServer *server.SSEServer // SSE 服务器实例，提供 HTTP 接口
}

// newMCPServer 创建一个新的 MCP 服务器实例，用于暴露后端服务的功能
func newMCPServer(name, version, baseURL string, clientConfig *MCPClientConfig) *Server {
	// 准备服务器选项
	serverOpts := []server.ServerOption{
		server.WithResourceCapabilities(true, true), // 启用资源能力
		server.WithRecovery(),                       // 启用恢复机制
	}

	// 如果启用了日志，添加日志选项
	if clientConfig.Options.LogEnabled.OrElse(false) {
		serverOpts = append(serverOpts, server.WithLogging())
	}

	// 创建 MCP 服务器实例
	mcpServer := server.NewMCPServer(
		name,
		version,
		serverOpts...,
	)

	// 创建 SSE 服务器实例，用于提供 HTTP 接口
	sseServer := server.NewSSEServer(mcpServer,
		server.WithStaticBasePath(name),
		server.WithBaseURL(baseURL),
	)

	// 创建并返回 Server 实例
	srv := &Server{
		mcpServer: mcpServer,
		sseServer: sseServer,
	}

	// 如果配置了认证令牌，设置到 Server 实例
	if clientConfig.Options != nil && len(clientConfig.Options.AuthTokens) > 0 {
		srv.tokens = clientConfig.Options.AuthTokens
	}

	return srv
}
