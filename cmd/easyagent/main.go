// Package main 是 EasyAgent 单机服务的可执行入口。
//
// Go 约定只有 package main 中的 main() 才能生成可执行文件；可以把它理解成
// Java 的 public static void main(String[] args)。真正的业务实现放在 internal/server，
// 这里仅负责组装依赖、管理进程生命周期和启动 HTTP 服务。
package main

import (
	"context"
	"errors"
	"flag"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/lakernote/easy-agent/internal/server"
	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/web"
)

func main() {
	// 1. 解析命令行参数。
	// flag.String 返回 *string（字符串指针）；flag.Parse 后通过 *address 取实际值。
	address := flag.String("listen", "127.0.0.1:8080", "HTTP listen address")
	databasePath := "./data/easyagent.db"
	flag.StringVar(&databasePath, "db", databasePath, "SQLite database file")
	flag.Parse()

	// 2. 初始化持久化层。
	// Go 常用“返回值 + error”代替 Java 异常；err != nil 就是显式处理失败分支。
	database, err := store.Open(databasePath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	// defer 类似 finally：函数正常返回前会关闭数据库，多个 defer 按后进先出执行。
	// 注意 log.Fatal/os.Exit 会直接结束进程而不执行 defer；启动失败时资源由 OS 回收。
	defer database.Close()

	// 3. 修复上次进程异常退出留下的会话状态。
	// 单机版没有外部任务队列，因此把中断的 running 状态改成用户可理解的失败。
	if err := database.RecoverRunning(time.Now()); err != nil {
		log.Fatalf("recover interrupted sessions: %v", err)
	}

	// 4. 读取编译进二进制的前端文件，并完成 Server 依赖组装。
	// web.DistFS 使用 go:embed，因此部署时只需要 easyagent 二进制和可写数据目录。
	assets, err := web.DistFS()
	if err != nil {
		log.Fatalf("load frontend: %v", err)
	}
	application := server.New(database, assets)

	// 5. 创建整个服务共享的生命周期 Context。
	// Context 可类比 Java 的 CancellationToken + deadline；收到 Ctrl+C 或 SIGTERM 后
	// Done() 会关闭，HTTP Server 和 Agent 都能协作停止。
	serviceContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	displayAddress := *address
	if strings.HasPrefix(displayAddress, ":") {
		displayAddress = "localhost" + displayAddress
	}

	// 6. 配置标准库 HTTP Server。
	// server.Server 实现业务路由并通过 Handler() 暴露 http.Handler；超时用于避免慢连接
	// 长期占用单机资源。1 << 20 是位移写法，等于 1 MiB。
	httpServer := &http.Server{
		Addr:              *address,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 一键检测 MCP 首次可能需要由 npx 下载依赖，给该请求留出完整连接窗口。
		WriteTimeout:   2 * time.Minute,
		IdleTimeout:    90 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// buffered channel 容量为 1，用来把监听协程的最终错误传回 main。
	// 这相当于只有一个结果的 Future/BlockingQueue，但 channel 是 Go 原生同步原语。
	listenErrors := make(chan error, 1)
	go func() {
		log.Printf("EasyAgent is running at http://%s", displayAddress)
		listenErrors <- httpServer.ListenAndServe()
	}()

	// 7. select 同时等待“操作系统要求退出”或“HTTP 服务意外停止”，谁先发生就处理谁。
	select {
	case <-serviceContext.Done():
		log.Printf("EasyAgent is shutting down")
	case err := <-listenErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped: %v", err)
		}
	}

	// 8. 优雅停机。先停止接收 HTTP 请求，再等待 Runner/Agent 等后台任务退出。
	// 15 秒 deadline 防止某个任务永久阻塞进程关闭。
	shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancelShutdown()
	if err := httpServer.Shutdown(shutdownContext); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	if err := application.Shutdown(shutdownContext); err != nil {
		log.Printf("runner shutdown: %v", err)
	}
}
