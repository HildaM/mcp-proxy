package main

import (
	"context"
	"errors"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"path"
	"strings"
	"syscall"
	"time"

	"github.com/mark3labs/mcp-go/server"
	"golang.org/x/sync/errgroup"
)

// MiddlewareFunc 定义中间件函数类型
type MiddlewareFunc func(http.Handler) http.Handler

// AdapterServer 包装MCP服务器和SSE服务器
type AdapterServer struct {
	mcpServer *server.MCPServer
	sseServer http.Handler
	tokens    []string
}

// chainMiddleware 将多个中间件链接在一起
func chainMiddleware(handler http.Handler, middlewares ...MiddlewareFunc) http.Handler {
	for i := len(middlewares) - 1; i >= 0; i-- {
		handler = middlewares[i](handler)
	}
	return handler
}

// recoverMiddleware 恢复中间件，防止panic导致服务器崩溃
func recoverMiddleware(serverName string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Printf("<%s> Panic recovered: %v", serverName, err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// loggerMiddleware 创建一个中间件，用给定的前缀记录传入的请求。
func loggerMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			log.Printf("<%s> Request [%s] %s", prefix, r.Method, r.URL.Path)
			next.ServeHTTP(w, r)
		})
	}
}

// authMiddleware 认证中间件
func newAuthMiddleware(tokens []string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(tokens) == 0 {
				next.ServeHTTP(w, r)
				return
			}

			authHeader := r.Header.Get("Authorization")
			if authHeader == "" {
				http.Error(w, "Authorization header required", http.StatusUnauthorized)
				return
			}

			if !strings.HasPrefix(authHeader, "Bearer ") {
				http.Error(w, "Bearer token required", http.StatusUnauthorized)
				return
			}

			token := strings.TrimPrefix(authHeader, "Bearer ")
			valid := false
			for _, validToken := range tokens {
				if token == validToken {
					valid = true
					break
				}
			}

			if !valid {
				http.Error(w, "Invalid token", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

// newAdapterServer 创建新的适配器服务器
func newAdapterServer(name, version, baseURL string, serverConfig *AdapterServerConfig) *AdapterServer {
	// 准备服务器选项
	serverOpts := []server.ServerOption{
		server.WithResourceCapabilities(true, true), // 启用资源能力
		server.WithRecovery(),                       // 启用恢复机制
	}

	// 如果启用了日志，添加日志选项
	if serverConfig.Options.LogEnabled {
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

	// 创建并返回 AdapterServer 实例
	srv := &AdapterServer{
		mcpServer: mcpServer,
		sseServer: sseServer,
	}

	// 如果配置了认证令牌，设置到 AdapterServer 实例
	if serverConfig.Options != nil && len(serverConfig.Options.AuthTokens) > 0 {
		srv.tokens = serverConfig.Options.AuthTokens
	}

	return srv
}

// startMultiAdapterHTTPServer 启动多适配器HTTP服务器
func startMultiAdapterHTTPServer(config *NewConfig, configPath string) error {
	baseURL, err := url.Parse(config.AdapterProxy.BaseURL)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var errorGroup errgroup.Group
	httpMux := http.NewServeMux()
	httpServer := &http.Server{
		Addr:    config.AdapterProxy.Addr,
		Handler: httpMux,
	}

	// 创建多适配器实例
	multiAdapter, err := NewMultiAdapter(configPath)
	if err != nil {
		return err
	}

	// 遍历每个配置的适配器服务器，设置其路由
	for name, serverConfig := range config.AdapterServers {
		// 为代理创建一个新的适配器服务器实例
		adapterServer := newAdapterServer(name, config.AdapterProxy.Version, config.AdapterProxy.BaseURL, serverConfig)

		// 并发地初始化每个适配器并将其添加到 HTTP 服务器
		errorGroup.Go(func() error {
			log.Printf("<%s> Connecting", name)

			// 将适配器能力注册到服务器实例中
			if adapter, exists := multiAdapter.GetAdapters()[name]; exists {
				addErr := adapter.AddToMCPServer(ctx, adapterServer.mcpServer)
				if addErr != nil {
					log.Printf("<%s> Failed to add adapter to server: %v", name, addErr)
					// 如果 PanicIfInvalid 为 true，此处的失败将导致整个代理服务停止启动
					if serverConfig.Options.PanicIfInvalid {
						return addErr
					}
					return nil
				}
			} else {
				log.Printf("<%s> Adapter not found", name)
				if serverConfig.Options.PanicIfInvalid {
					return errors.New("adapter not found")
				}
				return nil
			}

			log.Printf("<%s> Connected", name)

			// 根据其配置，为此特定路由动态构建中间件链
			middlewares := make([]MiddlewareFunc, 0)
			middlewares = append(middlewares, recoverMiddleware(name))
			if serverConfig.Options.LogEnabled {
				middlewares = append(middlewares, loggerMiddleware(name))
			}
			if len(serverConfig.Options.AuthTokens) > 0 {
				middlewares = append(middlewares, newAuthMiddleware(serverConfig.Options.AuthTokens))
			}

			// 为此适配器服务器构建唯一的路由
			adapterRoute := path.Join(baseURL.Path, name)
			if !strings.HasPrefix(adapterRoute, "/") {
				adapterRoute = "/" + adapterRoute
			}
			if !strings.HasSuffix(adapterRoute, "/") {
				adapterRoute += "/"
			}

			// 为特定的适配器服务器路由注册最终的处理程序，该处理程序被其自己的中间件包裹
			httpMux.Handle(adapterRoute, chainMiddleware(adapterServer.sseServer, middlewares...))

			// 注册一个关闭函数，以便在服务器关闭时优雅地关闭适配器连接
			httpServer.RegisterOnShutdown(func() {
				log.Printf("<%s> Shutting down", name)
			})
			return nil
		})
	}

	// 启动一个 goroutine，等待所有适配器成功初始化
	go func() {
		err := errorGroup.Wait()
		if err != nil {
			log.Fatalf("Failed to add adapters: %v", err)
		}
		log.Printf("All adapters initialized")
	}()

	// 在一个单独的 goroutine 中启动主 HTTP 服务器
	go func() {
		log.Printf("Starting multi-adapter SSE server")
		log.Printf("Multi-adapter SSE server listening on %s", config.AdapterProxy.Addr)
		hErr := httpServer.ListenAndServe()
		if hErr != nil && !errors.Is(hErr, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", hErr)
		}
	}()

	// 等待一个关闭信号（例如，Ctrl+C）以开始优雅停机
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutdown signal received")

	// 尝试以 5 秒的超时时间优雅地关闭服务器
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()

	err = httpServer.Shutdown(shutdownCtx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func main() {
	// 检查是否提供了配置文件参数
	configPath := "config.json"
	if len(os.Args) > 1 {
		configPath = os.Args[1]
	}

	// 加载配置
	config, err := LoadNewConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 启动多适配器HTTP服务器
	if err := startMultiAdapterHTTPServer(config, configPath); err != nil {
		log.Fatalf("Failed to start multi-adapter HTTP server: %v", err)
	}

	log.Println("Multi-adapter server exiting")
}
