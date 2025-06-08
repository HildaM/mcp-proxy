package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/mark3labs/mcp-go/mcp"
)

// ToolHandler 负责处理单个工具的调用请求
type ToolHandler struct {
	ToolConfig      ToolConfig
	TargetAPIConfig TargetAPIConfig
	Client          *http.Client
}

// Handle is the main function for handling tool calls. It implements the server.ToolHandlerFunc interface.
// It builds an HTTP request based on the tool configuration, sends it to the target API,
// and transforms the HTTP response into an MCP tool call result.
func (h *ToolHandler) Handle(ctx context.Context, req mcp.CallToolRequest) (*mcp.CallToolResult, error) {
	// Type assert params to map[string]interface{}
	params, ok := req.Params.Arguments.(map[string]interface{})
	if !ok {
		log.Printf("[TOOL_ERROR] Invalid parameters type for tool %s: expected map[string]interface{}, got %T", h.ToolConfig.Name, req.Params.Arguments)
		return nil, fmt.Errorf("invalid parameters: expected map[string]interface{}, got %T", req.Params.Arguments)
	}

	// 1. Validate required parameters
	if err := h.validateParams(params); err != nil {
		log.Printf("[TOOL_ERROR] Parameter validation failed for tool %s: %v", h.ToolConfig.Name, err)
		return nil, fmt.Errorf("invalid parameters: %w", err)
	}

	// 2. Build URL with path parameters
	targetURL, err := h.buildURL(params)
	if err != nil {
		log.Printf("[TOOL_ERROR] URL building failed for tool %s: %v", h.ToolConfig.Name, err)
		return nil, fmt.Errorf("failed to build URL: %w", err)
	}
	log.Printf("[TOOL_REQUEST] Target URL: %s", targetURL)

	// 3. Prepare request body for POST/PUT requests
	var body io.Reader
	if h.ToolConfig.HTTPMapping.Method == http.MethodPost || h.ToolConfig.HTTPMapping.Method == http.MethodPut {
		body, err = h.prepareBody(params)
		if err != nil {
			log.Printf("[TOOL_ERROR] Request body preparation failed for tool %s: %v", h.ToolConfig.Name, err)
			return nil, fmt.Errorf("failed to prepare request body: %w", err)
		}
	}

	// 4. Create HTTP request
	httpReq, err := http.NewRequestWithContext(ctx, h.ToolConfig.HTTPMapping.Method, targetURL.String(), body)
	if err != nil {
		log.Printf("[TOOL_ERROR] HTTP request creation failed for tool %s: %v", h.ToolConfig.Name, err)
		return nil, fmt.Errorf("failed to create http request: %w", err)
	}

	// 5. Set headers, query parameters, and authentication
	h.setHeaders(httpReq, params)
	h.addQueryParams(httpReq, params)

	log.Printf("Dispatching request: %s %s", httpReq.Method, httpReq.URL.String())

	// 6. Execute request with retry logic
	var resp *http.Response
	maxRetries := 3
	for i := 0; i < maxRetries; i++ {
		resp, err = h.Client.Do(httpReq)
		if err == nil {
			break
		}
		log.Printf("Request attempt %d failed: %v", i+1, err)
		if i < maxRetries-1 {
			time.Sleep(time.Duration(i+1) * time.Second)
		}
	}
	if err != nil {
		return nil, fmt.Errorf("http request failed after %d attempts: %w", maxRetries, err)
	}
	defer resp.Body.Close()

	// 7. Read response body
	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[TOOL_ERROR] Response body reading failed for tool %s: %v", h.ToolConfig.Name, err)
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// 8. Handle errors based on status code
	if customErr, ok := h.ToolConfig.ErrorMapping[resp.StatusCode]; ok {
		return nil, fmt.Errorf(customErr)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("http error: status code %d, body: %s", resp.StatusCode, string(respBody))
	}

	// 9. Process successful response
	var resultData interface{}
	// Try to unmarshal as JSON, if fails, return as raw string.
	if err := json.Unmarshal(respBody, &resultData); err != nil {
		// If unmarshalling fails, treat the entire response body as a single text string.
		log.Printf("[TOOL_SUCCESS] Tool %s completed (response as text): %s", h.ToolConfig.Name, string(respBody))
		return &mcp.CallToolResult{
			Content: []mcp.Content{
				mcp.TextContent{Text: string(respBody)},
			},
		}, nil
	}

	// If unmarshalling was successful, resultData could be a map, slice, or primitive.
	// We need to marshal it back to JSON bytes to be safe if it's not a simple string.
	// However, mcp.TextContent expects a string.
	// For simplicity, we'll marshal it back to a JSON string and pass that.
	// A more sophisticated approach might inspect resultData's type.
	resultBytes, err := json.Marshal(resultData)
	if err != nil {
		log.Printf("[TOOL_ERROR] Result data marshaling failed for tool %s: %v", h.ToolConfig.Name, err)
		return nil, fmt.Errorf("failed to marshal result data: %w", err)
	}

	log.Printf("[TOOL_SUCCESS] Tool %s completed (response as JSON): %s", h.ToolConfig.Name, string(resultBytes))
	return &mcp.CallToolResult{
		Content: []mcp.Content{
			mcp.TextContent{Text: string(resultBytes)},
		},
	}, nil
}

