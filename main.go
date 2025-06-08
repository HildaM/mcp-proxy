// main.go 文件是程序的入口点，负责解析命令行参数、加载配置并启动 HTTP 服务器。
// 这个文件非常简洁，仅包含必要的启动逻辑，将主要功能委托给其他模块。
package main

import (
	"flag"
	"fmt"
	"log"
)

// BuildVersion 存储应用程序的当前版本
// 在构建过程中会通过链接器标志(-ldflags)被替换为实际版本信息
var BuildVersion = "dev"

// main 函数是程序的入口点
func main() {
	// 定义并解析命令行参数
	conf := flag.String("config", "config.json", "path to config file or a http(s) url")
	version := flag.Bool("version", false, "print version and exit")
	help := flag.Bool("help", false, "print help and exit")
	flag.Parse()

	// 如果用户请求帮助信息，则显示所有可用的命令行参数
	if *help {
		flag.Usage()
		return
	}

	// 如果用户请求版本信息，则打印版本号并退出
	if *version {
		fmt.Println(BuildVersion)
		return
	}

	// 从指定的配置文件或URL加载配置
	config, err := load(*conf)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	// 使用加载的配置启动HTTP服务器
	// 这个函数会阻塞直到服务器关闭或发生错误
	err = startHTTPServer(config)
	if err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
