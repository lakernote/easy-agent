package store

import (
	"database/sql"
	"errors"
	"strings"
	"time"
)

func (store *Store) EnsureProject(id, name string, directories []string, now time.Time) error {
	var count int
	if err := store.db.QueryRow(`SELECT COUNT(*) FROM ea_projects`).Scan(&count); err != nil {
		return err
	}
	if count > 0 {
		return nil
	}
	_, err := store.CreateProject(id, name, directories, true, now)
	return err
}

func (store *Store) CreateProject(id, name string, directories []string, makeDefault bool, now time.Time) (Project, error) {
	tx, err := store.db.Begin()
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()
	if makeDefault {
		if _, err := tx.Exec(`UPDATE ea_projects SET is_default=0`); err != nil {
			return Project{}, err
		}
	}
	_, err = tx.Exec(`INSERT INTO ea_projects(id,name,is_default,created_at,updated_at) VALUES(?,?,?,?,?)`, id, name, makeDefault, formatTime(now), formatTime(now))
	if err != nil {
		return Project{}, err
	}
	if err := replaceProjectDirectories(tx, id, directories); err != nil {
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return store.GetProject(id)
}

type sqlExecutor interface {
	Exec(string, ...any) (sql.Result, error)
}

func replaceProjectDirectories(executor sqlExecutor, id string, directories []string) error {
	if _, err := executor.Exec(`DELETE FROM ea_project_directories WHERE project_id=?`, id); err != nil {
		return err
	}
	for index, directory := range directories {
		if _, err := executor.Exec(`INSERT INTO ea_project_directories(project_id,path,position) VALUES(?,?,?)`, id, directory, index); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "unique") {
				return errors.New("项目中存在重复的源文件夹")
			}
			return err
		}
	}
	return nil
}

func (store *Store) UpdateProject(id, name string, directories []string, makeDefault bool, now time.Time) (Project, error) {
	tx, err := store.db.Begin()
	if err != nil {
		return Project{}, err
	}
	defer tx.Rollback()
	if makeDefault {
		if _, err := tx.Exec(`UPDATE ea_projects SET is_default=0`); err != nil {
			return Project{}, err
		}
	}
	result, err := tx.Exec(`UPDATE ea_projects SET name=?,is_default=CASE WHEN ? THEN 1 ELSE is_default END,updated_at=? WHERE id=?`, name, makeDefault, formatTime(now), id)
	if err != nil {
		return Project{}, err
	}
	changed, _ := result.RowsAffected()
	if changed == 0 {
		return Project{}, errors.New("项目不存在")
	}
	if err := replaceProjectDirectories(tx, id, directories); err != nil {
		return Project{}, err
	}
	if err := tx.Commit(); err != nil {
		return Project{}, err
	}
	return store.GetProject(id)
}

func (store *Store) GetProject(id string) (Project, error) {
	var value Project
	var defaultValue int
	var created, updated string
	err := store.db.QueryRow(`SELECT id,name,is_default,created_at,updated_at FROM ea_projects WHERE id=?`, id).Scan(&value.ID, &value.Name, &defaultValue, &created, &updated)
	if err != nil {
		return Project{}, err
	}
	value.Default = defaultValue != 0
	value.CreatedAt, _ = time.Parse(time.RFC3339Nano, created)
	value.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	rows, err := store.db.Query(`SELECT path FROM ea_project_directories WHERE project_id=? ORDER BY position,path`, id)
	if err != nil {
		return Project{}, err
	}
	defer rows.Close()
	value.Directories = []string{}
	for rows.Next() {
		var path string
		if err := rows.Scan(&path); err != nil {
			return Project{}, err
		}
		value.Directories = append(value.Directories, path)
	}
	return value, rows.Err()
}

func (store *Store) ListProjects() ([]Project, error) {
	rows, err := store.db.Query(`SELECT id FROM ea_projects ORDER BY is_default DESC,name COLLATE NOCASE,id`)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	result := make([]Project, 0, len(ids))
	for _, id := range ids {
		value, err := store.GetProject(id)
		if err != nil {
			return nil, err
		}
		result = append(result, value)
	}
	return result, nil
}

func (store *Store) DefaultProject() (Project, error) {
	var id string
	err := store.db.QueryRow(`SELECT id FROM ea_projects ORDER BY is_default DESC,created_at LIMIT 1`).Scan(&id)
	if err != nil {
		return Project{}, err
	}
	return store.GetProject(id)
}

func (store *Store) DeleteProject(id string) error {
	tx, err := store.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var defaultValue int
	if err := tx.QueryRow(`SELECT is_default FROM ea_projects WHERE id=?`, id).Scan(&defaultValue); err != nil {
		return err
	}
	if defaultValue != 0 {
		return errors.New("默认项目不能移除；请先把其他项目设为默认")
	}
	if _, err := tx.Exec(`UPDATE ea_sessions SET project_id='' WHERE project_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`UPDATE ea_weixin_accounts SET project_id='' WHERE project_id=?`, id); err != nil {
		return err
	}
	if _, err := tx.Exec(`DELETE FROM ea_projects WHERE id=?`, id); err != nil {
		return err
	}
	return tx.Commit()
}
