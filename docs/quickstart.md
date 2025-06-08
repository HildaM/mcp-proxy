# 快速入门

本文档将引导您快速了解 mcp-proxy 项目的用途、配置和运行方式。

## 项目概述

`mcp-proxy` 是一个基于模型上下文协议 (Model Context Protocol, MCP) 的多路复用代理服务器。

它的核心功能是：

*   **管理多个 MCP 后端**: 它可以同时连接和管理多个 MCP 兼容的后端服务。这些后端服务本身可以是命令行工具（通过 `stdio` 通信）或网络服务（通过 `SSE` 或 `Streamable HTTP` 通信）。
*   **统一的访问入口**: 将所有后端服务聚合起来，并通过一个单一的 HTTP 服务器地址对外提供服务。每个后端服务会被映射到该地址下的一个独立路径（例如 `/github/`, `/fetch/`）。
*   **增加额外功能**: 在代理层为所有后端服务提供了统一的功能增强，包括：
    *   **身份认证 (Authentication)**: 为每个服务或全局配置访问令牌。
    *   **日志记录 (Logging)**: 记录所有流入的请求。
    *   **工具过滤 (Tool Filtering)**: 可以基于白名单或黑名单模式，精细化控制每个后端服务暴露的工具。
    *   **错误恢复 (Recovery)**: 从内部处理流程的 panic 中恢复，保证代理服务器的健壮性。

简而言之，`mcp-proxy` 充当了一个"MCP 网关"，让您可以更方便、更安全、更集中地管理和使用您的各种 MCP AI 功能。

## 配置文件 (`config.json`)

项目通过 `config.json` 文件进行配置。下面是一个示例配置的解析：

```json
{
  "mcpProxy": {
    "baseURL": "https://mcp.example.com",
    "addr": ":9090",
    "name": "MCP Proxy",
    "version": "1.0.0",
    "options": {
      "panicIfInvalid": false,
      "logEnabled": true,
      "authTokens": [
        "DefaultTokens"
      ]
    }
  },
  "mcpServers": {
    "github": {
      "command": "npx",
      "args": [
        "-y",
        "@modelcontextprotocol/server-github"
      ],
      // ...
    },
    "fetch": {
      "command": "uvx",
      "args": [
        "mcp-server-fetch"
      ],
      // ...
    },
    "amap": {
      "url": "https://mcp.amap.com/sse?key=<YOUR_TOKEN>"
    }
  }
}
```

### `mcpProxy`

这个部分定义了代理服务器本身的行为：

*   `baseURL`: 代理服务器对外暴露的基础 URL。
*   `addr`: 代理服务器监听的本地地址和端口，例如 `:9090`。
*   `name`, `version`: 代理服务器的名称和版本。
*   `options`: 全局选项。
    *   `logEnabled`: 是否开启请求日志。
    *   `authTokens`: 全局生效的访问令牌列表。请求头中需要包含 `Authorization: Bearer <token>`。
    *   `panicIfInvalid`: 如果后端客户端在初始化时失败，是否让整个代理服务崩溃。

### `mcpServers`

这个部分定义了所有被代理的后端 MCP 服务。它是一个键值对，键是您为服务起的名字（会用在 URL 路径中），值是服务的具体配置。

**后端服务类型:**

1.  **`stdio` 类型**: 通过执行本地命令并使用标准输入/输出进行通信。
    *   `command`: 要执行的命令 (e.g., `npx`, `python`)。
    *   `args`: 传递给命令的参数列表。
    *   `env`: 为命令设置的环境变量。

2.  **`sse` 或 `streamable-http` 类型**: 通过网络连接到一个远程服务。
    *   `url`: 远程服务的 URL。
    *   `headers`: 连接时需要发送的 HTTP 头。
    *   `timeout`: 连接超时时间（仅 `streamable-http`）。

**后端服务特定选项 (`options`):**

每个后端服务都可以有自己的 `options`，这些选项会覆盖 `mcpProxy` 中的全局选项。

*   `authTokens`: 为这个特定服务设置的访问令牌。
*   `toolFilter`: 工具过滤器。
    *   `mode`: `allow` (白名单) 或 `block` (黑名单)。
    *   `list`: 工具名称列表。

## 如何运行

下面我们将分步介绍如何启动 `mcp-proxy` 服务。无论您选择哪种方式，核心都是 **准备好一个 `config.json` 配置文件** 并让程序能够读取到它。

