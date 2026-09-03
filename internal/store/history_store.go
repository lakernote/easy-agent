package store

import (
	"encoding/json"
	"strings"
	"time"
)

func (store *Store) messagesWindow(id string, limit int, before int64) ([]Message, int, int, bool, bool, error) {
	var count, userTurns int
	if err := store.db.QueryRow(`SELECT COUNT(*), COALESCE(SUM(CASE WHEN role='user' THEN 1 ELSE 0 END),0) FROM ea_messages WHERE session_id=?`, id).Scan(&count, &userTurns); err != nil {
		return nil, 0, 0, false, false, err
	}
	query := `SELECT id,role,content,tool_calls_json,tool_call_id,name,created_at FROM ea_messages WHERE session_id=? ORDER BY seq`
	args := []any{id}
	truncated := false
	hasMore := false
	if limit > 0 {
		query = `SELECT id,role,content,tool_calls_json,tool_call_id,name,created_at FROM ea_messages WHERE session_id=?`
		if before > 0 {
			query += ` AND id < ?`
			args = append(args, before)
		}
		query += ` ORDER BY seq DESC LIMIT ?`
		// 多取一条只用于判断是否还有更早记录，响应仍然严格保持固定窗口大小。
		args = append(args, limit+1)
	}
	rows, err := store.db.Query(query, args...)
	if err != nil {
		return nil, 0, 0, false, false, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		var value Message
		var data []byte
		var created string
		if err := rows.Scan(&value.ID, &value.Role, &value.Content, &data, &value.ToolCallID, &value.Name, &created); err != nil {
			return nil, 0, 0, false, false, err
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
		return nil, 0, 0, false, false, err
	}
	if err := rows.Close(); err != nil {
		return nil, 0, 0, false, false, err
	}
	if limit > 0 {
		hasMore = len(result) > limit
		if hasMore {
			result = result[:limit]
		}
		truncated = count > len(result)
		for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
			result[left], result[right] = result[right], result[left]
		}
	}
	messageIDs := make([]int64, 0, len(result))
	for _, message := range result {
		messageIDs = append(messageIDs, message.ID)
	}
	attachments, err := store.messageAttachmentsForIDs(id, messageIDs)
	if err != nil {
		return nil, 0, 0, false, false, err
	}
	for index := range result {
		result[index].Attachments = attachments[result[index].ID]
		if result[index].Attachments == nil {
			result[index].Attachments = []Attachment{}
		}
	}
	return result, count, userTurns, truncated, hasMore, nil
}

// ListMessagesBefore 读取指定消息之前的一页，不会加载会话的其他历史。
func (store *Store) ListMessagesBefore(id string, before int64, limit int) ([]Message, int, bool, error) {
	result, count, _, _, hasMore, err := store.messagesWindow(id, limit, before)
	return result, count, hasMore, err
}

