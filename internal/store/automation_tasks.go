package store

import (
	"database/sql"
	"time"
)

const automationTaskSelectColumns = `id,name,prompt,project_id,workspace,profile_id,trigger_type,interval_minutes,repeat,schedule_time,schedule_weekday,schedule_enabled,cron,enabled,next_run_at,last_run_at,last_status,last_error,last_session_id,created_at,updated_at`

func (store *Store) CreateAutomationTask(value AutomationTask) (AutomationTask, error) {
	_, err := store.db.Exec(`INSERT INTO ea_automation_tasks(id,name,prompt,project_id,workspace,profile_id,trigger_type,interval_minutes,repeat,schedule_time,schedule_weekday,schedule_enabled,cron,enabled,next_run_at,last_run_at,last_status,last_error,last_session_id,created_at,updated_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		value.ID, value.Name, value.Prompt, value.ProjectID, value.Workspace, value.ProfileID, value.TriggerType, value.IntervalMinutes, value.Repeat, value.ScheduleTime, value.ScheduleWeekday, boolInt(value.ScheduleEnabled), value.Cron, boolInt(value.Enabled), formatAutomationOptionalTime(value.NextRunAt), formatAutomationOptionalTime(value.LastRunAt), value.LastStatus, value.LastError, value.LastSessionID, formatTime(value.CreatedAt), formatTime(value.UpdatedAt))
	if err != nil {
		return AutomationTask{}, err
	}
	return store.GetAutomationTask(value.ID)
}

func (store *Store) UpdateAutomationTask(value AutomationTask) (AutomationTask, error) {
	result, err := store.db.Exec(`UPDATE ea_automation_tasks SET name=?,prompt=?,project_id=?,workspace=?,profile_id=?,trigger_type=?,interval_minutes=?,repeat=?,schedule_time=?,schedule_weekday=?,schedule_enabled=?,cron=?,enabled=?,next_run_at=?,updated_at=? WHERE id=?`,
		value.Name, value.Prompt, value.ProjectID, value.Workspace, value.ProfileID, value.TriggerType, value.IntervalMinutes, value.Repeat, value.ScheduleTime, value.ScheduleWeekday, boolInt(value.ScheduleEnabled), value.Cron, boolInt(value.Enabled), formatAutomationOptionalTime(value.NextRunAt), formatTime(value.UpdatedAt), value.ID)
	if err != nil {
		return AutomationTask{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return AutomationTask{}, sql.ErrNoRows
	}
	return store.GetAutomationTask(value.ID)
}

func (store *Store) GetAutomationTask(id string) (AutomationTask, error) {
	row := store.db.QueryRow(`SELECT `+automationTaskSelectColumns+` FROM ea_automation_tasks WHERE id=?`, id)
	return scanAutomationTask(row)
}

func (store *Store) ListAutomationTasks() ([]AutomationTask, error) {
	rows, err := store.db.Query(`SELECT ` + automationTaskSelectColumns + ` FROM ea_automation_tasks ORDER BY enabled DESC,name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AutomationTask{}
	for rows.Next() {
		value, err := scanAutomationTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) ListDueAutomationTasks(now time.Time) ([]AutomationTask, error) {
	rows, err := store.db.Query(`SELECT `+automationTaskSelectColumns+` FROM ea_automation_tasks WHERE enabled=1 AND schedule_enabled=1 AND next_run_at<>'' AND next_run_at<=? ORDER BY next_run_at,id`, formatTime(now))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []AutomationTask{}
	for rows.Next() {
		value, err := scanAutomationTask(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) MarkAutomationTaskRun(id string, runAt time.Time, nextRunAt *time.Time) (bool, error) {
	result, err := store.db.Exec(`UPDATE ea_automation_tasks SET last_run_at=?,last_status='queued',last_error='',next_run_at=?,updated_at=? WHERE id=? AND enabled=1`, formatTime(runAt), formatAutomationOptionalTime(nextRunAt), formatTime(runAt), id)
	if err != nil {
		return false, err
	}
	changed, err := result.RowsAffected()
	return changed > 0, err
}

func (store *Store) RecordAutomationTaskResult(id, status, errorMessage, sessionID string, updatedAt time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_automation_tasks SET last_status=?,last_error=?,last_session_id=?,updated_at=? WHERE id=?`, status, errorMessage, sessionID, formatTime(updatedAt), id)
	return err
}

func (store *Store) DeleteAutomationTask(id string) error {
	result, err := store.db.Exec(`DELETE FROM ea_automation_tasks WHERE id=?`, id)
	if err != nil {
		return err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return sql.ErrNoRows
	}
	return nil
}

type automationTaskScanner interface{ Scan(...any) error }

func scanAutomationTask(row automationTaskScanner) (AutomationTask, error) {
	var value AutomationTask
	var enabled int
	var nextRunAt, lastRunAt, createdAt, updatedAt string
	var scheduleEnabled int
	if err := row.Scan(&value.ID, &value.Name, &value.Prompt, &value.ProjectID, &value.Workspace, &value.ProfileID, &value.TriggerType, &value.IntervalMinutes, &value.Repeat, &value.ScheduleTime, &value.ScheduleWeekday, &scheduleEnabled, &value.Cron, &enabled, &nextRunAt, &lastRunAt, &value.LastStatus, &value.LastError, &value.LastSessionID, &createdAt, &updatedAt); err != nil {
		return AutomationTask{}, err
	}
	if value.Repeat == "" {
		value.Repeat = "interval"
	}
	value.Enabled = enabled != 0
	value.ScheduleEnabled = scheduleEnabled != 0
	value.NextRunAt = parseAutomationOptionalTime(nextRunAt)
	value.LastRunAt = parseAutomationOptionalTime(lastRunAt)
	value.CreatedAt, _ = time.Parse(time.RFC3339Nano, createdAt)
	value.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updatedAt)
	return value, nil
}

func formatAutomationOptionalTime(value *time.Time) string {
	if value == nil || value.IsZero() {
		return ""
	}
	return formatTime(*value)
}

func parseAutomationOptionalTime(value string) *time.Time {
	if value == "" {
		return nil
	}
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return nil
	}
	return &parsed
}
