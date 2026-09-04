package store

import (
	"encoding/json"
	"time"
)

// RuntimeSession 只加载 Agent 运行真正需要的上下文：Session 元数据、最新检查点
// 以及检查点之后的消息尾部。它不会把页面 Trace 或已经被检查点覆盖的原始消息
// 读入内存；完整原文仍可通过 Session 和历史分页接口读取。
func (store *Store) RuntimeSession(id string) (Session, error) {
	row := store.db.QueryRow(`SELECT id,title,status,error,runtime,profile_id,model,workspace,source_workspace,worktree_branch,workspace_notice,response_id,provider_key,input_tokens,output_tokens,cached_tokens,cache_write_tokens,total_tokens,model_duration_ms,tool_duration_ms,model_calls,tool_calls,created_at,updated_at FROM ea_sessions WHERE id=?`, id)
	value, err := scanSession(row)
	if err != nil {
		return Session{}, err
	}
	value.Compactions, err = store.compactions(id)
	if err != nil {
		return Session{}, err
	}
	var throughMessageID int64
	if len(value.Compactions) > 0 {
		throughMessageID = value.Compactions[len(value.Compactions)-1].ThroughMessageID
	}
	value.Messages, value.MessageCount, value.UserTurnCount, err = store.messagesAfter(id, throughMessageID)
	if err != nil {
		return Session{}, err
	}
	// Trace 只用于页面审计和上下文账本，不参与 Agent 请求组装。
	value.Events = []Event{}
	return value, nil
}

func (store *Store) messagesAfter(id string, afterID int64) ([]Message, int, int, error) {
	var count, userTurns int
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN role='user' THEN 1 ELSE 0 END),0) FROM ea_messages WHERE session_id=?`, id).Scan(&count, &userTurns); err != nil {
		return nil, 0, 0, err
	}
	rows, err := store.db.Query(`SELECT id,role,content,tool_calls_json,tool_call_id,name,created_at FROM ea_messages WHERE session_id=? AND id>? ORDER BY seq`, id, afterID)
	if err != nil {
		return nil, 0, 0, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		var value Message
		var data []byte
		var created string
		if err := rows.Scan(&value.ID, &value.Role, &value.Content, &data, &value.ToolCallID, &value.Name, &created); err != nil {
			return nil, 0, 0, err
		}
		_ = json.Unmarshal(data, &value.ToolCalls)
		if value.ToolCalls == nil {
			value.ToolCalls = []ToolCall{}
		}
		value.Attachments = []Attachment{}
		value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, 0, err
	}
	messageIDs := make([]int64, 0, len(result))
	for _, message := range result {
		messageIDs = append(messageIDs, message.ID)
	}
	attachments, err := store.messageAttachmentsForIDs(id, messageIDs)
	if err != nil {
		return nil, 0, 0, err
	}
	for index := range result {
		result[index].Attachments = attachments[result[index].ID]
		if result[index].Attachments == nil {
			result[index].Attachments = []Attachment{}
		}
	}
	return result, count, userTurns, nil
}
