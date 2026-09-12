package store

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

const (
	ToolOperationExecuting = "executing"
	ToolOperationSucceeded = "succeeded"
	ToolOperationFailed    = "failed"
	ToolOperationUnknown   = "unknown"

	ToolGuaranteePreEffect    = "pre_effect"
	ToolGuaranteeObserved     = "observed"
	ToolGuaranteeUncorrelated = "uncorrelated"
)

// ToolOperation 是独立于 Trace 的副作用账本。Trace 用于展示，账本用于在
// 进程崩溃后判断一次工具调用是否可能已经产生了外部效果。
type ToolOperation struct {
	ID             int64
	SessionID      string
	Runtime        string
	Turn           int
	Step           int
	ActivityID     string
	Name           string
	ActivityKind   string
	ActivitySource string
	DisplayName    string
	Input          string
	Output         string
	Status         string
	Guarantee      string
	Error          string
	StartedAt      time.Time
	CompletedAt    time.Time
	UpdatedAt      time.Time
}

func normalizeToolGuarantee(value ToolOperation) string {
	switch value.Guarantee {
	case ToolGuaranteePreEffect, ToolGuaranteeObserved, ToolGuaranteeUncorrelated:
		return value.Guarantee
	}
	if value.Runtime == RuntimeEasyAgent {
		return ToolGuaranteePreEffect
	}
	return ToolGuaranteeObserved
}

func validateToolOperationIdentity(sessionID string, value ToolOperation) error {
	if strings.TrimSpace(sessionID) == "" {
		return errors.New("工具操作缺少 session id")
	}
	if strings.TrimSpace(value.Runtime) == "" || strings.TrimSpace(value.ActivityID) == "" || strings.TrimSpace(value.Name) == "" {
		return errors.New("工具操作缺少 runtime、activity id 或名称")
	}
	return nil
}

// BeginToolOperation 必须在 EasyAgent 真正调用工具前成功。重复 activity id
// 会直接失败，既不覆盖旧结论，也不允许第二次副作用绕过一一对应的账本。
func (store *Store) BeginToolOperation(sessionID string, value ToolOperation) error {
	if err := validateToolOperationIdentity(sessionID, value); err != nil {
		return err
	}
	if value.StartedAt.IsZero() {
		value.StartedAt = time.Now()
	}
	value.Guarantee = normalizeToolGuarantee(value)
	result, err := store.db.Exec(`INSERT OR IGNORE INTO ea_tool_operations(
session_id,runtime,turn,step,activity_id,name,activity_kind,activity_source,display_name,input,output,status,guarantee,error,started_at,completed_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,'executing',?,'',?,'',?)`,
		sessionID, value.Runtime, value.Turn, value.Step, value.ActivityID, value.Name,
		value.ActivityKind, value.ActivitySource, value.DisplayName, value.Input, value.Output,
		value.Guarantee, formatTime(value.StartedAt), formatTime(value.StartedAt))
	if err != nil {
		return err
	}
	inserted, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if inserted == 0 {
		return fmt.Errorf("工具操作 %s/%d/%s 已存在，不会重复执行", value.Runtime, value.Turn, value.ActivityID)
	}
	return nil
}

// SettleToolOperation 只接受确定的终态。若 start 通知缺失（例如 Codex
// app-server 版本差异），仍会补建一条完整记录；unknown 一旦形成不会被覆盖。
func (store *Store) SettleToolOperation(sessionID string, value ToolOperation) error {
	if err := validateToolOperationIdentity(sessionID, value); err != nil {
		return err
	}
	if value.Status != ToolOperationSucceeded && value.Status != ToolOperationFailed {
		return fmt.Errorf("非法工具操作终态 %q", value.Status)
	}
	if value.CompletedAt.IsZero() {
		value.CompletedAt = time.Now()
	}
	if value.StartedAt.IsZero() {
		value.StartedAt = value.CompletedAt
	}
	value.Guarantee = normalizeToolGuarantee(value)
	_, err := store.db.Exec(`INSERT INTO ea_tool_operations(
session_id,runtime,turn,step,activity_id,name,activity_kind,activity_source,display_name,input,output,status,guarantee,error,started_at,completed_at,updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(session_id,runtime,turn,activity_id) DO UPDATE SET
step=excluded.step,name=excluded.name,activity_kind=excluded.activity_kind,activity_source=excluded.activity_source,
display_name=CASE WHEN ea_tool_operations.status='executing' THEN excluded.display_name ELSE ea_tool_operations.display_name END,
input=CASE WHEN ea_tool_operations.status='executing' AND excluded.input<>'' THEN excluded.input ELSE ea_tool_operations.input END,
output=CASE WHEN ea_tool_operations.status='executing' THEN excluded.output ELSE ea_tool_operations.output END,
status=CASE WHEN ea_tool_operations.status='executing' THEN excluded.status ELSE ea_tool_operations.status END,
guarantee=ea_tool_operations.guarantee,
error=CASE WHEN ea_tool_operations.status='executing' THEN excluded.error ELSE ea_tool_operations.error END,
completed_at=CASE WHEN ea_tool_operations.status='executing' THEN excluded.completed_at ELSE ea_tool_operations.completed_at END,
updated_at=CASE WHEN ea_tool_operations.status='executing' THEN excluded.updated_at ELSE ea_tool_operations.updated_at END`,
		sessionID, value.Runtime, value.Turn, value.Step, value.ActivityID, value.Name,
		value.ActivityKind, value.ActivitySource, value.DisplayName, value.Input, value.Output,
		value.Status, value.Guarantee, value.Error, formatTime(value.StartedAt), formatTime(value.CompletedAt), formatTime(value.CompletedAt))
	return err
}

