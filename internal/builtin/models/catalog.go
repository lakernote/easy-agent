// Package models 集中维护 EasyAgent 自带的模型配置模板。
//
// 模型端点和型号会随厂商变化，因此它们属于可替换的产品目录，不参与
// Agent 的路由判断。模板只负责帮用户填表，保存后仍是普通 ModelSettings。
package models

// Preset 是一个 OpenAI 兼容模型模板。Ready 由服务器根据环境变量是否
// 存在动态填充，不会把密钥内容发送到页面。
type Preset struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	Provider    string `json:"provider"`
	Protocol    string `json:"protocol"`
	BaseURL     string `json:"baseUrl"`
	Model       string `json:"model"`
	APIKeyEnv   string `json:"apiKeyEnv"`
	Thinking    string `json:"thinking"`
	Free        bool   `json:"free"`
	Ready       bool   `json:"ready"`
}

// Catalog 返回适合试用 EasyAgent 的免费额度模板。
// “免费”只表示厂商当前提供免费路由或免费层，通常仍需注册并创建 API Key；
// 额度、地区和模型可用性以厂商实时规则为准。
func Catalog() []Preset {
	return []Preset{
		{
			ID: "gemini-flash-lite", Name: "Gemini 2.5 Flash-Lite", Provider: "gemini",
			Description: "免费云模型首选；速度快，支持通用对话和工具调用。",
			Protocol:    "chat_completions", BaseURL: "https://generativelanguage.googleapis.com/v1beta/openai",
			Model: "gemini-2.5-flash-lite", APIKeyEnv: "GEMINI_API_KEY", Thinking: "", Free: true,
		},
		{
			ID: "groq-gpt-oss", Name: "Groq GPT-OSS 20B", Provider: "groq",
			Description: "Groq 免费开发额度；推理速度快，但有每分钟和每日限额。",
			Protocol:    "chat_completions", BaseURL: "https://api.groq.com/openai/v1",
			Model: "openai/gpt-oss-20b", APIKeyEnv: "GROQ_API_KEY", Thinking: "", Free: true,
		},
		{
			ID: "cerebras-gpt-oss", Name: "Cerebras GPT-OSS 120B", Provider: "cerebras",
			Description: "高速免费额度；模型更大，适合 Agent 与代码任务试用。",
			Protocol:    "chat_completions", BaseURL: "https://api.cerebras.ai/v1",
			Model: "gpt-oss-120b", APIKeyEnv: "CEREBRAS_API_KEY", Thinking: "", Free: true,
		},
		{
			ID: "openrouter-free", Name: "OpenRouter Free · 实验", Provider: "openrouter",
			Description: "随机选择可用免费模型，能力和延迟可能变化；仅适合低频试验。",
			Protocol:    "chat_completions", BaseURL: "https://openrouter.ai/api/v1",
			Model: "openrouter/free", APIKeyEnv: "OPENROUTER_API_KEY", Thinking: "", Free: true,
		},
	}
}
