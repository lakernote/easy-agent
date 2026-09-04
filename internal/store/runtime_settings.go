package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

const runtimeSettingsKey = "runtime_settings"

const (
	DefaultMaxConcurrentTasks  = 4
	MinMaxConcurrentTasks      = 1
	MaxMaxConcurrentTasks      = 16
	DefaultTurnTimeoutSeconds  = 12 * 60 * 60
	MinTurnTimeoutSeconds      = 5 * 60
	MaxTurnTimeoutSeconds      = 24 * 60 * 60
	DefaultSSEHeartbeatSeconds = 20
	MinSSEHeartbeatSeconds     = 5
	MaxSSEHeartbeatSeconds     = 60
)

func DefaultRuntimeSettings() RuntimeSettings {
	return RuntimeSettings{
		MaxConcurrentTasks:  DefaultMaxConcurrentTasks,
		TurnTimeoutSeconds:  DefaultTurnTimeoutSeconds,
		SSEHeartbeatSeconds: DefaultSSEHeartbeatSeconds,
		GitWorktrees:        true,
	}
}

func (store *Store) GetRuntimeSettings() (RuntimeSettings, error) {
	value := DefaultRuntimeSettings()
	var data []byte
	err := store.db.QueryRow(`SELECT value_json FROM ea_settings WHERE key=?`, runtimeSettingsKey).Scan(&data)
	if err != nil {
		// A missing row is expected for databases created before this setting.
		if errors.Is(err, sql.ErrNoRows) {
			return value, nil
		}
		return RuntimeSettings{}, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return RuntimeSettings{}, fmt.Errorf("解析运行设置: %w", err)
	}
	return normalizeRuntimeSettings(value), nil
}

func (store *Store) SaveRuntimeSettings(value RuntimeSettings) (RuntimeSettings, error) {
	value = normalizeRuntimeSettings(value)
	data, err := json.Marshal(value)
	if err != nil {
		return RuntimeSettings{}, err
	}
	_, err = store.db.Exec(`INSERT INTO ea_settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, runtimeSettingsKey, data)
	return value, err
}

func normalizeRuntimeSettings(value RuntimeSettings) RuntimeSettings {
	if value.MaxConcurrentTasks < MinMaxConcurrentTasks {
		value.MaxConcurrentTasks = MinMaxConcurrentTasks
	}
	if value.MaxConcurrentTasks > MaxMaxConcurrentTasks {
		value.MaxConcurrentTasks = MaxMaxConcurrentTasks
	}
	if value.TurnTimeoutSeconds < MinTurnTimeoutSeconds {
		value.TurnTimeoutSeconds = DefaultTurnTimeoutSeconds
	}
	if value.TurnTimeoutSeconds > MaxTurnTimeoutSeconds {
		value.TurnTimeoutSeconds = MaxTurnTimeoutSeconds
	}
	if value.SSEHeartbeatSeconds < MinSSEHeartbeatSeconds {
		value.SSEHeartbeatSeconds = DefaultSSEHeartbeatSeconds
	}
	if value.SSEHeartbeatSeconds > MaxSSEHeartbeatSeconds {
		value.SSEHeartbeatSeconds = MaxSSEHeartbeatSeconds
	}
	return value
}
