package store

import "fmt"

func (store *Store) init() error {
	schema := `
PRAGMA foreign_keys = ON;
PRAGMA journal_mode = WAL;
CREATE TABLE IF NOT EXISTS ea_settings (
  key TEXT PRIMARY KEY,
  value_json BLOB NOT NULL
);
CREATE TABLE IF NOT EXISTS ea_auth_users (
  username TEXT PRIMARY KEY,
  password_salt BLOB NOT NULL,
  password_hash BLOB NOT NULL,
  password_iterations INTEGER NOT NULL,
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
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
  runtime TEXT NOT NULL DEFAULT 'easyagent',
  profile_id TEXT NOT NULL DEFAULT '',
  model TEXT NOT NULL,
  workspace TEXT NOT NULL DEFAULT '',
  source_workspace TEXT NOT NULL DEFAULT '',
  worktree_branch TEXT NOT NULL DEFAULT '',
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
CREATE TABLE IF NOT EXISTS ea_attachments (
  id TEXT PRIMARY KEY,
  message_id INTEGER NOT NULL REFERENCES ea_messages(id) ON DELETE CASCADE,
  name TEXT NOT NULL,
  mime_type TEXT NOT NULL,
  kind TEXT NOT NULL,
  size INTEGER NOT NULL,
  data BLOB NOT NULL,
  created_at TEXT NOT NULL
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
  split_turn INTEGER NOT NULL DEFAULT 0,
  source_messages INTEGER NOT NULL,
  compacted_messages INTEGER NOT NULL,
  usage_json BLOB NOT NULL,
  created_at TEXT NOT NULL,
  UNIQUE(session_id, seq)
);
CREATE TABLE IF NOT EXISTS ea_weixin_accounts (
  id TEXT PRIMARY KEY,
  label TEXT NOT NULL,
  user_id TEXT NOT NULL UNIQUE,
  token TEXT NOT NULL,
  base_url TEXT NOT NULL,
  enabled INTEGER NOT NULL DEFAULT 1,
  sync_buf TEXT NOT NULL DEFAULT '',
  current_session_id TEXT NOT NULL DEFAULT '',
  ignore_before TEXT NOT NULL,
  last_seen_at TEXT NOT NULL DEFAULT '',
  last_message_at TEXT NOT NULL DEFAULT '',
  last_sequence INTEGER NOT NULL DEFAULT 0,
  pending_message_id INTEGER NOT NULL DEFAULT 0,
  delivered_message_id INTEGER NOT NULL DEFAULT 0,
  pending_context_token TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL,
  updated_at TEXT NOT NULL
);
	CREATE INDEX IF NOT EXISTS idx_ea_sessions_updated ON ea_sessions(updated_at DESC);
	CREATE INDEX IF NOT EXISTS idx_ea_messages_session ON ea_messages(session_id, seq);
	CREATE INDEX IF NOT EXISTS idx_ea_messages_session_id ON ea_messages(session_id, id);
	CREATE INDEX IF NOT EXISTS idx_ea_attachments_message ON ea_attachments(message_id);
	CREATE INDEX IF NOT EXISTS idx_ea_events_session ON ea_events(session_id, seq);
	CREATE INDEX IF NOT EXISTS idx_ea_events_session_id ON ea_events(session_id, id);
CREATE INDEX IF NOT EXISTS idx_ea_events_created ON ea_events(created_at);
CREATE INDEX IF NOT EXISTS idx_ea_compactions_session ON ea_compactions(session_id, seq);
CREATE INDEX IF NOT EXISTS idx_ea_weixin_accounts_enabled ON ea_weixin_accounts(enabled, updated_at);
`
	_, err := store.db.Exec(schema)
	if err != nil {
		return fmt.Errorf("初始化 SQLite: %w", err)
	}
	// 旧版本数据库由 CREATE TABLE IF NOT EXISTS 不会自动补列；这里做一次
	// 幂等迁移，保证已有会话也能保存 split-turn 检查点。
	var splitTurnColumn int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ea_compactions') WHERE name='split_turn'`).Scan(&splitTurnColumn); err != nil {
		return err
	}
	if splitTurnColumn == 0 {
		if _, err := store.db.Exec(`ALTER TABLE ea_compactions ADD COLUMN split_turn INTEGER NOT NULL DEFAULT 0`); err != nil {
			return fmt.Errorf("迁移 ea_compactions.split_turn: %w", err)
		}
	}
	// 会话工作区是会话上下文的一部分。SQLite 的 IF NOT EXISTS 不会给已经存在的
	// 表补列，因此用幂等迁移保证升级后的现有数据库可以直接打开。
	var workspaceColumn int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ea_sessions') WHERE name='workspace'`).Scan(&workspaceColumn); err != nil {
		return err
	}
	if workspaceColumn == 0 {
		if _, err := store.db.Exec(`ALTER TABLE ea_sessions ADD COLUMN workspace TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("迁移 ea_sessions.workspace: %w", err)
		}
	}
	var runtimeColumn int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ea_sessions') WHERE name='runtime'`).Scan(&runtimeColumn); err != nil {
		return err
	}
	if runtimeColumn == 0 {
		if _, err := store.db.Exec(`ALTER TABLE ea_sessions ADD COLUMN runtime TEXT NOT NULL DEFAULT 'easyagent'`); err != nil {
			return fmt.Errorf("迁移 ea_sessions.runtime: %w", err)
		}
	}
	var profileColumn int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ea_sessions') WHERE name='profile_id'`).Scan(&profileColumn); err != nil {
		return err
	}
	if profileColumn == 0 {
		if _, err := store.db.Exec(`ALTER TABLE ea_sessions ADD COLUMN profile_id TEXT NOT NULL DEFAULT ''`); err != nil {
			return fmt.Errorf("迁移 ea_sessions.profile_id: %w", err)
		}
	}
	for _, column := range []struct {
		name       string
		definition string
	}{
		{name: "source_workspace", definition: "TEXT NOT NULL DEFAULT ''"},
		{name: "worktree_branch", definition: "TEXT NOT NULL DEFAULT ''"},
	} {
		var exists int
		if err := store.db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('ea_sessions') WHERE name=?`, column.name).Scan(&exists); err != nil {
			return err
		}
		if exists == 0 {
			if _, err := store.db.Exec(`ALTER TABLE ea_sessions ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
				return fmt.Errorf("迁移 ea_sessions.%s: %w", column.name, err)
			}
		}
	}
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ea_settings WHERE key='model'`).Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		if err := store.SaveModelSettings(DefaultModelSettings()); err != nil {
			return err
		}
	}
	return store.EnsureAdmin()
}