func (store *Store) ListToolOperations(sessionID string) ([]ToolOperation, error) {
	rows, err := store.db.Query(`SELECT id,session_id,runtime,turn,step,activity_id,name,activity_kind,activity_source,display_name,input,output,status,guarantee,error,started_at,completed_at,updated_at
FROM ea_tool_operations WHERE session_id=? ORDER BY id`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []ToolOperation{}
	for rows.Next() {
		var value ToolOperation
		var startedAt, completedAt, updatedAt string
		if err := rows.Scan(&value.ID, &value.SessionID, &value.Runtime, &value.Turn, &value.Step, &value.ActivityID, &value.Name,
			&value.ActivityKind, &value.ActivitySource, &value.DisplayName, &value.Input, &value.Output, &value.Status, &value.Guarantee, &value.Error,
			&startedAt, &completedAt, &updatedAt); err != nil {
			return nil, err
		}
		value.StartedAt, _ = time.Parse(time.RFC3339Nano, startedAt)
		if completedAt != "" {
			value.CompletedAt, _ = time.Parse(time.RFC3339Nano, completedAt)
		}
		value.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
		result = append(result, value)
	}
	return result, rows.Err()
}

// MarkUnfinishedToolOperationsUnknown closes every still-executing operation in
// the selected turn. It is used both on a normal cancellation/error path and
// during process-start recovery, so neither Runtime leaves a misleading
// "executing" row or silently retries an operation with an unknown outcome.
func (store *Store) MarkUnfinishedToolOperationsUnknown(sessionID, runtime string, turn int, reason string, now time.Time) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := markUnfinishedToolOperationsUnknownTx(tx, sessionID, runtime, turn, reason, now); err != nil {
		return err
	}
	return tx.Commit()
}

func markUnfinishedToolOperationsUnknownTx(tx *sql.Tx, sessionID, runtime string, turn int, reason string, now time.Time) error {
	query := `SELECT id,session_id,runtime,turn,step,activity_id,name,activity_kind,activity_source,display_name,input
FROM ea_tool_operations WHERE status='executing'`
	args := []any{}
	if sessionID != "" {
		query += ` AND session_id=?`
		args = append(args, sessionID)
	}
	if runtime != "" {
		query += ` AND runtime=?`
		args = append(args, runtime)
	}
	if turn > 0 {
		query += ` AND turn=?`
		args = append(args, turn)
	}
	query += ` ORDER BY id`
	rows, err := tx.Query(query, args...)
	if err != nil {
		return err
	}
	unfinished := []ToolOperation{}
	for rows.Next() {
		var value ToolOperation
		if err := rows.Scan(&value.ID, &value.SessionID, &value.Runtime, &value.Turn, &value.Step, &value.ActivityID, &value.Name,
			&value.ActivityKind, &value.ActivitySource, &value.DisplayName, &value.Input); err != nil {
			rows.Close()
			return err
		}
		unfinished = append(unfinished, value)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	if strings.TrimSpace(reason) == "" {
		reason = "工具执行未返回确定结果；为避免重复副作用不会自动重放"
	}
	for _, operation := range unfinished {
		if _, err := tx.Exec(`UPDATE ea_tool_operations SET status='unknown',error=?,completed_at=?,updated_at=? WHERE id=? AND status='executing'`, reason, formatTime(now), formatTime(now), operation.ID); err != nil {
			return err
		}
		if err := appendEventTx(tx, operation.SessionID, Event{
			Kind: "tool_unknown", Turn: operation.Turn, Step: operation.Step,
			Name: operation.Name, ActivityID: operation.ActivityID, ActivityKind: operation.ActivityKind,
			ActivitySource: operation.ActivitySource, DisplayName: operation.DisplayName,
			Status: ToolOperationUnknown, Detail: reason, Input: operation.Input,
			Protocol: operation.Runtime, CreatedAt: now,
		}); err != nil {
			return err
		}
	}
	return nil
}
