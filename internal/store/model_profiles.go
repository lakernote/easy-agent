package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

func (store *Store) GetModelSettings() (ModelSettings, error) {
	profiles, activeID, err := store.ListModelProfiles()
	if err != nil {
		return ModelSettings{}, err
	}
	for _, profile := range profiles {
		if profile.ID == activeID {
			return withProfile(profile), nil
		}
	}
	return ModelSettings{}, errors.New("没有可用的模型配置")
}

func (store *Store) SaveModelSettings(value ModelSettings) error {
	return store.saveModelProfile(value, true)
}

// SaveModelProfile updates one reusable profile without changing which profile
// is used for new sessions. Activation is a separate operation so selecting a
// profile never has to resubmit redacted settings or secrets.
func (store *Store) SaveModelProfile(value ModelSettings) error {
	return store.saveModelProfile(value, false)
}

func (store *Store) saveModelProfile(value ModelSettings, activate bool) error {
	value = value.WithDefaults()
	profiles, activeID, err := store.ListModelProfiles()
	if err != nil {
		return err
	}
	if value.ProfileID == "" {
		value.ProfileID = activeID
	}
	if value.ProfileID == "" {
		value.ProfileID = "default"
	}
	name := strings.TrimSpace(value.ProfileName)
	for _, profile := range profiles {
		if profile.ID == value.ProfileID && name == "" {
			name = profile.Name
		}
	}
	if name == "" {
		name = defaultModelProfileName(value)
	}
	value.ProfileName = name
	updated := false
	for index := range profiles {
		if profiles[index].ID == value.ProfileID {
			profiles[index] = ModelProfile{ID: value.ProfileID, Name: name, Settings: value}
			updated = true
			break
		}
	}
	if !updated {
		profiles = append(profiles, ModelProfile{ID: value.ProfileID, Name: name, Settings: value})
	}
	if activate || activeID == "" {
		activeID = value.ProfileID
	}
	return store.saveModelProfiles(profiles, activeID, filteredModelSettings(profiles, activeID))
}

// ListModelProfiles 返回所有模型配置以及当前用于创建新会话的配置 ID。
// 旧版本只有 ea_settings.model；首次读取时将它无损包装成 default profile。
func (store *Store) ListModelProfiles() ([]ModelProfile, string, error) {
	var data []byte
	err := store.db.QueryRow(`SELECT value_json FROM ea_settings WHERE key='model_profiles'`).Scan(&data)
	if err == nil {
		var profiles []ModelProfile
		if err := json.Unmarshal(data, &profiles); err != nil {
			return nil, "", err
		}
		activeID := ""
		_ = store.db.QueryRow(`SELECT value_json FROM ea_settings WHERE key='model_active_profile'`).Scan(&activeID)
		activeID = strings.Trim(activeID, `" `)
		if activeID == "" && len(profiles) > 0 {
			activeID = profiles[0].ID
		}
		for index := range profiles {
			profiles[index].Settings = withProfile(profiles[index])
		}
		return profiles, activeID, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, "", err
	}
	legacy, legacyErr := store.legacyModelSettings()
	if legacyErr != nil {
		if errors.Is(legacyErr, sql.ErrNoRows) {
			legacy = DefaultModelSettings()
		} else {
			return nil, "", legacyErr
		}
	}
	legacy = legacy.WithDefaults()
	legacy.ProfileID = "default"
	legacy.ProfileName = defaultModelProfileName(legacy)
	return []ModelProfile{{ID: legacy.ProfileID, Name: legacy.ProfileName, Settings: legacy}}, legacy.ProfileID, nil
}

// GetModelSettingsByProfileID 为新会话读取指定配置；空 ID 兼容旧客户端并使用当前配置。
func (store *Store) GetModelSettingsByProfileID(id string) (ModelSettings, error) {
	profiles, activeID, err := store.ListModelProfiles()
	if err != nil {
		return ModelSettings{}, err
	}
	if strings.TrimSpace(id) == "" {
		id = activeID
	}
	for _, profile := range profiles {
		if profile.ID == id {
			return withProfile(profile), nil
		}
	}
	return ModelSettings{}, fmt.Errorf("模型配置不存在: %s", id)
}

// SetActiveModelProfile only changes the default selection. It intentionally
// leaves the selected profile payload untouched, especially its API key.
func (store *Store) SetActiveModelProfile(id string) (ModelSettings, error) {
	profiles, _, err := store.ListModelProfiles()
	if err != nil {
		return ModelSettings{}, err
	}
	for _, profile := range profiles {
		if profile.ID != id {
			continue
		}
		active := withProfile(profile)
		if err := store.saveModelProfiles(profiles, id, active); err != nil {
			return ModelSettings{}, err
		}
		return active, nil
	}
	return ModelSettings{}, fmt.Errorf("模型配置不存在: %s", id)
}

func (store *Store) DeleteModelProfile(id string) error {
	profiles, activeID, err := store.ListModelProfiles()
	if err != nil {
		return err
	}
	if len(profiles) <= 1 {
		return errors.New("至少保留一套模型配置")
	}
	var references int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ea_sessions WHERE profile_id=?`, id).Scan(&references); err != nil {
		return err
	}
	if references > 0 {
		return fmt.Errorf("这套配置仍被 %d 个会话使用，不能删除", references)
	}
	filtered := make([]ModelProfile, 0, len(profiles)-1)
	for _, profile := range profiles {
		if profile.ID != id {
			filtered = append(filtered, profile)
		}
	}
	if len(filtered) == len(profiles) {
		return fmt.Errorf("模型配置不存在: %s", id)
	}
	if activeID == id {
		activeID = filtered[0].ID
	}
	return store.saveModelProfiles(filtered, activeID, filteredModelSettings(filtered, activeID))
}

func (store *Store) legacyModelSettings() (ModelSettings, error) {
	var data []byte
	if err := store.db.QueryRow(`SELECT value_json FROM ea_settings WHERE key='model'`).Scan(&data); err != nil {
		return ModelSettings{}, err
	}
	var result ModelSettings
	if err := json.Unmarshal(data, &result); err != nil {
		return ModelSettings{}, err
	}
	return result, nil
}

func withProfile(profile ModelProfile) ModelSettings {
	value := profile.Settings.WithDefaults()
	value.ProfileID, value.ProfileName = profile.ID, profile.Name
	return value
}

func defaultModelProfileName(value ModelSettings) string {
	if value.Runtime == RuntimeCodex {
		return "Codex 默认配置"
	}
	return "EasyAgent 默认配置"
}

func filteredModelSettings(profiles []ModelProfile, activeID string) ModelSettings {
	for _, profile := range profiles {
		if profile.ID == activeID {
			return withProfile(profile)
		}
	}
	return ModelSettings{}
}

func (store *Store) saveModelProfiles(profiles []ModelProfile, activeID string, active ModelSettings) error {
	data, err := encode(profiles)
	if err != nil {
		return err
	}
	activeData, err := encode(activeID)
	if err != nil {
		return err
	}
	legacyData, err := encode(active)
	if err != nil {
		return err
	}
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.Exec(`INSERT INTO ea_settings(key,value_json) VALUES('model_profiles',?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, data); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO ea_settings(key,value_json) VALUES('model_active_profile',?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, activeData); err != nil {
		return err
	}
	if _, err = tx.Exec(`INSERT INTO ea_settings(key,value_json) VALUES('model',?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, legacyData); err != nil {
		return err
	}
	return tx.Commit()
}
