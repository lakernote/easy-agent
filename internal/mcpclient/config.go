package mcpclient

// Config 是 MCP 连接层自己的配置模型。它刻意不复用 store.MCPConfig，避免
// 协议适配层反向依赖 SQLite 持久化模型；server 负责在两种模型之间做转换。
type Config struct {
	ID          string
	Name        string
	Description string
	Enabled     bool
	Transport   string
	Command     string
	Args        []string
	Endpoint    string
	AuthType    string
	Token       string
	Username    string
	Password    string
	Headers     map[string]string
	Environment map[string]string
}
