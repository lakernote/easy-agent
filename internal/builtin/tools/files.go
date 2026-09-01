package tools

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/agent"
)

const (
	maxFileBytes      = 4 * 1024 * 1024
	maxReadLines      = 2000
	defaultReadLines  = 200
	maxSearchResults  = 200
	maxFindResults    = 500
	maxFileToolOutput = 64 * 1024
)

// fileWorkspace 是一轮 Agent 共享的文件工作区。所有返回路径都相对工作区，
// 既减少 Token，也避免 Trace 把服务器用户名和绝对目录暴露到截图中。
type fileWorkspace struct {
	root  string
	mu    sync.Mutex
	reads map[string][sha256.Size]byte
}

func newFileWorkspace(root string) *fileWorkspace {
	root, _ = filepath.Abs(root)
	if resolved, resolveErr := filepath.EvalSymlinks(root); resolveErr == nil {
		root = resolved
	}
	return &fileWorkspace{root: filepath.Clean(root), reads: map[string][sha256.Size]byte{}}
}

func (workspace *fileWorkspace) tools() []agent.Tool {
	return []agent.Tool{
		workspace.readTool(), workspace.grepTool(), workspace.findTool(), workspace.listTool(), workspace.editTool(), workspace.writeTool(),
	}
}

func (workspace *fileWorkspace) readTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "read",
			Description: "读取工作区内已经存在的 UTF-8 文本文件，可指定起始行和行数。仅在任务需要检查文件内容时使用；普通问答无需调用。处理代码和文本时优先于 shell cat。",
			Parameters: objectSchema(map[string]any{
				"path":   stringSchema("相对工作区的文件路径，也接受工作区内绝对路径"),
				"offset": map[string]any{"type": "integer", "description": "可选，起始行（从 1 开始），默认 1", "minimum": 1},
				"limit":  map[string]any{"type": "integer", "description": "可选，最多读取行数，默认 200，最大 2000", "minimum": 1, "maximum": maxReadLines},
			}, []string{"path"}),
		},
		Run: workspace.read,
	}
}

func (workspace *fileWorkspace) grepTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "grep",
			Description: "在工作区文本文件中搜索内容，返回相对路径、行号和匹配行。搜索代码时优先使用它，不要用 shell grep/rg。",
			Parameters: objectSchema(map[string]any{
				"query":          stringSchema("要搜索的文本或正则表达式"),
				"path":           stringSchema("可选，搜索文件或目录，默认工作区根目录"),
				"glob":           stringSchema("可选文件过滤，例如 *.go、web/**/*.tsx"),
				"regex":          map[string]any{"type": "boolean", "description": "query 是否为 Go 正则表达式，默认 false"},
				"case_sensitive": map[string]any{"type": "boolean", "description": "是否区分大小写，默认 false"},
				"limit":          map[string]any{"type": "integer", "description": "可选，最多返回 200 条", "minimum": 1, "maximum": maxSearchResults},
			}, []string{"query"}),
		},
		Run: workspace.grep,
	}
}

func (workspace *fileWorkspace) findTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "find",
			Description: "按名称或 Glob 查找工作区中的文件，自动跳过 .git、node_modules 等生成目录。",
			Parameters: objectSchema(map[string]any{
				"pattern": stringSchema("文件名关键字或 Glob，例如 runner、*.go、web/**/*.tsx"),
				"path":    stringSchema("可选，起始目录，默认工作区根目录"),
				"limit":   map[string]any{"type": "integer", "description": "可选，最多返回 500 条", "minimum": 1, "maximum": maxFindResults},
			}, []string{"pattern"}),
		},
		Run: workspace.find,
	}
}

func (workspace *fileWorkspace) listTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "ls",
			Description: "列出工作区内一个目录的直接子项，返回名称、相对路径、类型和大小。",
			Parameters: objectSchema(map[string]any{
				"path":  stringSchema("可选目录，默认工作区根目录"),
				"limit": map[string]any{"type": "integer", "description": "可选，最多返回 500 项", "minimum": 1, "maximum": maxFindResults},
			}, nil),
		},
		Run: workspace.list,
	}
}

