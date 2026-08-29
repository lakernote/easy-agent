package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Store struct {
	db *sql.DB
}

func Open(path string) (*Store, error) {
	path = strings.TrimSpace(path)
	if path == "" {
		return nil, errors.New("SQLite 路径不能为空")
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	store := &Store{db: db}
	if err := store.init(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return store, nil
}

func (store *Store) Close() error { return store.db.Close() }

func (store *Store) init() error {
	schema := `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS ea_settings (
  key TEXT PRIMARY KEY,
  value_json BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS ea_skills (
  name TEXT PRIMARY KEY,
  description TEXT NOT NULL,
  content TEXT NOT NULL,
  enabled INTEGER NOT NULL,
  builtin INTEGER NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ea_mcp (
  id TEXT PRIMARY KEY,
  config_json BLOB NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ea_sessions (
  id TEXT PRIMARY KEY,
  title TEXT NOT NULL,
  status TEXT NOT NULL,
  error TEXT NOT NULL,
  model TEXT NOT NULL,
  response_id TEXT NOT NULL,
  provider_key TEXT NOT NULL,
  input_tokens INTEGER NOT NULL DEFAULT 0,
  output_tokens INTEGER NOT NULL DEFAULT 0,
  cached_tokens INTEGER NOT NULL DEFAULT 0,
  cache_write_tokens INTEGER NOT NULL DEFAULT 0,
  total_tokens INTEGER NOT NULL DEFAULT 0,
  model_duration_ms INTEGER NOT NULL DEFAULT 0,
  tool_duration_ms INTEGER NOT NULL DEFAULT 0,
  model_calls INTEGER NOT NULL DEFAULT 0,
  tool_calls INTEGER NOT NULL DEFAULT 0,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS ea_messages (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES ea_sessions(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  role TEXT NOT NULL,
  content TEXT NOT NULL,
  tool_calls_json BLOB NOT NULL,
  tool_call_id TEXT NOT NULL,
  name TEXT NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(session_id, seq)
);
CREATE TABLE IF NOT EXISTS ea_events (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES ea_sessions(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  event_json BLOB NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(session_id, seq)
);
CREATE TABLE IF NOT EXISTS ea_compactions (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  session_id TEXT NOT NULL REFERENCES ea_sessions(id) ON DELETE CASCADE,
  seq INTEGER NOT NULL,
  summary TEXT NOT NULL,
  through_message_id INTEGER NOT NULL,
  source_messages INTEGER NOT NULL,
  compacted_messages INTEGER NOT NULL,
  usage_json BLOB NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(session_id, seq)
);
CREATE INDEX IF NOT EXISTS idx_ea_sessions_updated ON ea_sessions(updated_at DESC);
CREATE INDEX IF NOT EXISTS idx_ea_messages_session ON ea_messages(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_ea_events_session ON ea_events(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_ea_compactions_session ON ea_compactions(session_id, seq);
`
	_, err := store.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("初始化 SQLite: %w", err)
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ea_settings WHERE key='model'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		return store.SaveModel(DefaultModelSettings())
	}
	return nil
}

func encode(value any) ([]byte, error) { return json.Marshal(value) }

func (store *Store) Model() (ModelSettings, error) {
	var data []byte
	if err := store.db.QueryRow(`SELECT value_json FROM ea_settings WHERE key='model'`).Scan(&data); err != nil {
		return ModelSettings{}, err
	}
	var result ModelSettings
	if err := json.Unmarshal(data, &result); err != nil {
		return ModelSettings{}, err
	}
	return result.WithDefaults(), nil
}

func (store *Store) SaveModel(value ModelSettings) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`INSERT INTO ea_settings(key,value_json) VALUES('model',?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, data)
	return err
}

func (store *Store) SkillOverrides() ([]SkillOverride, error) {
	rows, err := store.db.Query(`SELECT name,description,content,enabled,builtin FROM ea_skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SkillOverride
	for rows.Next() {
		var value SkillOverride
		if err := rows.Scan(&value.Name, &value.Description, &value.Content, &value.Enabled, &value.Builtin); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) SaveSkill(value SkillOverride) error {
	_, err := store.db.Exec(`INSERT INTO ea_skills(name,description,content,enabled,builtin,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET description=excluded.description,content=excluded.content,enabled=excluded.enabled,builtin=excluded.builtin,updated_at=excluded.updated_at`,
		value.Name, value.Description, value.Content, value.Enabled, value.Builtin, formatTime(time.Now()))
	return err
}

func (store *Store) DeleteSkill(name string) error {
	_, err := store.db.Exec(`DELETE FROM ea_skills WHERE name=?`, name)
	return err
}

func (store *Store) MCPs() ([]MCPConfig, error) {
	rows, err := store.db.Query(`SELECT config_json FROM ea_mcp ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MCPConfig{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var value MCPConfig
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) SaveMCP(value MCPConfig) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`INSERT INTO ea_mcp(id,config_json,updated_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET config_json=excluded.config_json,updated_at=excluded.updated_at`, value.ID, data, formatTime(time.Now()))
	return err
}

func (store *Store) DeleteMCP(id string) error {
	_, err := store.db.Exec(`DELETE FROM ea_mcp WHERE id=?`, id)
	return err
}

func (store *Store) CreateSession(id, title, model string, now time.Time) (Session, error) {
	_, err := store.db.Exec(`INSERT INTO ea_sessions(id,title,status,error,model,response_id,provider_key,created_at,updated_at) VALUES(?,?,'idle','',?,'','',?,?)`, id, title, model, formatTime(now), formatTime(now))
	if err != nil {
		return Session{}, err
	}
	return store.Session(id)
}

func (store *Store) ListSessions(limit int) ([]Session, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := store.db.Query(`SELECT id,title,status,error,model,response_id,provider_key,input_tokens,output_tokens,cached_tokens,cache_write_tokens,total_tokens,model_duration_ms,tool_duration_ms,model_calls,tool_calls,created_at,updated_at FROM ea_sessions ORDER BY updated_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Session{}
	for rows.Next() {
		value, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		value.Messages = []Message{}
		value.Events = []Event{}
		result = append(result, value)
	}
	return result, rows.Err()
}

type rowScanner interface{ Scan(...any) error }

func scanSession(row rowScanner) (Session, error) {
	var value Session
	var created, updated string
	err := row.Scan(&value.ID, &value.Title, &value.Status, &value.Error, &value.Model, &value.ResponseID, &value.ProviderKey,
		&value.Usage.InputTokens, &value.Usage.OutputTokens, &value.Usage.CachedTokens, &value.Usage.CacheWriteTokens, &value.Usage.TotalTokens,
		&value.Usage.ModelDurationMS, &value.Usage.ToolDurationMS, &value.Usage.ModelCalls, &value.Usage.ToolCalls, &created, &updated)
	if err != nil {
		return Session{}, err
	}
	value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	value.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return value, nil
}

func (store *Store) Session(id string) (Session, error) {
	row := store.db.QueryRow(`SELECT id,title,status,error,model,response_id,provider_key,input_tokens,output_tokens,cached_tokens,cache_write_tokens,total_tokens,model_duration_ms,tool_duration_ms,model_calls,tool_calls,created_at,updated_at FROM ea_sessions WHERE id=?`, id)
	value, err := scanSession(row)
	if err != nil {
		return Session{}, err
	}
	value.Messages, err = store.messages(id)
	if err != nil {
		return Session{}, err
	}
	value.Events, err = store.events(id)
	if err != nil {
		return Session{}, err
	}
	value.Compactions, err = store.compactions(id)
	if err != nil {
		return Session{}, err
	}
	for _, event := range value.Events {
		value.Usage.CacheReported = value.Usage.CacheReported || event.CacheReported
		if event.CacheReported && (event.Kind == "model_end" || event.Kind == "compaction_end") {
			value.Usage.CacheInputTokens += event.InputTokens
		}
	}
	return value, err
}

func (store *Store) messages(id string) ([]Message, error) {
	rows, err := store.db.Query(`SELECT id,role,content,tool_calls_json,tool_call_id,name,created_at FROM ea_messages WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Message{}
	for rows.Next() {
		var value Message
		var data []byte
		var created string
		if err := rows.Scan(&value.ID, &value.Role, &value.Content, &data, &value.ToolCallID, &value.Name, &created); err != nil {
			return nil, err
		}
		_ = json.Unmarshal(data, &value.ToolCalls)
		if value.ToolCalls == nil {
			value.ToolCalls = []ToolCall{}
		}
		value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) events(id string) ([]Event, error) {
	rows, err := store.db.Query(`SELECT id,event_json,created_at FROM ea_events WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Event{}
	for rows.Next() {
		var value Event
		var databaseID int64
		var data []byte
		var created string
		if err := rows.Scan(&databaseID, &data, &created); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		// event_json 保存的是事件内容，行 ID 以数据库主键为准。
		value.ID = databaseID
		value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) AppendMessage(id string, value Message) error {
	data, err := encode(value.ToolCalls)
	if err != nil {
		return err
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	_, err = store.db.Exec(`INSERT INTO ea_messages(session_id,seq,role,content,tool_calls_json,tool_call_id,name,created_at)
VALUES(?,COALESCE((SELECT MAX(seq)+1 FROM ea_messages WHERE session_id=?),1),?,?,?,?,?,?)`, id, id, value.Role, value.Content, data, value.ToolCallID, value.Name, formatTime(value.CreatedAt))
	return err
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
	rows, err := store.db.Query(`SELECT id,summary,through_message_id,source_messages,compacted_messages,usage_json,created_at FROM ea_compactions WHERE session_id=? ORDER BY seq`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []Compaction{}
	for rows.Next() {
		var value Compaction
		var usage []byte
		var created string
		if err := rows.Scan(&value.ID, &value.Summary, &value.ThroughMessageID, &value.SourceMessages, &value.CompactedMessages, &usage, &created); err != nil {
			return nil, err
		}
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
	_, err = store.db.Exec(`INSERT INTO ea_compactions(session_id,seq,summary,through_message_id,source_messages,compacted_messages,usage_json,created_at)
VALUES(?,COALESCE((SELECT MAX(seq)+1 FROM ea_compactions WHERE session_id=?),1),?,?,?,?,?,?)`,
		id, id, value.Summary, value.ThroughMessageID, value.SourceMessages, value.CompactedMessages, usage, formatTime(value.CreatedAt))
	return err
}

// QueueSession 先把任务标成排队中。真正获得单机执行槽后才会进入 running，
// 页面因此不会把等待本地模型的时间误算成模型运行时间。
func (store *Store) QueueSession(id, model string, now time.Time) error {
	result, err := store.db.Exec(`UPDATE ea_sessions SET status='queued',error='',model=?,updated_at=? WHERE id=? AND status NOT IN ('queued','running')`, model, formatTime(now), id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return errors.New("Agent 正在处理上一条消息")
	}
	return nil
}

func (store *Store) MarkRunning(id string, now time.Time) error {
	result, err := store.db.Exec(`UPDATE ea_sessions SET status='running',updated_at=? WHERE id=? AND status='queued'`, formatTime(now), id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return errors.New("任务已不在排队状态")
	}
	return nil
}

func (store *Store) FinishSession(id, responseID, providerKey string, usage Usage, now time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_sessions SET status='idle',error='',response_id=?,provider_key=?,
input_tokens=input_tokens+?,output_tokens=output_tokens+?,cached_tokens=cached_tokens+?,cache_write_tokens=cache_write_tokens+?,total_tokens=total_tokens+?,
model_duration_ms=model_duration_ms+?,tool_duration_ms=tool_duration_ms+?,model_calls=model_calls+?,tool_calls=tool_calls+?,updated_at=? WHERE id=? AND status='running'`,
		responseID, providerKey, usage.InputTokens, usage.OutputTokens, usage.CachedTokens, usage.CacheWriteTokens, usage.TotalTokens,
		usage.ModelDurationMS, usage.ToolDurationMS, usage.ModelCalls, usage.ToolCalls, formatTime(now), id)
	return err
}

func (store *Store) FailSession(id string, runError error, usage Usage, now time.Time) error {
	// 即使用户主动取消，也要保留已经发生的模型/工具调用和耗时；但 canceled
	// 状态与用户可读错误不能被后台退出覆盖。
	_, err := store.db.Exec(`UPDATE ea_sessions SET
status=CASE WHEN status='canceled' THEN status ELSE 'failed' END,
error=CASE WHEN status='canceled' THEN error ELSE ? END,
input_tokens=input_tokens+?,output_tokens=output_tokens+?,cached_tokens=cached_tokens+?,cache_write_tokens=cache_write_tokens+?,total_tokens=total_tokens+?,
model_duration_ms=model_duration_ms+?,tool_duration_ms=tool_duration_ms+?,model_calls=model_calls+?,tool_calls=tool_calls+?,updated_at=? WHERE id=?`,
		runError.Error(), usage.InputTokens, usage.OutputTokens, usage.CachedTokens, usage.CacheWriteTokens, usage.TotalTokens,
		usage.ModelDurationMS, usage.ToolDurationMS, usage.ModelCalls, usage.ToolCalls, formatTime(now), id)
	return err
}

// CancelSession 只取消仍在排队或运行的任务，返回是否真的改变了状态。
func (store *Store) CancelSession(id string, now time.Time) (bool, error) {
	result, err := store.db.Exec(`UPDATE ea_sessions SET status='canceled',error='用户已停止任务',updated_at=? WHERE id=? AND status IN ('queued','running')`, formatTime(now), id)
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	return changed > 0, nil
}

func (store *Store) DeleteSession(id string) error {
	_, err := store.db.Exec(`DELETE FROM ea_sessions WHERE id=?`, id)
	return err
}

func (store *Store) RecoverRunning(now time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_sessions SET status='failed',error='服务重启时任务尚未完成，请重新发送消息',updated_at=? WHERE status IN ('queued','running')`, formatTime(now))
	return err
}

func (store *Store) Ping(ctx context.Context) error { return store.db.PingContext(ctx) }

func formatTime(value time.Time) string { return value.Format(time.RFC3339Nano) }
