package adapter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"text/template"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolHandler 负责处理单个工具的调用请求
type ToolHandler struct {
	ToolConfig      ToolConfig
	TargetAPIConfig TargetAPIConfig
	Client          *http.Client
}

// Handle 是处理工具调用的主函数，它实现了 server.ToolHandlerFunc 接口
func (h *ToolHandler) Handle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// 在 v0.28.0, 参数已经是解码后的 map，无需再次 Unmarshal
	params := req.Params.Arguments

	targetURL := h.buildURL(params)

	var body io.Reader
	var err error
	if h.ToolConfig.HTTPMapping.Method == http.MethodPost || h.ToolConfig.HTTPMapping.Method == http.MethodPut {
		body, err = h.prepareBody(params)
		if err != nil {
			return nil, fmt.Errorf("failed to prepare request body: %w", err)
		}
	}

	httpReq, err := http.NewRequestWithContext(ctx, h.ToolConfig.HTTPMapping.Method, targetURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	h.setHeaders(httpReq, params)
	h.setQueryParams(httpReq, params)
	h.setAuth(httpReq)

	log.Printf("Dispatching request: %s %s", httpReq.Method, httpReq.URL.String())

	resp, err := h.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if customErr, ok := h.ToolConfig.ErrorMapping[resp.StatusCode]; ok {
		return nil, fmt.Errorf(customErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http error: status code %d, body: %s", resp.StatusCode, string(respBody))
	}

	var resultData interface{}
	if err := json.Unmarshal(respBody, &resultData); err != nil {
		resultData = string(respBody)
	}

	resultBytes, err := json.Marshal(resultData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal result data: %w", err)
	}

	return &mcp.CallToolResult{
		Result: json.RawMessage(resultBytes),
	}, nil
}

// buildURL 根据配置和输入参数构建最终的请求 URL
func (h *ToolHandler) buildURL(params map[string]interface{}) string {
	path := h.ToolConfig.HTTPMapping.Path
	// 替换路径参数, e.g., /users/{userId} -> /users/123
	for _, p := range h.ToolConfig.HTTPMapping.ParameterMapping {
		if p.In == "path" {
			placeholder := "{" + p.Target + "}"
			if val, ok := params[p.Source]; ok {
				path = strings.Replace(path, placeholder, fmt.Sprint(val), -1)
			}
		}
	}
	return h.TargetAPIConfig.BaseURL + path
}

// setQueryParams 从输入参数中提取值并设置到 URL 的查询字符串中
func (h *ToolHandler) setQueryParams(req *http.Request, params map[string]interface{}) {
	q := req.URL.Query()
	for _, p := range h.ToolConfig.HTTPMapping.ParameterMapping {
		if p.In == "query" {
			if val, ok := params[p.Source]; ok {
				q.Add(p.Target, fmt.Sprint(val))
			} else if p.Default != nil {
				q.Add(p.Target, fmt.Sprint(p.Default))
			}
		}
	}
	req.URL.RawQuery = q.Encode()
}

// setHeaders 从输入参数中提取值并设置到 HTTP 请求头中
func (h *ToolHandler) setHeaders(req *http.Request, params map[string]interface{}) {
	// 添加全局请求头
	for key, val := range h.TargetAPIConfig.Headers {
		req.Header.Set(key, val)
	}
	// 添加动态请求头
	for _, p := range h.ToolConfig.HTTPMapping.ParameterMapping {
		if p.In == "header" {
			if val, ok := params[p.Source]; ok {
				req.Header.Set(p.Target, fmt.Sprint(val))
			} else if p.Default != nil {
				req.Header.Set(p.Target, fmt.Sprint(p.Default))
			}
		}
	}
	// 如果有请求体，设置 Content-Type
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
}

// setAuth 根据配置向请求中添加认证信息
func (h *ToolHandler) setAuth(req *http.Request) {
	auth := h.TargetAPIConfig.Auth
	switch auth.Type {
	case "apiKey":
		key := auth.Config["key"]
		value := auth.Config["value"]
		location := auth.Config["location"]
		if key == "" || value == "" {
			return
		}
		if location == "header" {
			req.Header.Set(key, value)
		} else if location == "query" {
			q := req.URL.Query()
			q.Add(key, value)
			req.URL.RawQuery = q.Encode()
		}
	case "bearer":
		token := auth.Config["token"]
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}
}

// prepareBody 根据 bodyTemplate 和输入参数生成请求体
func (h *ToolHandler) prepareBody(params map[string]interface{}) (io.Reader, error) {
	if h.ToolConfig.HTTPMapping.BodyTemplate == nil {
		// 如果没有模板，直接将所有参数序列化为 JSON
		bodyBytes, err := json.Marshal(params)
		if err != nil {
			return nil, err
		}
		return bytes.NewBuffer(bodyBytes), nil
	}

	// 如果有模板，使用模板进行渲染
	tmpl, err := template.New("body").Parse(string(*h.ToolConfig.HTTPMapping.BodyTemplate))
	if err != nil {
		return nil, fmt.Errorf("failed to parse body template: %w", err)
	}

	var buf bytes.Buffer
	err = tmpl.Execute(&buf, params)
	if err != nil {
		return nil, fmt.Errorf("failed to execute body template: %w", err)
	}

	return &buf, nil
}
