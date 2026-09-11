package server

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/lakernote/easy-agent/internal/store"
	"github.com/lakernote/easy-agent/internal/tracevalue"
)

const (
	codexSessionLogDefaultLimit = 300
	codexSessionLogMaxLimit     = 500
	codexSessionLogMaxLineBytes = 8 * 1024 * 1024
	codexSessionLogPayloadLimit = 1024 * 1024
)

type codexSessionLogPage struct {
	Available  bool                  `json:"available"`
	ThreadID   string                `json:"threadId,omitempty"`
	Path       string                `json:"path,omitempty"`
	Model      string                `json:"model,omitempty"`
	Source     string                `json:"source,omitempty"`
	Lines      []codexSessionLogLine `json:"lines"`
	TotalLines int                   `json:"totalLines"`
	TotalBytes int64                 `json:"totalBytes"`
	NextCursor int                   `json:"nextCursor,omitempty"`
	Message    string                `json:"message,omitempty"`
}

type codexSessionLogLine struct {
	Line      int    `json:"line"`
	Timestamp string `json:"timestamp,omitempty"`
	Type      string `json:"type"`
	Subtype   string `json:"subtype,omitempty"`
	Bytes     int    `json:"bytes"`
	Payload   string `json:"payload"`
	Truncated bool   `json:"truncated,omitempty"`
}

// getCodexSessionLog exposes the persisted Codex rollout that belongs to one
// EasyAgent session. The browser never supplies a filesystem path: it supplies
// an EasyAgent session ID, which is resolved to the saved Codex thread ID and
// then to the path reported by thread/read.
func (server *Server) getCodexSessionLog(response http.ResponseWriter, request *http.Request) {
	session, err := server.store.LoadSessionWindow(request.PathValue("id"), 1, 1)
	if errors.Is(err, sql.ErrNoRows) {
		writeError(response, http.StatusNotFound, "会话不存在")
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if session.Runtime != store.RuntimeCodex {
		writeError(response, http.StatusBadRequest, "只有 Codex Runtime 会话包含 Session JSONL")
		return
	}
	threadID := strings.TrimSpace(session.ResponseID)
	if threadID == "" {
		writeJSON(response, http.StatusOK, codexSessionLogPage{Available: false, Lines: []codexSessionLogLine{}, Message: "当前会话还没有 Codex Thread ID；可先查看 App Server 实时记录。"})
		return
	}
	cursor, limit, ok := codexSessionLogCursor(response, request)
	if !ok {
		return
	}
	path, metadata, err := server.resolveCodexSessionLog(request.Context(), session, threadID)
	if errors.Is(err, fs.ErrNotExist) {
		writeJSON(response, http.StatusOK, codexSessionLogPage{Available: false, ThreadID: threadID, Lines: []codexSessionLogLine{}, Message: "没有找到对应的 Codex Session JSONL；可继续查看 App Server 实时记录。"})
		return
	}
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	page, err := readCodexSessionLog(path, cursor, limit)
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	page.Available = true
	page.ThreadID = threadID
	page.Path = displayCodexSessionLogPath(path)
	page.Model = metadata.Model
	page.Source = metadata.Source
	writeJSON(response, http.StatusOK, page)
}

func codexSessionLogCursor(response http.ResponseWriter, request *http.Request) (int, int, bool) {
	cursor := 0
	if value := strings.TrimSpace(request.URL.Query().Get("cursor")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed < 0 {
			writeError(response, http.StatusBadRequest, "Session JSONL 游标必须是非负整数")
			return 0, 0, false
		}
		cursor = parsed
	}
	limit := codexSessionLogDefaultLimit
	if value := strings.TrimSpace(request.URL.Query().Get("limit")); value != "" {
		parsed, err := strconv.Atoi(value)
		if err != nil || parsed <= 0 || parsed > codexSessionLogMaxLimit {
			writeError(response, http.StatusBadRequest, fmt.Sprintf("Session JSONL 每页数量必须在 1 到 %d 之间", codexSessionLogMaxLimit))
			return 0, 0, false
		}
		limit = parsed
	}
	return cursor, limit, true
}

type codexThreadLogMetadata struct {
	Path   string
	Model  string
	Source string
}

func (server *Server) resolveCodexSessionLog(ctx context.Context, session store.Session, threadID string) (string, codexThreadLogMetadata, error) {
	metadata := codexThreadLogMetadata{}
	value, queryErr := server.codexQueryWithParams(ctx, session.Workspace, "thread/read", map[string]any{"threadId": threadID, "includeTurns": false})
	if queryErr == nil {
		metadata = codexThreadLogMetadataFrom(value)
		if metadata.Path != "" {
			path, err := validateCodexSessionLogPath(metadata.Path, threadID)
			if err == nil {
				return path, metadata, nil
			}
			if !errors.Is(err, fs.ErrNotExist) {
				return "", metadata, err
			}
		}
	}
	path, err := findCodexSessionLog(threadID)
	if err != nil {
		return "", metadata, err
	}
	metadata.Path = path
	return path, metadata, nil
}

func codexThreadLogMetadataFrom(value any) codexThreadLogMetadata {
	root, _ := value.(map[string]any)
	thread, _ := root["thread"].(map[string]any)
	if thread == nil {
		return codexThreadLogMetadata{}
	}
	return codexThreadLogMetadata{
		Path:   stringMapValue(thread, "path"),
		Model:  stringMapValue(thread, "model"),
		Source: stringMapValue(thread, "source"),
	}
}

func stringMapValue(value map[string]any, key string) string {
	result, _ := value[key].(string)
	return strings.TrimSpace(result)
}

func codexHomeDirectory() (string, error) {
	if value := strings.TrimSpace(os.Getenv("CODEX_HOME")); value != "" {
		if !filepath.IsAbs(value) {
			return "", errors.New("CODEX_HOME 必须是绝对路径")
		}
		return filepath.Clean(value), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("读取 Codex 用户目录: %w", err)
	}
	return filepath.Join(home, ".codex"), nil
}

func validateCodexSessionLogPath(value, threadID string) (string, error) {
	root, err := codexHomeDirectory()
	if err != nil {
		return "", err
	}
	resolvedRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(filepath.Clean(value))
	if err != nil {
		return "", err
	}
	relative, err := filepath.Rel(resolvedRoot, resolved)
	if err != nil || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return "", errors.New("Codex Session JSONL 路径不在 CODEX_HOME 中")
	}
	info, err := os.Stat(resolved)
	if err != nil {
		return "", err
	}
	if !info.Mode().IsRegular() || !strings.EqualFold(filepath.Ext(resolved), ".jsonl") {
		return "", errors.New("Codex Session JSONL 不是普通 JSONL 文件")
	}
	if err := verifyCodexSessionLogThread(resolved, threadID); err != nil {
		return "", err
	}
	return resolved, nil
}

