package store

import (
	"errors"
	"time"
)

func (store *Store) QueueSession(id, model string, now time.Time) error {
	result, err := store.db.Exec(`UPDATE ea_sessions SET status='queued',error='',model=?,updated_at=? WHERE id=? AND status NOT IN ('queued','running','paused')`, model, formatTime(now), id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return errors.New("Agent 正在处理或已暂停上一条消息")
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
status=CASE WHEN status IN ('canceled','paused') THEN status ELSE 'failed' END,
error=CASE WHEN status IN ('canceled','paused') THEN error ELSE ? END,
input_tokens=input_tokens+?,output_tokens=output_tokens+?,cached_tokens=cached_tokens+?,cache_write_tokens=cache_write_tokens+?,total_tokens=total_tokens+?,
model_duration_ms=model_duration_ms+?,tool_duration_ms=tool_duration_ms+?,model_calls=model_calls+?,tool_calls=tool_calls+?,updated_at=? WHERE id=?`,
		runError.Error(), usage.InputTokens, usage.OutputTokens, usage.CachedTokens, usage.CacheWriteTokens, usage.TotalTokens,
		usage.ModelDurationMS, usage.ToolDurationMS, usage.ModelCalls, usage.ToolCalls, formatTime(now), id)
	return err
}

// PauseQueuedSession 只暂停尚未获得执行槽的任务，因此恢复时不会重复任何
// 已经执行过的模型请求、命令或文件写入。
func (store *Store) PauseQueuedSession(id string, now time.Time) (bool, error) {
	result, err := store.db.Exec(`UPDATE ea_sessions SET status='paused',error='',updated_at=? WHERE id=? AND status='queued'`, formatTime(now), id)
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	return changed > 0, nil
}

func (store *Store) ResumePausedSession(id string, now time.Time) (bool, error) {
	result, err := store.db.Exec(`UPDATE ea_sessions SET status='queued',error='',updated_at=? WHERE id=? AND status='paused'`, formatTime(now), id)
	if err != nil {
		return false, err
	}
	changed, _ := result.RowsAffected()
	return changed > 0, nil
}

// CancelSession 只取消仍在排队或运行的任务，返回是否真的改变了状态。
func (store *Store) CancelSession(id string, now time.Time) (bool, error) {
	result, err := store.db.Exec(`UPDATE ea_sessions SET status='canceled',error='用户已停止任务',updated_at=? WHERE id=? AND status IN ('queued','running','paused')`, formatTime(now), id)
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
	// running 任务可能已经执行过命令或写文件，进程消失后自动重跑会重复副作用；
	// queued 任务尚未开始，保留状态交给新进程恢复执行。
	_, err := store.db.Exec(`UPDATE ea_sessions SET status='failed',error='服务重启时任务正在运行，已标记为中断；可确认工作区状态后重新发送消息',updated_at=? WHERE status='running'`, formatTime(now))
	return err
}

func (store *Store) ListQueuedSessions() ([]Session, error) {
	rows, err := store.db.Query(`SELECT ` + sessionSelectColumns + ` FROM ea_sessions WHERE status='queued' ORDER BY updated_at,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []Session
	for rows.Next() {
		value, err := scanSession(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) SetSessionWorkspace(id, workspace, sourceWorkspace, branch, notice string) error {
	_, err := store.db.Exec(`UPDATE ea_sessions SET workspace=?,source_workspace=?,worktree_branch=?,workspace_notice=? WHERE id=?`, workspace, sourceWorkspace, branch, notice, id)
	return err
}

func (store *Store) SetSessionContinuation(id, responseID string) error {
	_, err := store.db.Exec(`UPDATE ea_sessions SET response_id=? WHERE id=?`, responseID, id)
	return err
}

// UpdateSessionMetadata 更新会话的管理信息，不改变 updated_at，避免仅重命名或
// 整理文件夹就把旧会话移动到“最近”顶部。
func (store *Store) UpdateSessionMetadata(id, title, projectID string) error {
	result, err := store.db.Exec(`UPDATE ea_sessions SET title=?,project_id=? WHERE id=?`, title, projectID, id)
	if err != nil {
		return err
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if changed == 0 {
		return errors.New("会话不存在")
	}
	return nil
}
