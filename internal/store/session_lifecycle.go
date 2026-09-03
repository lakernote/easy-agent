package store

import (
	"errors"
	"time"
)

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
