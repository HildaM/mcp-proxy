package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func Run() {
	// 1. 加载配置
	configPath := flag.String("config", "./adapter/config.json", "Path to the adapter config file")
	flag.Parse()

	cfg, err := LoadConfig(*configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 2. 初始化 MCP 服务器
	mcpServer := server.NewMCPServer(
		cfg.Server.Name,
		cfg.Server.Version,
		server.WithResourceCapabilities(true, true),
		server.WithLogging(),
		server.WithRecovery(),
	)

	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// 3. 注册工具
	for _, toolCfg := range cfg.Tools {
		inputSchema := mcp.ToolInputSchema{}
		if b, err := json.Marshal(toolCfg.InputSchema); err == nil {
			err = json.Unmarshal(b, &inputSchema)
			if err != nil {
				log.Fatalf("Failed to unmarshal input schema for tool %s: %v", toolCfg.Name, err)
			}
		} else {
			log.Fatalf("Failed to marshal input schema for tool %s: %v", toolCfg.Name, err)
		}

		currentToolCfg := toolCfg
		handler := &ToolHandler{
			ToolConfig:      currentToolCfg,
			TargetAPIConfig: cfg.TargetAPI,
			Client:          httpClient,
		}
		tool := mcp.Tool{
			Name:        currentToolCfg.Name,
			Description: currentToolCfg.Description,
			InputSchema: inputSchema,
		}
		log.Printf("Adding tool: %s", tool.Name)
		mcpServer.AddTool(tool, handler.Handle)
	}

	// 4. 注册静态提示
	for _, promptCfg := range cfg.Prompts {
		currentPromptCfg := promptCfg
		prompt := mcp.Prompt{
			Name:        currentPromptCfg.Name,
			Description: currentPromptCfg.Description,
		}
		log.Printf("Adding prompt: %s", prompt.Name)
		mcpServer.AddPrompt(prompt, func(ctx context.Context, request mcp.GetPromptRequest) (*mcp.GetPromptResult, error) {
			contentJSON, err := json.Marshal(currentPromptCfg.Content)
			if err != nil {
				return nil, err
			}
			return &mcp.GetPromptResult{
				Result: json.RawMessage(contentJSON),
			}, nil
		})
	}

	// 5. 注册静态资源
	for _, resourceCfg := range cfg.Resources {
		currentResourceCfg := resourceCfg
		resource := mcp.Resource{
			Name:        currentResourceCfg.Name,
			Description: currentResourceCfg.Description,
		}
		log.Printf("Adding resource: %s", resource.Name)
		mcpServer.AddResource(resource, func(ctx context.Context, request mcp.ReadResourceRequest) ([]mcp.ResourceContents, error) {
			return []mcp.ResourceContents{
				mcp.TextResourceContents{
					URI:      currentResourceCfg.Name,
					MIMEType: currentResourceCfg.ContentType,
					Text:     currentResourceCfg.Content,
				},
			}, nil
		})
	}

	// 6. 创建并启动 HTTP (SSE) 服务器
	sseServer := server.NewSSEServer(mcpServer,
		server.WithBaseURL(cfg.Server.BaseURL),
	)

	httpServer := &http.Server{
		Addr:    cfg.Server.Addr,
		Handler: sseServer,
	}

	go func() {
		log.Printf("Starting adapter server on %s", cfg.Server.Addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", err)
		}
	}()

	// 7. 处理优雅停机
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutdown signal received, shutting down gracefully...")

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := httpServer.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("Server shutdown failed: %v", err)
	}
	log.Println("Server exited properly")
}
