package store

import "time"

// PurgeExpiredSessions removes terminal sessions older than the configured
// retention window. Messages, attachments, events and compaction checkpoints
// are foreign-key children of a session and are removed by SQLite cascade.
// Active work is intentionally excluded so cleanup can never interrupt a task.
func (store *Store) PurgeExpiredSessions(now time.Time, retentionDays int) (int64, error) {
	settings := normalizeRuntimeSettings(RuntimeSettings{RetentionDays: retentionDays})
	cutoff := now.AddDate(0, 0, -settings.RetentionDays)
	result, err := store.db.Exec(`DELETE FROM ea_sessions
WHERE updated_at < ? AND status NOT IN ('queued', 'running', 'paused')`, formatTime(cutoff))
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}
