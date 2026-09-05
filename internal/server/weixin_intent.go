package server

import "strings"

type weixinIntent struct {
	Command    string
	Confidence int
	Explicit   bool
}

type weixinIntentParser struct {
	explicit   map[string]string
	help       map[string]struct{}
	newSession map[string]struct{}
	status     map[string]struct{}
	stop       map[string]struct{}
}

var defaultWeixinIntentParser = newWeixinIntentParser()

func weixinCommand(value string) string {
	return defaultWeixinIntentParser.Parse(value).Command
}

func newWeixinIntentParser() *weixinIntentParser {
	parser := &weixinIntentParser{
		explicit:   map[string]string{"/help": "help", "/new": "new", "/status": "status", "/stop": "stop"},
		help:       phraseSet("帮助", "使用帮助", "使用说明", "命令", "可用命令", "有哪些命令", "怎么用", "如何使用", "做什么", "哪些操作", "支持什么", "你能做什么", "你会什么"),
		newSession: make(map[string]struct{}),
		status:     make(map[string]struct{}),
		stop:       make(map[string]struct{}),
	}
	parser.buildHelpGrammar()
	parser.buildNewSessionGrammar()
	parser.buildStatusGrammar()
	parser.buildStopGrammar()
	return parser
}

func (parser *weixinIntentParser) Parse(value string) weixinIntent {
	normalized := normalizeWeixinIntent(value)
	if command, ok := parser.explicit[normalized]; ok {
		return weixinIntent{Command: command, Confidence: 100, Explicit: true}
	}
	if normalized == "" || len([]rune(normalized)) > 40 || containsPhrase(normalized, weixinIntentNegations) || containsPhrase(normalized, weixinIntentDiscussion) {
		return weixinIntent{}
	}
	core := trimWeixinIntentDecorators(normalized)
	if _, ok := parser.help[core]; ok {
		return weixinIntent{Command: "help", Confidence: 90}
	}
	if _, ok := parser.newSession[core]; ok {
		return weixinIntent{Command: "new", Confidence: 92}
	}
	if _, ok := parser.status[core]; ok {
		return weixinIntent{Command: "status", Confidence: 90}
	}
	if _, ok := parser.stop[core]; ok {
		return weixinIntent{Command: "stop", Confidence: 96}
	}
	return weixinIntent{}
}

func (parser *weixinIntentParser) buildHelpGrammar() {
	for _, subject := range []string{"", "你"} {
		for _, verb := range []string{"支持", "可以做", "能做", "会"} {
			for _, question := range []string{"什么", "哪些操作", "哪些命令", "哪些事情"} {
				parser.help[subject+verb+question] = struct{}{}
			}
		}
	}
}

func (parser *weixinIntentParser) buildNewSessionGrammar() {
	actions := []string{"", "创建", "新建", "开始", "开启", "开", "重开", "重新创建", "重新开始", "切换到", "换到"}
	quantifiers := []string{"", "一个", "个"}
	modifiers := []string{"", "新", "新的", "全新"}
	objects := []string{"会话", "对话", "聊天"}
	for _, action := range actions {
		for _, quantifier := range quantifiers {
			for _, modifier := range modifiers {
				if action == "" && modifier == "" {
					continue
				}
				for _, object := range objects {
					parser.newSession[action+quantifier+modifier+object] = struct{}{}
				}
			}
		}
	}
	for _, action := range []string{"", "创建", "新建", "开始", "开启"} {
		for _, quantifier := range quantifiers {
			for _, modifier := range modifiers {
				if action == "" && modifier == "" {
					continue
				}
				parser.newSession[action+quantifier+modifier+"任务"] = struct{}{}
			}
		}
	}
}

func (parser *weixinIntentParser) buildStatusGrammar() {
	queries := []string{"", "看", "看看", "看下", "查看", "查", "查询", "告诉我"}
	fillers := []string{"", "一下"}
	times := []string{"", "现在", "当前"}
	objects := []string{"", "任务", "这个任务", "当前任务", "会话", "这个会话", "当前会话"}
	for _, query := range queries {
		for _, filler := range fillers {
			for _, moment := range times {
				for _, object := range objects {
					for _, possessive := range []string{"", "的"} {
						for _, state := range []string{"状态", "进度", "情况"} {
							parser.status[query+filler+moment+object+possessive+state] = struct{}{}
						}
					}
					for _, progress := range []string{"跑到哪了", "跑到哪里了", "执行到哪了", "执行到哪里了", "进行到哪了", "进行到哪里了", "跑得怎么样", "执行得怎么样", "完成了吗", "结束了吗", "跑完了吗", "还在运行吗", "还在跑吗", "怎么样了"} {
						parser.status[query+filler+moment+object+progress] = struct{}{}
					}
				}
			}
		}
	}
}

func (parser *weixinIntentParser) buildStopGrammar() {
	actions := []string{"停止", "停下", "停掉", "停", "取消", "中止", "终止"}
	objects := []string{"", "任务", "这个任务", "当前任务", "现在的任务", "会话", "这个会话", "当前会话"}
	for _, action := range actions {
		for _, suffix := range []string{"", "一下"} {
			for _, object := range objects {
				parser.stop[action+suffix+object] = struct{}{}
				parser.stop[object+action+suffix] = struct{}{}
				if object != "" {
					parser.stop["把"+object+action+suffix] = struct{}{}
				}
			}
		}
	}
}

var weixinIntentPrefixes = []string{"麻烦帮我", "能不能帮我", "可以帮我", "请帮我", "我想要", "麻烦", "请", "我想", "我要", "能不能", "可以", "帮我", "给我", "先"}
var weixinIntentSuffixes = []string{"可以吗", "好不好", "行不行", "好吗", "谢谢", "一下吧", "吧", "呢", "呀", "啊", "啦"}
var weixinIntentNegations = []string{"不要", "先别", "别", "不必", "不用", "无需", "不能", "不可", "不想"}
var weixinIntentDiscussion = []string{"怎么实现", "如何实现", "怎么做的", "原理", "代码", "按钮", "接口", "文档", "正则", "prompt", "tool", "测试"}

func normalizeWeixinIntent(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.NewReplacer("／", "/", "，", "", ",", "", "。", "", "！", "", "!", "", "？", "", "?", "", "；", "", ";", "", "～", "", "~", "").Replace(value)
	return strings.Join(strings.Fields(value), "")
}

func trimWeixinIntentDecorators(value string) string {
	for {
		before := value
		value = trimFirstPrefix(value, weixinIntentPrefixes)
		value = trimFirstSuffix(value, weixinIntentSuffixes)
		if value == before {
			return value
		}
	}
}

func trimFirstPrefix(value string, candidates []string) string {
	for _, candidate := range candidates {
		if strings.HasPrefix(value, candidate) {
			return strings.TrimPrefix(value, candidate)
		}
	}
	return value
}

func trimFirstSuffix(value string, candidates []string) string {
	for _, candidate := range candidates {
		if strings.HasSuffix(value, candidate) {
			return strings.TrimSuffix(value, candidate)
		}
	}
	return value
}

func containsPhrase(value string, candidates []string) bool {
	for _, candidate := range candidates {
		if strings.Contains(value, candidate) {
			return true
		}
	}
	return false
}

func phraseSet(values ...string) map[string]struct{} {
	result := make(map[string]struct{}, len(values))
	for _, value := range values {
		result[value] = struct{}{}
	}
	return result
}