func (workspace *fileWorkspace) editTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "edit",
			Description: "精确替换工作区文本文件中的内容并返回修改预览。仅当用户要求修改工作区文件时使用，不要把‘写文章/写说明’误解成写入服务器文件。默认要求 old_text 只出现一次。",
			Parameters: objectSchema(map[string]any{
				"path":        stringSchema("要修改的文件路径"),
				"old_text":    stringSchema("文件中必须存在的原文，默认必须唯一"),
				"new_text":    stringSchema("替换后的文本，可以为空"),
				"replace_all": map[string]any{"type": "boolean", "description": "明确设为 true 时替换全部匹配；默认 false"},
			}, []string{"path", "old_text", "new_text"}),
		},
		Run: workspace.edit,
	}
}

func (workspace *fileWorkspace) writeTool() agent.Tool {
	return agent.Tool{
		Spec: agent.ToolSpec{
			Name:        "write",
			Description: "创建或完整覆盖工作区文本文件。仅当用户明确要求把内容写入文件时使用；用户只要求生成文章、代码示例或回答时直接在对话中输出。覆盖现有文件前必须 read，小范围修改优先 edit。",
			Parameters: objectSchema(map[string]any{
				"path":      stringSchema("要创建或覆盖的文件路径；父目录必须已经存在"),
				"content":   stringSchema("完整 UTF-8 文件内容"),
				"overwrite": map[string]any{"type": "boolean", "description": "覆盖现有文件时必须明确为 true，并且文件必须在本轮调用 read 后未发生变化"},
			}, []string{"path", "content"}),
		},
		Run: workspace.write,
	}
}

func (workspace *fileWorkspace) read(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Path   string `json:"path"`
		Offset int    `json:"offset"`
		Limit  int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("read 参数错误: %w", err)
	}
	absolute, relative, err := workspace.resolveExisting(input.Path)
	if err != nil {
		return "", err
	}
	content, err := readTextFile(absolute)
	if err != nil {
		return "", err
	}
	workspace.rememberRead(absolute, content)
	lines := splitLines(string(content))
	offset := input.Offset
	if offset <= 0 {
		offset = 1
	}
	limit := bounded(input.Limit, defaultReadLines, maxReadLines)
	if offset > len(lines) && len(lines) > 0 {
		return "", fmt.Errorf("read 起始行 %d 超过文件总行数 %d", offset, len(lines))
	}
	start := min(max(offset-1, 0), len(lines))
	end := min(start+limit, len(lines))
	selected := strings.Join(lines[start:end], "\n")
	selected, bytesTruncated := truncateUTF8(selected, maxFileToolOutput)
	return jsonResult(map[string]any{
		"ok": true, "path": relative, "start_line": start + 1, "end_line": end,
		"total_lines": len(lines), "truncated": end < len(lines) || bytesTruncated, "content": selected,
	})
}

func (workspace *fileWorkspace) list(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Path  string `json:"path"`
		Limit int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("ls 参数错误: %w", err)
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = "."
	}
	absolute, relative, err := workspace.resolveExisting(input.Path)
	if err != nil {
		return "", err
	}
	entries, err := os.ReadDir(absolute)
	if err != nil {
		return "", fmt.Errorf("读取目录失败: %w", err)
	}
	limit := bounded(input.Limit, 200, maxFindResults)
	items := make([]map[string]any, 0, min(len(entries), limit))
	for _, entry := range entries[:min(len(entries), limit)] {
		kind, size := "file", int64(0)
		info, infoErr := entry.Info()
		if entry.IsDir() {
			kind = "directory"
		} else if entry.Type()&os.ModeSymlink != 0 {
			kind = "symlink"
		} else if infoErr == nil {
			size = info.Size()
		}
		items = append(items, map[string]any{"name": entry.Name(), "path": joinDisplayPath(relative, entry.Name()), "type": kind, "size": size})
	}
	return jsonResult(map[string]any{"ok": true, "path": relative, "items": items, "truncated": len(entries) > limit})
}

func (workspace *fileWorkspace) find(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Limit   int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("find 参数错误: %w", err)
	}
	input.Pattern = strings.TrimSpace(input.Pattern)
	if input.Pattern == "" {
		return "", errors.New("find pattern 不能为空")
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = "."
	}
	absolute, _, err := workspace.resolveExisting(input.Path)
	if err != nil {
		return "", err
	}
	limit := bounded(input.Limit, 200, maxFindResults)
	results := make([]string, 0, min(limit, 64))
	truncated := false
	err = filepath.WalkDir(absolute, func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() && path != absolute && skipDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		relative, relErr := filepath.Rel(workspace.root, path)
		if relErr != nil || !matchesFilePattern(filepath.ToSlash(relative), input.Pattern) {
			return nil
		}
		if len(results) >= limit {
			truncated = true
			return fs.SkipAll
		}
		results = append(results, filepath.ToSlash(relative))
		return nil
	})
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return "", err
	}
	sort.Strings(results)
	return jsonResult(map[string]any{"ok": true, "files": results, "truncated": truncated})
}

