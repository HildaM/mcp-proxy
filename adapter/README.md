# Multi-Adapter Proxy Architecture

这是一个重新设计的适配器架构，支持在单个代理服务中配置和管理多个后端API服务。

## 架构概述

新架构将配置分为两个主要部分：

1. **代理通用配置** (`adapter_proxy`): 定义代理服务器的基本设置
2. **多服务器配置** (`adapter_servers`): 定义多个后端API适配器的具体配置

## 主要特性

- **多服务器支持**: 在单个代理实例中管理多个后端API
- **独立配置**: 每个后端服务可以有自己的认证、工具过滤和选项
- **配置继承**: 服务器级别的配置可以继承代理级别的默认设置
- **灵活路由**: 每个后端服务在不同的路径下提供服务
- **中间件支持**: 支持认证、日志、错误恢复等中间件

## 文件结构

```
adapter/
├── new_config.go          # 新的配置结构定义
├── new_adapter.go         # 多适配器管理器
├── new_main.go           # 新的主程序入口
├── new_config.json.example # 配置示例文件
└── README_NEW_ARCHITECTURE.md # 本文档
```

## 配置文件结构

### 代理通用配置 (`adapter_proxy`)

```json
{
  "adapter_proxy": {
    "name": "Multi-Adapter Proxy",
    "version": "1.0.0",
    "base_url": "http://localhost:9091",
    "addr": ":9091",
    "options": {
      "panic_if_invalid": false,
      "log_enabled": true,
      "auth_tokens": [],
      "tool_filter": {
        "mode": "allow",
        "tools": []
      }
    }
  }
}
```

### 多服务器配置 (`adapter_servers`)

```json
{
  "adapter_servers": {
    "service_name": {
      "target_api": {
        "base_url": "https://api.example.com",
        "timeout": "30s",
        "auth": {
          "type": "bearer",
          "token": "${API_TOKEN}"
        }
      },
      "tools": [...],
      "prompts": [...],
      "resources": [...],
      "options": {
        "panic_if_invalid": true,
        "log_enabled": true,
        "auth_tokens": ["service-token"],
        "tool_filter": {
          "mode": "allow",
          "tools": ["tool1", "tool2"]
        }
      }
    }
  }
}
```

## 使用方法

### 1. 创建配置文件

复制 `new_config.json.example` 并根据需要修改：

```bash
cp new_config.json.example my_config.json
```

### 2. 设置环境变量

配置文件支持环境变量替换：

```bash
export WEATHER_API_KEY="your_weather_api_key"
export GITHUB_TOKEN="your_github_token"
```

### 3. 启动服务

```bash
go run new_main.go my_config.json
```

或者编译后运行：

```bash
go build -o multi-adapter new_main.go
./multi-adapter my_config.json
```

### 4. 访问服务

每个配置的服务将在不同的路径下可用：

- JSONPlaceholder API: `http://localhost:9091/jsonplaceholder/`
- Weather API: `http://localhost:9091/weather_api/`
- GitHub API: `http://localhost:9091/github_api/`

## 配置选项详解

### 认证配置 (`auth`)

支持多种认证方式：

```json
{
  "auth": {
    "type": "none"  // 无认证
  }
}
```

```json
{
  "auth": {
    "type": "api_key",
    "api_key": "${API_KEY}",
    "location": "query",  // 或 "header"
    "key_name": "appid"
  }
}
```

```json
{
  "auth": {
    "type": "bearer",
    "token": "${BEARER_TOKEN}"
  }
}
```

### 工具过滤 (`tool_filter`)

控制哪些工具可以被访问：

```json
{
  "tool_filter": {
    "mode": "allow",  // 允许模式，只有列出的工具可用
    "tools": ["get_posts", "get_post_by_id"]
  }
}
```

```json
{
  "tool_filter": {
    "mode": "deny",   // 拒绝模式，列出的工具不可用
    "tools": ["dangerous_tool"]
  }
}
```

### 选项配置 (`options`)

- `panic_if_invalid`: 当服务初始化失败时是否停止整个代理
- `log_enabled`: 是否启用请求日志
- `auth_tokens`: 访问此服务所需的认证令牌列表
- `tool_filter`: 工具过滤配置

## 中间件

系统自动为每个服务配置以下中间件：

1. **恢复中间件**: 防止panic导致服务崩溃
2. **日志中间件**: 记录请求信息（可选）
3. **认证中间件**: 验证访问令牌（可选）

## 环境变量支持

配置文件中可以使用 `${VARIABLE_NAME}` 格式引用环境变量：

```json
{
  "target_api": {
    "base_url": "https://api.example.com",
    "auth": {
      "type": "bearer",
      "token": "${API_TOKEN}"
    }
  }
}
```

## 迁移指南

从旧的单服务器架构迁移到新架构：

1. 将原有的 `config.json` 内容移动到 `adapter_servers` 下的一个服务配置中
2. 添加 `adapter_proxy` 配置部分
3. 根据需要调整路由和选项
4. 使用新的主程序 `new_main.go` 启动服务

## 示例场景

### 场景1：多API聚合服务

配置多个不同的API服务（如天气、新闻、社交媒体），在单个代理中提供统一访问接口。

### 场景2：开发环境

为不同的开发阶段配置不同的后端服务（开发、测试、预生产），通过不同路径访问。

### 场景3：API版本管理

同时支持API的多个版本，每个版本作为独立的服务配置。

## 故障排除

### 常见问题

1. **服务启动失败**
   - 检查配置文件格式是否正确
   - 确认环境变量已正确设置
   - 查看日志中的具体错误信息

2. **认证失败**
   - 验证认证令牌是否正确
   - 检查认证配置是否与API要求匹配

3. **工具不可用**
   - 检查工具过滤配置
   - 确认工具名称拼写正确

### 调试模式

启用详细日志以便调试：

```json
{
  "options": {
    "log_enabled": true
  }
}
```

## 性能优化

- 合理设置超时时间
- 使用连接池（已内置）
- 根据需要调整重试机制
- 监控内存和CPU使用情况

## 安全考虑

- 使用环境变量存储敏感信息
- 配置适当的认证令牌
- 定期轮换API密钥
- 使用HTTPS进行生产部署
- 合理配置工具过滤以限制访问