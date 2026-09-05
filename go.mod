// module 类似 Maven 的 groupId + artifactId，也是项目内 import 的根路径。
module github.com/lakernote/easy-agent

// 编译本项目所需的最低 Go 工具链版本；不是业务依赖版本。
go 1.26.7

// 第一组是代码直接 import 的依赖，类似 pom.xml 中直接声明的 dependency。
require (
	github.com/BurntSushi/toml v1.5.0 // 读取和安全更新 Codex config.toml
	github.com/modelcontextprotocol/go-sdk v1.7.0 // MCP 官方 Go SDK，用于动态接入外部 Agent 工具
	github.com/wdvxdr1123/go-silk v0.0.0-20220304095002-f67345df09ea // 解码微信 SILK 语音
	modernc.org/sqlite v1.45.0 // 纯 Go SQLite 驱动，不依赖系统 SQLite 或 CGO
)

// indirect 表示传递依赖，类似 Maven 依赖树中的 transitive dependency。
// 该列表由 go mod tidy 维护，业务代码通常不直接 import，也不需要手工逐项升级。
require (
	github.com/dustin/go-humanize v1.0.1 // indirect
	github.com/google/jsonschema-go v0.4.3 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/mattn/go-isatty v0.0.20 // indirect
	github.com/ncruces/go-strftime v1.0.0 // indirect
	github.com/remyoudompheng/bigfft v0.0.0-20230129092748-24d4a6f8daec // indirect
	github.com/segmentio/asm v1.1.3 // indirect
	github.com/segmentio/encoding v0.5.4 // indirect
	github.com/yosida95/uritemplate/v3 v3.0.2 // indirect
	golang.org/x/exp v0.0.0-20260410095643-746e56fc9e2f // indirect
	golang.org/x/oauth2 v0.35.0 // indirect
	golang.org/x/sync v0.20.0 // indirect
	golang.org/x/sys v0.46.0 // indirect
	golang.org/x/time v0.15.0 // indirect
	modernc.org/libc v1.67.6 // indirect
	modernc.org/mathutil v1.7.1 // indirect
	modernc.org/memory v1.11.0 // indirect
)