func (workspace *fileWorkspace) grep(ctx context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Query         string `json:"query"`
		Path          string `json:"path"`
		Glob          string `json:"glob"`
		Regex         bool   `json:"regex"`
		CaseSensitive bool   `json:"case_sensitive"`
		Limit         int    `json:"limit"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("grep 参数错误: %w", err)
	}
	if input.Query == "" {
		return "", errors.New("grep query 不能为空")
	}
	if strings.TrimSpace(input.Path) == "" {
		input.Path = "."
	}
	pattern := input.Query
	if !input.Regex {
		pattern = regexp.QuoteMeta(pattern)
	}
	if !input.CaseSensitive {
		pattern = "(?i)" + pattern
	}
	matcher, err := regexp.Compile(pattern)
	if err != nil {
		return "", fmt.Errorf("grep 正则表达式无效: %w", err)
	}
	absolute, _, err := workspace.resolveExisting(input.Path)
	if err != nil {
		return "", err
	}
	limit := bounded(input.Limit, 100, maxSearchResults)
	matches := make([]map[string]any, 0, min(limit, 32))
	truncated := false
	walk := func(path string, entry fs.DirEntry, walkErr error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if walkErr != nil {
			return nil
		}
		if entry.IsDir() && path != absolute && skipDirectory(entry.Name()) {
			return filepath.SkipDir
		}
		if entry.IsDir() {
			return nil
		}
		// WalkDir 不会进入符号链接目录，但普通文件链接会被 read 跟随；
		// 先解析真实目标并校验工作区边界，避免 grep 读取工作区外文件。
		if entry.Type()&os.ModeSymlink != 0 {
			resolved, resolveErr := filepath.EvalSymlinks(path)
			if resolveErr != nil {
				return nil
			}
			if _, relativeErr := workspace.relative(resolved); relativeErr != nil {
				return nil
			}
		}
		relative, relErr := filepath.Rel(workspace.root, path)
		if relErr != nil || (input.Glob != "" && !matchesFilePattern(filepath.ToSlash(relative), input.Glob)) {
			return nil
		}
		content, readErr := readSearchFile(path)
		if readErr != nil {
			return nil
		}
		for index, line := range splitLines(string(content)) {
			if !matcher.MatchString(line) {
				continue
			}
			if len(matches) >= limit {
				truncated = true
				return fs.SkipAll
			}
			line, _ = truncateUTF8(line, 1000)
			matches = append(matches, map[string]any{"path": filepath.ToSlash(relative), "line": index + 1, "text": line})
		}
		return nil
	}
	info, err := os.Stat(absolute)
	if err != nil {
		return "", err
	}
	if info.Mode().IsRegular() {
		err = walk(absolute, fileEntry{info}, nil)
	} else {
		err = filepath.WalkDir(absolute, walk)
	}
	if err != nil && !errors.Is(err, fs.SkipAll) {
		return "", err
	}
	return jsonResult(map[string]any{"ok": true, "matches": matches, "truncated": truncated})
}

func (workspace *fileWorkspace) edit(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Path       string `json:"path"`
		OldText    string `json:"old_text"`
		NewText    string `json:"new_text"`
		ReplaceAll bool   `json:"replace_all"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("edit 参数错误: %w", err)
	}
	if input.OldText == "" {
		return "", errors.New("edit old_text 不能为空")
	}
	absolute, relative, err := workspace.resolveExisting(input.Path)
	if err != nil {
		return "", err
	}
	content, err := readTextFile(absolute)
	if err != nil {
		return "", err
	}
	count := strings.Count(string(content), input.OldText)
	if count == 0 {
		return "", errors.New("edit 没有找到 old_text；请先调用 read 获取当前内容")
	}
	if !input.ReplaceAll && count != 1 {
		return "", fmt.Errorf("edit old_text 出现 %d 次；请提供更完整的唯一片段，或明确设置 replace_all=true", count)
	}
	replacements := count
	updated := strings.ReplaceAll(string(content), input.OldText, input.NewText)
	if !input.ReplaceAll {
		replacements = 1
		updated = strings.Replace(string(content), input.OldText, input.NewText, 1)
	}
	if err := atomicWrite(absolute, []byte(updated)); err != nil {
		return "", err
	}
	workspace.rememberRead(absolute, []byte(updated))
	line := 1 + strings.Count(string(content)[:strings.Index(string(content), input.OldText)], "\n")
	return jsonResult(map[string]any{
		"ok": true, "path": relative, "replacements": replacements,
		"preview": changePreview(line, input.OldText, input.NewText),
	})
}

