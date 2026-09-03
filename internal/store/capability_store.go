package store

import (
	"encoding/json"
	"time"
)

func encode(value any) ([]byte, error) { return json.Marshal(value) }

func (store *Store) ListSkillOverrides() ([]SkillOverride, error) {
	rows, err := store.db.Query(`SELECT name,description,content,enabled,builtin FROM ea_skills ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []SkillOverride
	for rows.Next() {
		var value SkillOverride
		if err := rows.Scan(&value.Name, &value.Description, &value.Content, &value.Enabled, &value.Builtin); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) SaveSkill(value SkillOverride) error {
	_, err := store.db.Exec(`INSERT INTO ea_skills(name,description,content,enabled,builtin,updated_at) VALUES(?,?,?,?,?,?)
ON CONFLICT(name) DO UPDATE SET description=excluded.description,content=excluded.content,enabled=excluded.enabled,builtin=excluded.builtin,updated_at=excluded.updated_at`,
		value.Name, value.Description, value.Content, value.Enabled, value.Builtin, formatTime(time.Now()))
	return err
}

func (store *Store) DeleteSkill(name string) error {
	_, err := store.db.Exec(`DELETE FROM ea_skills WHERE name=?`, name)
	return err
}

func (store *Store) ListMCPConfigs() ([]MCPConfig, error) {
	rows, err := store.db.Query(`SELECT config_json FROM ea_mcp ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := []MCPConfig{}
	for rows.Next() {
		var data []byte
		if err := rows.Scan(&data); err != nil {
			return nil, err
		}
		var value MCPConfig
		if err := json.Unmarshal(data, &value); err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, rows.Err()
}

func (store *Store) SaveMCP(value MCPConfig) error {
	data, err := encode(value)
	if err != nil {
		return err
	}
	_, err = store.db.Exec(`INSERT INTO ea_mcp(id,config_json,updated_at) VALUES(?,?,?) ON CONFLICT(id) DO UPDATE SET config_json=excluded.config_json,updated_at=excluded.updated_at`, value.ID, data, formatTime(time.Now()))
	return err
}

func (store *Store) DeleteMCP(id string) error {
	_, err := store.db.Exec(`DELETE FROM ea_mcp WHERE id=?`, id)
	return err
}