func (store *Store) messageAttachmentsForIDs(sessionID string, messageIDs []int64) (map[int64][]Attachment, error) {
	query := `SELECT a.id,a.message_id,a.name,a.mime_type,a.kind,a.size,a.data
FROM ea_attachments a JOIN ea_messages m ON m.id=a.message_id
	WHERE m.session_id=?`
	args := []any{sessionID}
	if messageIDs != nil {
		if len(messageIDs) == 0 {
			return map[int64][]Attachment{}, nil
		}
		placeholders := make([]string, len(messageIDs))
		for index, id := range messageIDs {
			placeholders[index] = "?"
			args = append(args, id)
		}
		query += ` AND m.id IN (` + strings.Join(placeholders, ",") + `)`
	}
	query += ` ORDER BY a.rowid`
	rows, err := store.db.Query(query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[int64][]Attachment{}
	for rows.Next() {
		var messageID int64
		var value Attachment
		if err := rows.Scan(&value.ID, &messageID, &value.Name, &value.MIMEType, &value.Kind, &value.Size, &value.Data); err != nil {
			return nil, err
		}
		result[messageID] = append(result[messageID], value)
	}
	return result, rows.Err()
}

// Attachment 按 ID 读取一份附件数据，供本地页面预览或下载。
func (store *Store) Attachment(id string) (Attachment, error) {
	var value Attachment
	err := store.db.QueryRow(`SELECT id,name,mime_type,kind,size,data FROM ea_attachments WHERE id=?`, id).
		Scan(&value.ID, &value.Name, &value.MIMEType, &value.Kind, &value.Size, &value.Data)
	return value, err
}

func (store *Store) eventsWindow(id string, limit int, before int64) ([]Event, int, bool, bool, error) {
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ea_events WHERE session_id=?`, id).Scan(&count); err != nil {
		return nil, 0, false, false, err
	}
	query := `SELECT id,event_json,created_at FROM ea_events WHERE session_id=? ORDER BY seq`
	args := []any{id}
	truncated := false
	hasMore := false
	if limit > 0 {
		query = `SELECT id,event_json,created_at FROM ea_events WHERE session_id=?`
		if before > 0 {
			query += ` AND id < ?`
			args = append(args, before)
		}
		query += ` ORDER BY seq DESC LIMIT ?`
		args = append(args, limit+1)
	}
	rows, err := store.db.Query(query, args...)
	if err != nil {
		return nil, 0, false, false, err
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		var value Event
		var databaseID int64
		var data []byte
		var created string
		if err := rows.Scan(&databaseID, &data, &created); err != nil {
			return nil, 0, false, false, err
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, 0, false, false, err
		}
		// event_json 保存的是事件内容，行 ID 以数据库主键为准。
		value.ID = databaseID
		value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, value)
	}
	if err := rows.Err(); err != nil {
		return nil, 0, false, false, err
	}
	if limit > 0 {
		hasMore = len(result) > limit
		if hasMore {
			result = result[:limit]
		}
		truncated = count > len(result)
		for left, right := 0, len(result)-1; left < right; left, right = left+1, right-1 {
			result[left], result[right] = result[right], result[left]
		}
	}
	return result, count, truncated, hasMore, nil
}

// ListEventsBefore 读取指定 Trace 事件之前的一页，不会加载会话的其他历史。
func (store *Store) ListEventsBefore(id string, before int64, limit int) ([]Event, int, bool, error) {
	result, count, _, hasMore, err := store.eventsWindow(id, limit, before)
	return result, count, hasMore, err
}

func (store *Store) AppendMessage(id string, value Message) error {
	return store.AppendMessages(id, []Message{value})
}

// AppendMessages 在一个事务中保存一组属于同一 Agent step 的消息。特别是
// assistant tool call 和对应的 tool result 必须一起提交，避免进程在两次
// AppendMessage 之间退出后留下 Provider 无法接受的不完整工具链。
func (store *Store) AppendMessages(id string, values []Message) error {
	if len(values) == 0 {
		return nil
	}
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, value := range values {
		data, err := encode(value.ToolCalls)
		if err != nil {
			return err
		}
		if value.CreatedAt.IsZero() {
			value.CreatedAt = time.Now()
		}
		inserted, err := tx.Exec(`INSERT INTO ea_messages(session_id,seq,role,content,tool_calls_json,tool_call_id,name,created_at)
VALUES(?,COALESCE((SELECT MAX(seq)+1 FROM ea_messages WHERE session_id=?),1),?,?,?,?,?,?)`, id, id, value.Role, value.Content, data, value.ToolCallID, value.Name, formatTime(value.CreatedAt))
		if err != nil {
			return err
		}
		messageID, err := inserted.LastInsertId()
		if err != nil {
			return err
		}
		for _, attachment := range value.Attachments {
			if _, err := tx.Exec(`INSERT INTO ea_attachments(id,message_id,name,mime_type,kind,size,data,created_at) VALUES(?,?,?,?,?,?,?,?)`,
				attachment.ID, messageID, attachment.Name, attachment.MIMEType, attachment.Kind, attachment.Size, attachment.Data, formatTime(value.CreatedAt)); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func (store *Store) AppendEvent(id string, value Event) error {
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	data, err := encode(value)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`INSERT INTO ea_events(session_id,seq,event_json,created_at)
VALUES(?,COALESCE((SELECT MAX(seq)+1 FROM ea_events WHERE session_id=?),1),?,?)`, id, id, data, formatTime(value.CreatedAt))
	return err
}

func (store *Store) compactions(id string) ([]Compaction, error) {
	rows, err := store.db.Query(`SELECT id,summary,through_message_id,split_turn,source_messages,compacted_messages,usage_json,created_at FROM ea_compactions WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Compaction{}
	for rows.Next() {
		var value Compaction
		var usage []byte
		var created string
		var splitTurn int
		if err := rows.Scan(&value.ID, &value.Summary, &value.ThroughMessageID, &splitTurn, &value.SourceMessages, &value.CompactedMessages, &usage, &created); err != nil {
			return nil, err
		}
		value.SplitTurn = splitTurn != 0
		if err := json.Unmarshal(usage, &value.Usage); err != nil {
			return nil, err
		}
		value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, value)
	}
	return result, rows.Err()
}

// AppendCompaction 保存一个新的检查点。原始消息不删除，便于审计和未来重建。
func (store *Store) AppendCompaction(id string, value Compaction) error {
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	usage, err := encode(value.Usage)
	if err != nil {
		return err
	}
	splitTurn := 0
	if value.SplitTurn {
		splitTurn = 1
	}
	_, err = store.db.Exec(`INSERT INTO ea_compactions(session_id,seq,summary,through_message_id,split_turn,source_messages,compacted_messages,usage_json,created_at)
	VALUES(?,COALESCE((SELECT MAX(seq)+1 FROM ea_compactions WHERE session_id=?),1),?,?,?,?,?,?,?)`,
		id, id, value.Summary, value.ThroughMessageID, splitTurn, value.SourceMessages, value.CompactedMessages, usage, formatTime(value.CreatedAt))
	return err
}

// QueueSession 先把任务标成排队中。真正获得单机执行槽后才会进入 running，
// 页面因此不会把等待本地模型的时间误算成模型运行时间。