func (workspace *fileWorkspace) write(_ context.Context, raw json.RawMessage) (string, error) {
	var input struct {
		Path      string `json:"path"`
		Content   string `json:"content"`
		Overwrite bool   `json:"overwrite"`
	}
	if err := json.Unmarshal(raw, &input); err != nil {
		return "", fmt.Errorf("write 参数错误: %w", err)
	}
	if len(input.Content) > maxFileBytes {
		return "", fmt.Errorf("write 内容超过 %d MiB", maxFileBytes/(1024*1024))
	}
	if !utf8.ValidString(input.Content) {
		return "", errors.New("write 只支持 UTF-8 文本")
	}
	absolute, relative, exists, err := workspace.resolveForWrite(input.Path)
	if err != nil {
		return "", err
	}
	if exists {
		if !input.Overwrite {
			return "", errors.New("write 目标文件已存在；小范围修改请使用 edit，完整覆盖需设置 overwrite=true")
		}
		current, readErr := readTextFile(absolute)
		if readErr != nil {
			return "", readErr
		}
		if !workspace.wasRead(absolute, current) {
			return "", errors.New("write 覆盖现有文件前必须先调用 read；若文件已变化，请重新读取")
		}
	}
	if err := atomicWrite(absolute, []byte(input.Content)); err != nil {
		return "", err
	}
	workspace.rememberRead(absolute, []byte(input.Content))
	return jsonResult(map[string]any{"ok": true, "path": relative, "created": !exists, "bytes": len(input.Content)})
}

func (workspace *fileWorkspace) resolveExisting(input string) (string, string, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", errors.New("文件路径不能为空")
	}
	absolute := input
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(workspace.root, absolute)
	}
	absolute = filepath.Clean(absolute)
	resolved, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", "", fmt.Errorf("路径不可用: %w", err)
	}
	relative, err := workspace.relative(resolved)
	if err != nil {
		return "", "", err
	}
	return resolved, relative, nil
}

func (workspace *fileWorkspace) resolveForWrite(input string) (string, string, bool, error) {
	input = strings.TrimSpace(input)
	if input == "" {
		return "", "", false, errors.New("文件路径不能为空")
	}
	absolute := input
	if !filepath.IsAbs(absolute) {
		absolute = filepath.Join(workspace.root, absolute)
	}
	absolute = filepath.Clean(absolute)
	if info, err := os.Stat(absolute); err == nil {
		if !info.Mode().IsRegular() {
			return "", "", false, errors.New("write 目标不是普通文件")
		}
		resolved, resolveErr := filepath.EvalSymlinks(absolute)
		if resolveErr != nil {
			return "", "", false, resolveErr
		}
		relative, relativeErr := workspace.relative(resolved)
		return resolved, relative, true, relativeErr
	} else if !errors.Is(err, os.ErrNotExist) {
		return "", "", false, err
	}
	parent, err := filepath.EvalSymlinks(filepath.Dir(absolute))
	if err != nil {
		return "", "", false, fmt.Errorf("父目录不可用: %w", err)
	}
	absolute = filepath.Join(parent, filepath.Base(absolute))
	relative, err := workspace.relative(absolute)
	return absolute, relative, false, err
}

func (workspace *fileWorkspace) relative(absolute string) (string, error) {
	relative, err := filepath.Rel(workspace.root, absolute)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("路径超出 EasyAgent 工作区")
	}
	return filepath.ToSlash(relative), nil
}

func (workspace *fileWorkspace) rememberRead(path string, content []byte) {
	workspace.mu.Lock()
	defer workspace.mu.Unlock()
	workspace.reads[path] = sha256.Sum256(content)
}

func (workspace *fileWorkspace) wasRead(path string, content []byte) bool {
	workspace.mu.Lock()
	defer workspace.mu.Unlock()
	value, ok := workspace.reads[path]
	return ok && value == sha256.Sum256(content)
}

func readTextFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("目标不是普通文件")
	}
	if info.Size() > maxFileBytes {
		return nil, fmt.Errorf("文件超过 %d MiB；请使用 grep 定位后再读取更小文件", maxFileBytes/(1024*1024))
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if bytes.IndexByte(content, 0) >= 0 || !utf8.Valid(content) {
		return nil, errors.New("只支持 UTF-8 文本文件")
	}
	return content, nil
}

func readSearchFile(path string) ([]byte, error) {
	info, err := os.Stat(path)
	if err != nil || !info.Mode().IsRegular() || info.Size() > maxFileBytes {
		return nil, errors.New("跳过不可搜索文件")
	}
	return readTextFile(path)
}

func atomicWrite(path string, content []byte) error {
	info, err := os.Stat(path)
	mode := fs.FileMode(0o644)
	if err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(filepath.Dir(path), ".easyagent-edit-*")
	if err != nil {
		return err
	}
	temporaryName := temporary.Name()
	defer os.Remove(temporaryName)
	if err := temporary.Chmod(mode); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(content); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := temporary.Close(); err != nil {
		return err
	}
	return os.Rename(temporaryName, path)
}

func splitLines(content string) []string {
	content = strings.TrimSuffix(content, "\n")
	if content == "" {
		return []string{}
	}
	return strings.Split(content, "\n")
}

func matchesFilePattern(path, pattern string) bool {
	path, pattern = filepath.ToSlash(path), filepath.ToSlash(strings.TrimSpace(pattern))
	if pattern == "" || pattern == "*" {
		return true
	}
	if !strings.ContainsAny(pattern, "*?") {
		return strings.Contains(strings.ToLower(path), strings.ToLower(pattern))
	}
	matcher, err := regexp.Compile(globPattern(pattern))
	if err != nil {
		return false
	}
	if matcher.MatchString(path) {
		return true
	}
	return !strings.Contains(pattern, "/") && matcher.MatchString(filepath.Base(path))
}

// globPattern 支持代码仓库最常用的 *、? 和 **。特别处理 **/，使
// src/**/*.go 同时匹配 src/a.go 与 src/pkg/a.go。
func globPattern(pattern string) string {
	var result strings.Builder
	result.WriteByte('^')
	for index := 0; index < len(pattern); index++ {
		switch pattern[index] {
		case '*':
			if index+1 < len(pattern) && pattern[index+1] == '*' {
				index++
				if index+1 < len(pattern) && pattern[index+1] == '/' {
					index++
					result.WriteString("(?:.*/)?")
				} else {
					result.WriteString(".*")
				}
			} else {
				result.WriteString("[^/]*")
			}
		case '?':
			result.WriteString("[^/]")
		default:
			result.WriteString(regexp.QuoteMeta(string(pattern[index])))
		}
	}
	result.WriteByte('$')
	return result.String()
}

func skipDirectory(name string) bool {
	switch name {
	case ".git", ".idea", ".vscode", "node_modules", "vendor", "dist", "build", "coverage", ".cache":
		return true
	default:
		return false
	}
}

func joinDisplayPath(parent, name string) string {
	if parent == "." {
		return filepath.ToSlash(name)
	}
	return filepath.ToSlash(filepath.Join(parent, name))
}

func changePreview(line int, before, after string) string {
	before, beforeTruncated := truncateUTF8(before, 4000)
	after, afterTruncated := truncateUTF8(after, 4000)
	marker := ""
	if beforeTruncated || afterTruncated {
		marker = "\n... 预览已截断 ..."
	}
	return fmt.Sprintf("@@ line %d @@\n- %s\n+ %s%s", line, before, after, marker)
}

func truncateUTF8(value string, limit int) (string, bool) {
	if len(value) <= limit {
		return value, false
	}
	value = value[:limit]
	for !utf8.ValidString(value) {
		value = value[:len(value)-1]
	}
	return value, true
}

func bounded(value, fallback, maximum int) int {
	if value <= 0 {
		return fallback
	}
	return min(value, maximum)
}

func jsonResult(value any) (string, error) {
	encoded, err := json.MarshalIndent(value, "", "  ")
	return string(encoded), err
}

// fileEntry 把 os.FileInfo 适配为 fs.DirEntry，供 grep 搜索单个文件复用同一路径。
type fileEntry struct{ os.FileInfo }

func (entry fileEntry) Type() fs.FileMode          { return entry.Mode().Type() }
func (entry fileEntry) Info() (os.FileInfo, error) { return entry.FileInfo, nil }
