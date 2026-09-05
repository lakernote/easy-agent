package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const weixinSettingsKey = "weixin_settings"

func (store *Store) GetWeixinSettings() (WeixinSettings, error) {
	value := WeixinSettings{}
	var data []byte
	err := store.db.QueryRow(`SELECT value_json FROM ea_settings WHERE key=?`, weixinSettingsKey).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return value, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return value, fmt.Errorf("解析微信远程设置: %w", err)
	}
	return value, nil
}

func (store *Store) SaveWeixinSettings(value WeixinSettings) (WeixinSettings, error) {
	data, err := json.Marshal(value)
	if err != nil {
		return WeixinSettings{}, err
	}
	_, err = store.db.Exec(`INSERT INTO ea_settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, weixinSettingsKey, data)
	return value, err
}

func (store *Store) SaveWeixinAccount(value WeixinAccount) error {
	if strings.TrimSpace(value.ID) == "" || strings.TrimSpace(value.UserID) == "" || strings.TrimSpace(value.Token) == "" {
		return errors.New("微信绑定信息不完整")
	}
	if strings.TrimSpace(value.Label) == "" {
		value.Label = "微信用户"
	}
	if value.CreatedAt.IsZero() {
		value.CreatedAt = time.Now()
	}
	if value.UpdatedAt.IsZero() {
		value.UpdatedAt = value.CreatedAt
	}
	_, err := store.db.Exec(`INSERT INTO ea_weixin_accounts(
		id,label,user_id,token,base_url,enabled,sync_buf,current_session_id,project_id,ignore_before,
		last_seen_at,last_message_at,last_sequence,pending_message_id,delivered_message_id,
		pending_context_token,created_at,updated_at
	) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)
	ON CONFLICT(user_id) DO UPDATE SET
		id=excluded.id,label=excluded.label,token=excluded.token,base_url=excluded.base_url,
		enabled=excluded.enabled,sync_buf='',project_id=excluded.project_id,ignore_before=excluded.ignore_before,
		last_seen_at='',last_message_at='',last_sequence=0,pending_message_id=0,
		delivered_message_id=0,pending_context_token='',updated_at=excluded.updated_at`,
		value.ID, value.Label, value.UserID, value.Token, value.BaseURL, boolInt(value.Enabled),
		value.SyncBuffer, value.CurrentSessionID, value.ProjectID, formatOptionalTime(value.IgnoreBefore),
		formatOptionalTime(value.LastSeenAt), formatOptionalTime(value.LastMessageAt), value.LastSequence,
		value.PendingMessageID, value.DeliveredMessageID, value.PendingContextToken,
		formatTime(value.CreatedAt), formatTime(value.UpdatedAt))
	return err
}

func (store *Store) ListWeixinAccounts() ([]WeixinAccount, error) {
	rows, err := store.db.Query(`SELECT id,label,user_id,token,base_url,enabled,sync_buf,current_session_id,project_id,
		ignore_before,last_seen_at,last_message_at,last_sequence,pending_message_id,
		delivered_message_id,pending_context_token,created_at,updated_at
		FROM ea_weixin_accounts ORDER BY created_at ASC`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	values := []WeixinAccount{}
	for rows.Next() {
		value, scanErr := scanWeixinAccount(rows)
		if scanErr != nil {
			return nil, scanErr
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

func (store *Store) GetWeixinAccount(id string) (WeixinAccount, error) {
	row := store.db.QueryRow(`SELECT id,label,user_id,token,base_url,enabled,sync_buf,current_session_id,project_id,
		ignore_before,last_seen_at,last_message_at,last_sequence,pending_message_id,
		delivered_message_id,pending_context_token,created_at,updated_at
		FROM ea_weixin_accounts WHERE id=?`, id)
	return scanWeixinAccount(row)
}

func (store *Store) UpdateWeixinAccount(id, label string, enabled bool, projectID string, now time.Time) (WeixinAccount, error) {
	label = strings.TrimSpace(label)
	if label == "" {
		return WeixinAccount{}, errors.New("绑定备注不能为空")
	}
	enabledValue := boolInt(enabled)
	result, err := store.db.Exec(`UPDATE ea_weixin_accounts SET label=?,enabled=?,project_id=?,
		ignore_before=CASE WHEN enabled=0 AND ?=1 THEN ? ELSE ignore_before END,
		delivered_message_id=CASE WHEN ?=0 THEN pending_message_id ELSE delivered_message_id END,
		pending_context_token=CASE WHEN ?=0 THEN '' ELSE pending_context_token END,updated_at=? WHERE id=?`,
		label, enabledValue, projectID, enabledValue, formatTime(now), enabledValue, enabledValue, formatTime(now), id)
	if err != nil {
		return WeixinAccount{}, err
	}
	if affected, _ := result.RowsAffected(); affected == 0 {
		return WeixinAccount{}, sql.ErrNoRows
	}
	return store.GetWeixinAccount(id)
}

func (store *Store) SetWeixinCurrentSession(id, sessionID string, now time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_weixin_accounts SET current_session_id=?,updated_at=? WHERE id=?`, sessionID, formatTime(now), id)
	return err
}