### 第一步：准备 `config.json`

项目根目录下已经为您提供了一个名为 `config.json` 的示例文件。这是启动服务的基础。

1.  **检查文件**: 打开项目根目录下的 `config.json` 文件。
2.  **修改配置**: 这个示例配置中包含了一些需要您根据自己情况修改的占位符，例如：
    *   在 `github` 服务的 `env` 中，将 `<YOUR_TOKEN>` 替换为您的 GitHub Personal Access Token。
    *   在 `amap` 服务的 `url` 中，将 `<YOUR_TOKEN>` 替换为您的 Amap Key。
    如果您暂时没有这些服务的令牌，可以先将 `mcpServers` 中对应的服务（如 `"github": { ... }`）整个删除，以避免启动时出错。

### 第二步：选择一种方式启动服务

#### 方式一：直接通过 Go 源码运行（推荐开发时使用）

这种方式最直接，可以看到完整的日志输出，方便调试。

**环境准备**:
*   安装 [Go](https://go.dev/doc/install) (版本 >= 1.20)
*   一个命令行终端（Terminal）

**启动步骤**:
1.  在命令行中，进入 `mcp-proxy` 项目的根目录。
2.  运行以下命令：
    ```bash
    go run . --config config.json
    ```
    *   `go run .` 会编译并运行当前目录的 Go 代码。
    *   `--config config.json` 是一个明确的参数，告诉程序在当前目录下寻找 `config.json` 作为配置文件。
3.  如果看到 `Starting SSE server listening on :9090` 和 `All clients initialized` 等日志，说明服务已成功启动。

#### 方式二：通过 Docker 运行（推荐生产环境使用）

这种方式可以将服务打包成一个独立的容器运行，环境纯净且便于部署。

**环境准备**:
*   安装 [Docker](https://docs.docker.com/engine/install/) 和 [Docker Compose](https://docs.docker.com/compose/install/)

**启动步骤**:
1.  确保您已经按照 **第一步** 的说明，在项目根目录准备好了 `config.json` 文件。这是因为 `docker-compose.yaml` 文件配置了将本地的 `config.json` 挂载到容器内部。
2.  在命令行中，进入 `mcp-proxy` 项目的根目录。
3.  运行以下命令来启动服务：
    ```bash
    docker-compose up -d
    ```
    *   `up` 会创建并启动服务。
    *   `-d` 参数表示在后台（detached mode）运行。
4.  **查看日志**: 您可以使用以下命令来查看服务的实时日志：
    ```bash
    docker-compose logs -f
    ```
5.  **停止服务**: 如果需要停止服务，可以运行：
    ```bash
    docker-compose down
    ```

## 代码逻辑概览

1.  **`main.go`**: 程序入口。负责解析命令行参数（如配置文件路径），加载配置，并启动 HTTP 服务器。
2.  **`config.go`**: 定义了所有配置项的 Go 结构体。核心逻辑在于 `load` 函数，它使用 `confstore` 库从 JSON 文件加载配置。
3.  **`http.go`**: HTTP 服务器的核心。`startHTTPServer` 函数会：
    *   遍历 `config.json` 中定义的所有 `mcpServers`。
    *   为每一个 `server` 创建对应的 MCP 客户端 (`client.go`) 和 MCP 服务端 (`server` from `mcp-go` library)。
    *   为每个服务注册一个 HTTP Handler，路径为 `/<server_name>/`。
    *   使用中间件（Middleware）模式来注入日志、认证和恐慌恢复功能。
    *   实现了优雅停机（Graceful Shutdown）。
4.  **`client.go`**: 这是连接和适配后端 MCP 服务的关键。
    *   `newMCPClient` 函数根据配置创建不同类型（`stdio`, `sse`, `streamable-http`）的 `mcp-go` 客户端实例。
    *   `addToMCPServer` 方法是核心：它通过 MCP 协议与后端服务进行"握手"（`Initialize`），然后获取后端提供的所有能力（`Tools`, `Prompts`, `Resources`），并将这些能力注册到代理的 `MCPServer` 实例上。
    *   在注册 `Tool` 时，它会将实际的 `CallTool` 请求转发回原始的后端客户端，从而实现了代理的核心功能。工具过滤逻辑也在此处实现。

该项目巧妙地利用了 `mcp-go` 库，构建了一个功能强大且可扩展的代理服务器，极大地简化了对多个异构 MCP 后端服务的管理和集成。 