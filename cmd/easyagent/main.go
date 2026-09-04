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
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/lakernote/easy-agent/internal/appenv"
	"github.com/lakernote/easy-agent/internal/server"
	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/web"
)

const defaultListenAddress = "0.0.0.0:8080"

// Release builds replace these values through -ldflags. Keeping usable
// fallbacks makes local `go build` and development binaries self-describing.
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// 1. 先解析启动参数，使 -version 和 -help 不会创建应用目录。
	address := flag.String("listen", defaultListenAddress, "HTTP listen address")
	databasePath := flag.String("db", "", "SQLite database file (default ~/.easyagent/easyagent.db)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("easyagent %s (%s)\n", version, commit)
		return
	}

	// 2. 初始化 EasyAgent 自己管理的 Home、默认工作区、私有运行时和 PATH。
	// 这些是应用内部状态，不要求用户在启动命令中配置；真正执行任务时，用户可在
	// 页面为新会话选择工作区。
	environment, err := appenv.Open(appenv.Config{})
	if err != nil {
		log.Fatalf("open runtime environment: %v", err)
	}
	if strings.TrimSpace(*databasePath) == "" {
		*databasePath = filepath.Join(environment.Home(), "easyagent.db")
	}

	// 3. 先独占 HTTP 端口，再打开数据库或恢复任务状态。这样即使用户、launchd
	// 或其他进程误启动了第二个实例，它也会在触碰 SQLite 前退出，不会把第一个
	// 实例正在执行的会话误判成“服务重启中断”。
	listener, err := net.Listen(listenNetwork(*address), *address)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			port := *address
			if _, configuredPort, splitErr := net.SplitHostPort(*address); splitErr == nil {
				port = configuredPort
			}
			log.Fatalf("listen %s: address already in use; stop the existing EasyAgent process or choose another port with -listen, for example -listen 0.0.0.0:8081 (check with: ss -ltnp | grep :%s)", *address, port)
		}
		log.Fatalf("listen %s: %v", *address, err)
	}
	defer listener.Close()

	// 4. 初始化持久化层。
	// Go 常用“返回值 + error”代替 Java 异常；err != nil 就是显式处理失败分支。
	database, err := store.Open(*databasePath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	// defer 类似 finally：函数正常返回前会关闭数据库，多个 defer 按后进先出执行。
	// 注意 log.Fatal/os.Exit 会直接结束进程而不执行 defer；启动失败时资源由 OS 回收。
	defer database.Close()

	// 5. 修复上次进程异常退出留下的会话状态。
	// 单机版没有外部任务队列，因此把中断的 running 状态改成用户可理解的失败。
	if err := database.RecoverRunning(time.Now()); err != nil {
		log.Fatalf("recover interrupted sessions: %v", err)
	}

	// 6. 读取编译进二进制的前端文件，并完成 Server 依赖组装。
	// web.DistFS 使用 go:embed，因此部署时只需要 easyagent 二进制和可写数据目录。
	assets, err := web.DistFS()
	if err != nil {
		log.Fatalf("load frontend: %v", err)
	}
	application := server.New(database, assets, environment)

	// 7. 创建整个服务共享的生命周期 Context。
	// Context 可类比 Java 的 CancellationToken + deadline；收到 Ctrl+C 或 SIGTERM 后
	// Done() 会关闭，HTTP Server 和 Agent 都能协作停止。
	serviceContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	displayAddress := *address
	if strings.HasPrefix(displayAddress, "0.0.0.0:") {
		displayAddress = "127.0.0.1" + strings.TrimPrefix(displayAddress, "0.0.0.0")
	} else if strings.HasPrefix(displayAddress, ":") {
		displayAddress = "localhost" + displayAddress
	}
	boundAddress := listener.Addr().String()

	// 8. 配置标准库 HTTP Server。
	// server.Server 实现业务路由并通过 Handler() 暴露 http.Handler；超时用于避免慢连接
	// 长期占用单机资源。1 << 20 是位移写法，等于 1 MiB。
	httpServer := &http.Server{
		Addr:              *address,
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		// 私有 MCP 首次安装和握手可能需要几分钟；安装与连接内部仍有更短超时。
		WriteTimeout:   4 * time.Minute,
		IdleTimeout:    90 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}

	// buffered channel 容量为 1，用来把监听协程的最终错误传回 main。
	// 这相当于只有一个结果的 Future/BlockingQueue，但 channel 是 Go 原生同步原语。
	listenErrors := make(chan error, 1)
	go func() {
		log.Printf("EasyAgent is running at http://%s (bound to %s)", displayAddress, boundAddress)
		listenErrors <- httpServer.Serve(listener)
	}()

	// 9. select 同时等待“操作系统要求退出”或“HTTP 服务意外停止”，谁先发生就处理谁。
	select {
	case <-serviceContext.Done():
		log.Printf("EasyAgent is shutting down")
	case err := <-listenErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped: %v", err)
		}
	}

	// 10. 分两个阶段优雅停机。HTTP 收尾和 Runner/Agent 收尾不能共用一个
	// deadline，否则慢连接会耗尽 Runner 清理 Codex/MCP 子进程的时间。
	httpShutdownContext, cancelHTTPShutdown := context.WithTimeout(context.Background(), 5*time.Second)
	if err := httpServer.Shutdown(httpShutdownContext); err != nil {
		log.Printf("HTTP shutdown: %v", err)
	}
	cancelHTTPShutdown()
	runnerShutdownContext, cancelRunnerShutdown := context.WithTimeout(context.Background(), 15*time.Second)
	if err := application.Shutdown(runnerShutdownContext); err != nil {
		log.Printf("runner shutdown: %v", err)
	}
	cancelRunnerShutdown()
}

func listenNetwork(address string) string {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return "tcp"
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "tcp"
	}
	if ip.To4() != nil {
		return "tcp4"
	}
	return "tcp6"
}