func (store *Store) SetWeixinProject(id, projectID string, now time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_weixin_accounts SET project_id=?,updated_at=? WHERE id=?`, projectID, formatTime(now), id)
	return err
}

func (store *Store) SuppressPendingWeixinDeliveries(now time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_weixin_accounts SET delivered_message_id=pending_message_id,
		pending_context_token='',updated_at=?`, formatTime(now))
	return err
}

func (store *Store) DeleteWeixinAccount(id string) error {
	_, err := store.db.Exec(`DELETE FROM ea_weixin_accounts WHERE id=?`, id)
	return err
}

func (store *Store) UpdateWeixinCursor(id, buffer string, sequence int64, seenAt time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_weixin_accounts SET sync_buf=?,last_sequence=?,last_seen_at=?,updated_at=? WHERE id=?`,
		buffer, sequence, formatTime(seenAt), formatTime(seenAt), id)
	return err
}

func (store *Store) SetWeixinTask(id, sessionID string, messageID int64, contextToken string, messageAt, now time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_weixin_accounts SET current_session_id=?,pending_message_id=?,
		pending_context_token=?,last_message_at=?,updated_at=? WHERE id=?`, sessionID, messageID,
		contextToken, formatOptionalTime(messageAt), formatTime(now), id)
	return err
}

func (store *Store) MarkWeixinDelivered(id string, messageID int64, now time.Time) error {
	_, err := store.db.Exec(`UPDATE ea_weixin_accounts SET delivered_message_id=?,pending_context_token='',updated_at=? WHERE id=?`,
		messageID, formatTime(now), id)
	return err
}

func scanWeixinAccount(scanner interface{ Scan(...any) error }) (WeixinAccount, error) {
	var value WeixinAccount
	var enabled int
	var ignoreBefore, lastSeenAt, lastMessageAt, createdAt, updatedAt string
	err := scanner.Scan(&value.ID, &value.Label, &value.UserID, &value.Token, &value.BaseURL, &enabled,
		&value.SyncBuffer, &value.CurrentSessionID, &value.ProjectID, &ignoreBefore, &lastSeenAt, &lastMessageAt,
		&value.LastSequence, &value.PendingMessageID, &value.DeliveredMessageID,
		&value.PendingContextToken, &createdAt, &updatedAt)
	if err != nil {
		return WeixinAccount{}, err
	}
	value.Enabled = enabled != 0
	value.IgnoreBefore = parseOptionalTime(ignoreBefore)
	value.LastSeenAt = parseOptionalTime(lastSeenAt)
	value.LastMessageAt = parseOptionalTime(lastMessageAt)
	value.CreatedAt = parseOptionalTime(createdAt)
	value.UpdatedAt = parseOptionalTime(updatedAt)
	return value, nil
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatTime(value)
}

func parseOptionalTime(value string) time.Time {
	parsed, _ := time.Parse(time.RFC3339Nano, value)
	return parsed
}

func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}