// validateParams checks if all required parameters are present in the input.
func (h *ToolHandler) validateParams(params map[string]interface{}) error {
	for _, p := range h.ToolConfig.HTTPMapping.ParameterMapping {
		if p.Required {
			if _, ok := params[p.Source]; !ok && p.Default == nil {
				return fmt.Errorf("required parameter '%s' is missing", p.Source)
			}
		}
	}
	return nil
}

// buildURL constructs the final request URL by parsing the base URL and replacing path parameters.
func (h *ToolHandler) buildURL(params map[string]interface{}) (*url.URL, error) {
	// Start with the base URL from the target API config
	baseURL, err := url.Parse(h.TargetAPIConfig.BaseURL)
	if err != nil {
		return nil, fmt.Errorf("invalid target API base URL: %w", err)
	}

	// Append the path from the HTTP mapping
	path := h.ToolConfig.HTTPMapping.Path
	for _, p := range h.ToolConfig.HTTPMapping.ParameterMapping {
		if p.In == "path" {
			if val, ok := params[p.Source]; ok {
				// URL-escape the path segment to handle special characters
				escapedVal := url.PathEscape(fmt.Sprint(val))
				path = strings.Replace(path, "{"+p.Target+"}", escapedVal, -1)
			}
		}
	}

	// Resolve the final path against the base URL
	finalURL, err := baseURL.Parse(path)
	if err != nil {
		return nil, fmt.Errorf("failed to parse path against base URL: %w", err)
	}

	return finalURL, nil
}

// addQueryParams adds query parameters from tool inputs and authentication settings.
func (h *ToolHandler) addQueryParams(req *http.Request, params map[string]interface{}) {
	q := req.URL.Query()

	// Add params from mapping
	for _, p := range h.ToolConfig.HTTPMapping.ParameterMapping {
		if p.In == "query" {
			if val, ok := params[p.Source]; ok {
				q.Add(p.Target, fmt.Sprint(val))
			} else if p.Default != nil {
				q.Add(p.Target, fmt.Sprint(p.Default))
			}
		}
	}

	// Add auth params
	auth := h.TargetAPIConfig.Auth
	if auth.Type == "apiKey" && auth.Config["location"] == "query" {
		if key, val := auth.Config["key"], auth.Config["value"]; key != "" && val != "" {
			q.Add(key, val)
		}
	}

	req.URL.RawQuery = q.Encode()
}

// setHeaders sets headers from global config, parameter mapping, and authentication.
func (h *ToolHandler) setHeaders(req *http.Request, params map[string]interface{}) {
	// Add global headers
	for key, val := range h.TargetAPIConfig.Headers {
		req.Header.Set(key, val)
	}

	// Add dynamic headers from params
	for _, p := range h.ToolConfig.HTTPMapping.ParameterMapping {
		if p.In == "header" {
			if val, ok := params[p.Source]; ok {
				req.Header.Set(p.Target, fmt.Sprint(val))
			} else if p.Default != nil {
				req.Header.Set(p.Target, fmt.Sprint(p.Default))
			}
		}
	}

	// Add auth headers
	auth := h.TargetAPIConfig.Auth
	if auth.Type == "apiKey" && auth.Config["location"] == "header" {
		if key, val := auth.Config["key"], auth.Config["value"]; key != "" && val != "" {
			req.Header.Set(key, val)
		}
	} else if auth.Type == "bearer" {
		if token := auth.Config["token"]; token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
	}

	// Set Content-Type for requests with a body
	if req.Body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
}

// prepareBody creates the io.Reader for the request body.
// It discards the complex and unsafe text/template approach.
// If a bodyTemplate is defined, it's used as a map to structure the body.
// Otherwise, all input parameters are marshalled into the body.
func (h *ToolHandler) prepareBody(params map[string]interface{}) (io.Reader, error) {
	var bodyData interface{}

	if h.ToolConfig.HTTPMapping.BodyTemplate == nil {
		// No template, use all params as the body
		bodyData = params
	} else {
		// A template exists, unmarshal it into a map
		var templateMap map[string]interface{}
		if err := json.Unmarshal(*h.ToolConfig.HTTPMapping.BodyTemplate, &templateMap); err != nil {
			return nil, fmt.Errorf("failed to parse bodyTemplate JSON: %w", err)
		}

		// Recursively substitute placeholders like "{{.paramName}}" in the template map
		bodyData = substitute(templateMap, params)
	}

	bodyBytes, err := json.Marshal(bodyData)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal final request body: %w", err)
	}
	return bytes.NewBuffer(bodyBytes), nil
}

// substitute recursively replaces placeholder values in the template structure.
func substitute(template interface{}, params map[string]interface{}) interface{} {
	switch t := template.(type) {
	case string:
		// Check if it's a placeholder of the form "{{.key}}"
		if strings.HasPrefix(t, "{{.") && strings.HasSuffix(t, "}}") {
			key := strings.TrimSuffix(strings.TrimPrefix(t, "{{."), "}}")
			if val, ok := params[key]; ok {
				return val
			}
		}
		return t
	case map[string]interface{}:
		result := make(map[string]interface{}, len(t))
		for k, v := range t {
			result[k] = substitute(v, params)
		}
		return result
	case []interface{}:
		result := make([]interface{}, len(t))
		for i, v := range t {
			result[i] = substitute(v, params)
		}
		return result
	default:
		return t
	}
}
