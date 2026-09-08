package store

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

const researchSettingsKey = "research_settings"

// ResearchProviderSettings stores only administrator overrides. Empty values
// intentionally fall back to the EasyAgent service environment at runtime.
// Secrets are redacted by the HTTP layer before settings reach the browser.
type ResearchProviderSettings struct {
	Endpoint string `json:"endpoint,omitempty"`
	Secret   string `json:"secret,omitempty"`
}

type ResearchSettings struct {
	Providers map[string]ResearchProviderSettings `json:"providers"`
}

func DefaultResearchSettings() ResearchSettings {
	return ResearchSettings{Providers: map[string]ResearchProviderSettings{}}
}

func (store *Store) GetResearchSettings() (ResearchSettings, error) {
	value := DefaultResearchSettings()
	var data []byte
	err := store.db.QueryRow(`SELECT value_json FROM ea_settings WHERE key=?`, researchSettingsKey).Scan(&data)
	if errors.Is(err, sql.ErrNoRows) {
		return value, nil
	}
	if err != nil {
		return ResearchSettings{}, err
	}
	if err := json.Unmarshal(data, &value); err != nil {
		return ResearchSettings{}, fmt.Errorf("解析 Research 设置: %w", err)
	}
	return normalizeResearchSettings(value), nil
}

func (store *Store) SaveResearchSettings(value ResearchSettings) (ResearchSettings, error) {
	value = normalizeResearchSettings(value)
	data, err := json.Marshal(value)
	if err != nil {
		return ResearchSettings{}, err
	}
	_, err = store.db.Exec(`INSERT INTO ea_settings(key,value_json) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value_json=excluded.value_json`, researchSettingsKey, data)
	return value, err
}

func normalizeResearchSettings(value ResearchSettings) ResearchSettings {
	result := DefaultResearchSettings()
	for rawID, provider := range value.Providers {
		id := strings.TrimSpace(rawID)
		if id == "" {
			continue
		}
		provider.Endpoint = strings.TrimSpace(provider.Endpoint)
		provider.Secret = strings.TrimSpace(provider.Secret)
		if provider.Endpoint == "" && provider.Secret == "" {
			continue
		}
		result.Providers[id] = provider
	}
	return result
}
