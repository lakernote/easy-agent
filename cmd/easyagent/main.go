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

// 构建时通过 -ldflags 注入，便于确认部署二进制对应的版本和提交。
var (
	version = "dev"
	commit  = "unknown"
)

func main() {
	// 解析启动参数
	address := flag.String("listen", defaultListenAddress, "HTTP listen address")
	databasePath := flag.String("db", "", "SQLite database file (default ~/.easyagent/easyagent.db)")
	showVersion := flag.Bool("version", false, "print version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Printf("easyagent %s (%s)\n", version, commit)
		return
	}

	// 初始化 ~/.easyagent 和 PATH
	environment, err := appenv.Open(appenv.Config{})
	if err != nil {
		log.Fatalf("open runtime environment: %v", err)
	}
	if strings.TrimSpace(*databasePath) == "" {
		*databasePath = filepath.Join(environment.Home(), "easyagent.db")
	}

	// 先监听端口，避免第二个实例在端口冲突时触碰数据库。
	listener, err := net.Listen(listenNetwork(*address), *address)
	if err != nil {
		if errors.Is(err, syscall.EADDRINUSE) {
			log.Fatalf("listen %s: address already in use; stop the existing EasyAgent process or choose another port with -listen, for example -listen 0.0.0.0:8081", *address)
		}
		log.Fatalf("listen %s: %v", *address, err)
	}

	// 打开 SQLite
	database, err := store.Open(*databasePath)
	if err != nil {
		log.Fatalf("open store: %v", err)
	}
	defer database.Close()

	// 将上次异常退出时仍在运行的任务标记为中断。
	if err := database.RecoverRunning(time.Now()); err != nil {
		log.Fatalf("recover interrupted sessions: %v", err)
	}

	// 加载嵌入式 Web UI 并组装应用。
	assets, err := web.DistFS()
	if err != nil {
		log.Fatalf("load frontend: %v", err)
	}
	application, err := server.New(database, assets, environment)
	if err != nil {
		log.Fatalf("initialize server: %v", err)
	}

	// 监听进程退出信号。
	serviceContext, stopSignals := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stopSignals()
	displayAddress := displayListenAddress(*address, listener.Addr())
	boundAddress := listener.Addr().String()

	// 启动 HTTP 服务。SSE 需要长期连接，因此不设置全局写超时。
	httpServer := &http.Server{
		Handler:           application.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      0,
		IdleTimeout:       90 * time.Second,
		MaxHeaderBytes:    1 << 20,
	}

	listenErrors := make(chan error, 1)
	go func() {
		log.Printf("EasyAgent is running at http://%s (bound to %s)", displayAddress, boundAddress)
		listenErrors <- httpServer.Serve(listener)
	}()

	// 等待退出信号或 HTTP 服务异常停止。
	select {
	case <-serviceContext.Done():
		log.Printf("EasyAgent is shutting down")
	case err := <-listenErrors:
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Printf("HTTP server stopped: %v", err)
		}
	}

	// 先通知后台任务和 SSE 退出，再等待 HTTP 请求与运行时依次收尾。
	application.BeginShutdown()
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

func displayListenAddress(configured string, bound net.Addr) string {
	host, _, err := net.SplitHostPort(configured)
	if err != nil {
		return bound.String()
	}
	_, port, err := net.SplitHostPort(bound.String())
	if err != nil {
		return configured
	}
	switch host {
	case "":
		host = "localhost"
	case "0.0.0.0":
		host = "127.0.0.1"
	case "::":
		host = "::1"
	}
	return net.JoinHostPort(host, port)
}