func verifyCodexSessionLogThread(path, threadID string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), codexSessionLogMaxLineBytes)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return err
		}
		return errors.New("Codex Session JSONL 是空文件")
	}
	var first struct {
		Type    string `json:"type"`
		Payload struct {
			ID string `json:"id"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(scanner.Bytes(), &first); err != nil || first.Type != "session_meta" || first.Payload.ID != threadID {
		return errors.New("Codex Session JSONL 与当前会话不匹配")
	}
	return nil
}

func findCodexSessionLog(threadID string) (string, error) {
	if !validCodexThreadID(threadID) {
		return "", errors.New("Codex Thread ID 无效")
	}
	root, err := codexHomeDirectory()
	if err != nil {
		return "", err
	}
	for _, directory := range []string{filepath.Join(root, "sessions"), filepath.Join(root, "archived_sessions")} {
		var candidate string
		err := filepath.WalkDir(directory, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if errors.Is(walkErr, fs.ErrNotExist) {
					return fs.SkipDir
				}
				return walkErr
			}
			if entry.Type()&os.ModeSymlink != 0 {
				if entry.IsDir() {
					return fs.SkipDir
				}
				return nil
			}
			if entry.IsDir() {
				return nil
			}
			name := entry.Name()
			if name == threadID+".jsonl" || strings.HasSuffix(name, "-"+threadID+".jsonl") {
				candidate = path
				return fs.SkipAll
			}
			return nil
		})
		if err != nil && !errors.Is(err, fs.SkipAll) && !errors.Is(err, fs.ErrNotExist) {
			return "", err
		}
		if candidate != "" {
			return validateCodexSessionLogPath(candidate, threadID)
		}
	}
	return "", fs.ErrNotExist
}

func validCodexThreadID(value string) bool {
	if len(value) < 8 || len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') || (character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func readCodexSessionLog(path string, cursor, limit int) (codexSessionLogPage, error) {
	file, err := os.Open(path)
	if err != nil {
		return codexSessionLogPage{}, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return codexSessionLogPage{}, err
	}
	page := codexSessionLogPage{Lines: []codexSessionLogLine{}, TotalBytes: info.Size()}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), codexSessionLogMaxLineBytes)
	for scanner.Scan() {
		page.TotalLines++
		if page.TotalLines <= cursor || len(page.Lines) >= limit {
			continue
		}
		raw := append([]byte(nil), scanner.Bytes()...)
		page.Lines = append(page.Lines, parseCodexSessionLogLine(page.TotalLines, raw))
	}
	if err := scanner.Err(); err != nil {
		return codexSessionLogPage{}, fmt.Errorf("读取 Codex Session JSONL: %w", err)
	}
	if cursor+len(page.Lines) < page.TotalLines {
		page.NextCursor = cursor + len(page.Lines)
	}
	return page, nil
}

func parseCodexSessionLogLine(line int, raw []byte) codexSessionLogLine {
	result := codexSessionLogLine{Line: line, Type: "unknown", Bytes: len(raw), Truncated: len(raw) > codexSessionLogPayloadLimit}
	var envelope struct {
		Timestamp string          `json:"timestamp"`
		Type      string          `json:"type"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		result.Timestamp = envelope.Timestamp
		if strings.TrimSpace(envelope.Type) != "" {
			result.Type = envelope.Type
		}
		var payload map[string]any
		if json.Unmarshal(envelope.Payload, &payload) == nil {
			for _, key := range []string{"type", "role", "name"} {
				if value := stringMapValue(payload, key); value != "" {
					result.Subtype = value
					break
				}
			}
		}
	}
	result.Payload = tracevalue.Marshal(string(raw), codexSessionLogPayloadLimit)
	return result
}

func displayCodexSessionLogPath(path string) string {
	root, err := codexHomeDirectory()
	if err != nil {
		return path
	}
	relative, err := filepath.Rel(root, path)
	if err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return filepath.Join("$CODEX_HOME", relative)
	}
	return path
}
