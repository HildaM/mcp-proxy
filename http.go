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

	"github.com/mark3labs/mcp-go/mcp"
	"golang.org/x/sync/errgroup"
)

// MiddlewareFunc 定义了中间件函数的类型，它接收一个 http.Handler 并返回一个新的 http.Handler。
type MiddlewareFunc func(http.Handler) http.Handler

// chainMiddleware 将一系列中间件处理器应用于一个 http.Handler。
func chainMiddleware(h http.Handler, middlewares ...MiddlewareFunc) http.Handler {
	for _, mw := range middlewares {
		h = mw(h)
	}
	return h
}

// newAuthMiddleware 创建一个中间件，该中间件基于一个有效的令牌列表来强制执行身份验证。
func newAuthMiddleware(tokens []string) MiddlewareFunc {
	tokenSet := make(map[string]struct{}, len(tokens))
	for _, token := range tokens {
		tokenSet[token] = struct{}{}
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if len(tokens) != 0 {
				token := r.Header.Get("Authorization")
				token = strings.TrimSpace(strings.TrimPrefix(token, "Bearer "))
				if token == "" {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
				if _, ok := tokenSet[token]; !ok {
					http.Error(w, "Unauthorized", http.StatusUnauthorized)
					return
				}
			}
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

// recoverMiddleware 创建一个中间件，用于从处理器内的 panic 中恢复，
// 记录错误，并返回 500 内部服务器错误，以防止服务器崩溃。
func recoverMiddleware(prefix string) MiddlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if err := recover(); err != nil {
					log.Printf("<%s> Recovered from panic: %v", prefix, err)
					http.Error(w, "Internal Server Error", http.StatusInternalServerError)
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}

// startHTTPServer 根据提供的配置初始化并启动主 HTTP 代理服务器。
// 它负责设置路由、中间件和优雅停机处理。
func startHTTPServer(config *Config) error {

	baseURL, err := url.Parse(config.McpProxy.BaseURL)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var errorGroup errgroup.Group
	httpMux := http.NewServeMux()
	httpServer := &http.Server{
		Addr:    config.McpProxy.Addr,
		Handler: httpMux,
	}
	info := mcp.Implementation{
		Name:    config.McpProxy.Name,
		Version: config.McpProxy.Version,
	}

	// 遍历每个配置的 MCP 服务器，以设置其客户端和路由。
	for name, clientConfig := range config.McpServers {
		// 为代理创建一个新的 MCP 客户端和相应的服务器实例。
		mcpClient, err := newMCPClient(name, clientConfig)
		if err != nil {
			log.Fatalf("<%s> Failed to create client: %v", name, err)
		}
		server := newMCPServer(name, config.McpProxy.Version, config.McpProxy.BaseURL, clientConfig)
		// 并发地初始化每个客户端并将其添加到 HTTP 服务器。
		errorGroup.Go(func() error {
			log.Printf("<%s> Connecting", name)
			// 连接到后端 MCP 服务，并将其能力（工具等）注册到代理的服务器实例中。
			addErr := mcpClient.addToMCPServer(ctx, info, server.mcpServer)
			if addErr != nil {
				log.Printf("<%s> Failed to add client to server: %v", name, addErr)
				// 如果 PanicIfInvalid 为 true，此处的失败将导致整个代理服务停止启动。
				if clientConfig.Options.PanicIfInvalid.OrElse(false) {
					return addErr
				}
				return nil
			}
			log.Printf("<%s> Connected", name)

			// 根据其配置，为此特定路由动态构建中间件链。
			middlewares := make([]MiddlewareFunc, 0)
			middlewares = append(middlewares, recoverMiddleware(name))
			if clientConfig.Options.LogEnabled.OrElse(false) {
				middlewares = append(middlewares, loggerMiddleware(name))
			}
			if len(clientConfig.Options.AuthTokens) > 0 {
				middlewares = append(middlewares, newAuthMiddleware(clientConfig.Options.AuthTokens))
			}
			// 为此 MCP 服务器构建唯一的路由。
			mcpRoute := path.Join(baseURL.Path, name)
			if !strings.HasPrefix(mcpRoute, "/") {
				mcpRoute = "/" + mcpRoute
			}
			if !strings.HasSuffix(mcpRoute, "/") {
				mcpRoute += "/"
			}
			// 为特定的 MCP 服务器路由注册最终的处理程序，该处理程序被其自己的中间件包裹。
			httpMux.Handle(mcpRoute, chainMiddleware(server.sseServer, middlewares...))
			// 注册一个关闭函数，以便在服务器关闭时优雅地关闭客户端连接。
			httpServer.RegisterOnShutdown(func() {
				log.Printf("<%s> Shutting down", name)
				_ = mcpClient.Close()
			})
			return nil
		})
	}

	// 启动一个 goroutine，等待所有客户端成功初始化。
	// 如果任何客户端初始化失败并配置为 panic，这将导致致命错误。
	go func() {
		err := errorGroup.Wait()
		if err != nil {
			log.Fatalf("Failed to add clients: %v", err)
		}
		log.Printf("All clients initialized")
	}()

	// 在一个单独的 goroutine 中启动主 HTTP 服务器。
	go func() {
		log.Printf("Starting SSE server")
		log.Printf("SSE server listening on %s", config.McpProxy.Addr)
		hErr := httpServer.ListenAndServe()
		if hErr != nil && !errors.Is(hErr, http.ErrServerClosed) {
			log.Fatalf("Failed to start server: %v", hErr)
		}
	}()

	// 等待一个关闭信号（例如，Ctrl+C）以开始优雅停机。
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	<-sigChan
	log.Println("Shutdown signal received")

	// 尝试以 5 秒的超时时间优雅地关闭服务器。
	shutdownCtx, shutdownCancel := context.WithTimeout(ctx, 5*time.Second)
	defer shutdownCancel()

	err = httpServer.Shutdown(shutdownCtx)
	if err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}
