.PHONY: build build-linux web test run clean

LINUX_ARCH ?= amd64
BUILD_FLAGS ?= -trimpath -ldflags="-s -w"

# 默认发布构建：先生成前端 dist，再把 dist 嵌入 Go 单二进制。
build: web
	mkdir -p bin
	go build $(BUILD_FLAGS) -o bin/easyagent ./cmd/easyagent

# 交叉编译 Linux 单文件；可用 `make build-linux LINUX_ARCH=arm64` 切换架构。
build-linux: web
	mkdir -p bin
	CGO_ENABLED=0 GOOS=linux GOARCH=$(LINUX_ARCH) go build $(BUILD_FLAGS) -o bin/easyagent-linux-$(LINUX_ARCH) ./cmd/easyagent

# npm ci 严格按照 package-lock.json 安装依赖，适合 CI 和可复现构建。
web:
	cd web && npm ci && npm run build

# 本项目的完整本地检查：依赖校验、Go 静态检查、竞态测试和前端构建。
test:
	cd web && npm ci && npm run build
	go mod verify
	go vet ./...
	go test -race ./...

# build 成功后以前台方式启动；默认监听 0.0.0.0:8080。
run: build
	./bin/easyagent

# 删除可重新生成的构建产物，不会删除 SQLite 数据目录。
clean:
	rm -rf bin web/dist
