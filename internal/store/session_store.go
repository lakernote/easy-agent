package store

import (
	"strings"
	"time"
)

func (store *Store) CreateSession(id, title, model, workspace string, now time.Time) (Session, error) {
	return store.CreateSessionWithProfile(id, title, RuntimeEasyAgent, "", model, workspace, now)
}

func (store *Store) CreateSessionWithRuntime(id, title, runtime, model, workspace string, now time.Time) (Session, error) {
	return store.CreateSessionWithProfile(id, title, runtime, "", model, workspace, now)
}

func (store *Store) CreateSessionWithProfile(id, title, runtime, profileID, model, workspace string, now time.Time) (Session, error) {
	if runtime != RuntimeCodex {
		runtime = RuntimeEasyAgent
	}
	_, err := store.db.Exec(`INSERT INTO ea_sessions(id,title,status,error,runtime,profile_id,model,workspace,source_workspace,worktree_branch,response_id,provider_key,created_at,updated_at) VALUES(?,?,'idle','',?,?,?,?,?,'','','',?,?)`, id, title, runtime, profileID, model, workspace, workspace, formatTime(now), formatTime(now))
	if err != nil {
		return Session{}, err
	}
	return store.LoadSession(id)
}

// ListSessionsBefore 按更新时间和 ID 游标读取更早的会话，供侧栏无限滚动使用。
func (store *Store) ListSessionsBefore(limit int, beforeUpdatedAt, beforeID string) ([]Session, bool, error) {
	return store.listSessionsPage(limit, beforeUpdatedAt, beforeID)
}

func (store *Store) listSessionsPage(limit int, beforeUpdatedAt, beforeID string) ([]Session, bool, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	query := `SELECT id,title,status,error,runtime,profile_id,model,workspace,source_workspace,worktree_branch,response_id,provider_key,input_tokens,output_tokens,cached_tokens,cache_write_tokens,total_tokens,model_duration_ms,tool_duration_ms,model_calls,tool_calls,created_at,updated_at FROM ea_sessions`
	args := []any{}
	if strings.TrimSpace(beforeUpdatedAt) != "" && strings.TrimSpace(beforeID) != "" {
		query += ` WHERE updated_at < ? OR (updated_at = ? AND id < ?)`
		args = append(args, beforeUpdatedAt, beforeUpdatedAt, beforeID)
	}
	query += ` ORDER BY updated_at DESC, id DESC LIMIT ?`
	args = append(args, limit+1)
	rows, err := store.db.Query(query, args...)
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	result := []Session{}
	for rows.Next() {
		value, err := scanSession(rows)
		if err != nil {
			return nil, false, err
		}
		value.Messages = []Message{}
		value.Events = []Event{}
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	hasMore := len(result) > limit
	if hasMore {
		result = result[:limit]
	}
	return result, hasMore, nil
}

type rowScanner interface{ Scan(...any) error }

func scanSession(row rowScanner) (Session, error) {
	var value Session
	var created, updated string
	err := row.Scan(&value.ID, &value.Title, &value.Status, &value.Error, &value.Runtime, &value.ProfileID, &value.Model, &value.Workspace, &value.SourceWorkspace, &value.WorktreeBranch, &value.ResponseID, &value.ProviderKey,
		&value.Usage.InputTokens, &value.Usage.OutputTokens, &value.Usage.CachedTokens, &value.Usage.CacheWriteTokens, &value.Usage.TotalTokens,
		&value.Usage.ModelDurationMS, &value.Usage.ToolDurationMS, &value.Usage.ModelCalls, &value.Usage.ToolCalls, &created, &updated)
	if err != nil {
		return Session{}, err
	}
	value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	value.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return value, nil
}

func (store *Store) LoadSession(id string) (Session, error) {
	return store.sessionWindowBefore(id, 0, 0, 0, 0)
}

// LoadSessionWindow 返回供页面使用的有界会话窗口。Agent Runtime 使用 RuntimeSession
// 读取检查点之后的活跃消息；HTTP 页面和轮询不应随着单个会话增长而反复传输全部历史。
func (store *Store) LoadSessionWindow(id string, messageLimit, eventLimit int) (Session, error) {
	return store.sessionWindowBefore(id, messageLimit, eventLimit, 0, 0)
}

func (store *Store) sessionWindowBefore(id string, messageLimit, eventLimit int, messageBefore, eventBefore int64) (Session, error) {
	row := store.db.QueryRow(`SELECT id,title,status,error,runtime,profile_id,model,workspace,source_workspace,worktree_branch,response_id,provider_key,input_tokens,output_tokens,cached_tokens,cache_write_tokens,total_tokens,model_duration_ms,tool_duration_ms,model_calls,tool_calls,created_at,updated_at FROM ea_sessions WHERE id=?`, id)
	value, err := scanSession(row)
	if err != nil {
		return Session{}, err
	}
	value.Messages, value.MessageCount, value.UserTurnCount, value.MessagesTruncated, value.MessagesHasMore, err = store.messagesWindow(id, messageLimit, messageBefore)
	if err != nil {
		return Session{}, err
	}
	value.Events, value.EventCount, value.EventsTruncated, value.EventsHasMore, err = store.eventsWindow(id, eventLimit, eventBefore)
	if err != nil {
		return Session{}, err
	}
	value.Compactions, err = store.compactions(id)
	if err != nil {
		return Session{}, err
	}
	for _, event := range value.Events {
		value.Usage.CacheReported = value.Usage.CacheReported || event.CacheReported
		if event.CacheReported && (event.Kind == "model_end" || event.Kind == "compaction_end" || event.Kind == "codex_usage") {
			value.Usage.CacheInputTokens += event.InputTokens
		}
	}
	return value, err
}
