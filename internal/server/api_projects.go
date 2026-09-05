package server

import (
	"database/sql"
	"errors"
	"net/http"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/lakernote/easy-agent/internal/store"
)

type projectRequest struct {
	Name        string   `json:"name"`
	Directories []string `json:"directories"`
	Default     bool     `json:"default"`
}

func (server *Server) ensureDefaultProject() error {
	name := filepath.Base(server.env.Workspace())
	if name == "." || name == string(filepath.Separator) || name == "" {
		name = "默认项目"
	}
	return server.store.EnsureProject(newID(), name, []string{server.env.Workspace()}, time.Now())
}

func (server *Server) projects() ([]store.Project, error) {
	if err := server.ensureDefaultProject(); err != nil {
		return nil, err
	}
	return server.store.ListProjects()
}

func (server *Server) validateProject(input projectRequest) (projectRequest, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" || utf8.RuneCountInString(input.Name) > 60 || containsControl(input.Name) {
		return input, errors.New("项目名称必须为 1 到 60 个有效字符")
	}
	if len(input.Directories) == 0 {
		return input, errors.New("项目至少需要一个源文件夹")
	}
	if len(input.Directories) > 12 {
		return input, errors.New("一个项目最多添加 12 个源文件夹")
	}
	seen := map[string]struct{}{}
	directories := make([]string, 0, len(input.Directories))
	for _, value := range input.Directories {
		environment, err := server.env.WithWorkspace(value)
		if err != nil {
			return input, err
		}
		path := filepath.Clean(environment.Workspace())
		if _, exists := seen[path]; exists {
			continue
		}
		seen[path] = struct{}{}
		directories = append(directories, path)
	}
	if len(directories) == 0 {
		return input, errors.New("项目至少需要一个有效的源文件夹")
	}
	input.Directories = directories
	return input, nil
}

func (server *Server) createProject(response http.ResponseWriter, request *http.Request) {
	var input projectRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input, err := server.validateProject(input)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	projects, err := server.projects()
	if err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	value, err := server.store.CreateProject(newID(), input.Name, input.Directories, input.Default || len(projects) == 0, time.Now())
	if err != nil {
		writeError(response, http.StatusConflict, err.Error())
		return
	}
	writeJSON(response, http.StatusCreated, value)
}

func (server *Server) updateProject(response http.ResponseWriter, request *http.Request) {
	var input projectRequest
	if !decodeJSON(response, request, &input) {
		return
	}
	input, err := server.validateProject(input)
	if err != nil {
		writeError(response, http.StatusBadRequest, err.Error())
		return
	}
	value, err := server.store.UpdateProject(request.PathValue("id"), input.Name, input.Directories, input.Default, time.Now())
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "项目不存在")
		} else {
			writeError(response, http.StatusConflict, err.Error())
		}
		return
	}
	writeJSON(response, http.StatusOK, value)
}

func (server *Server) deleteProject(response http.ResponseWriter, request *http.Request) {
	if err := server.store.DeleteProject(request.PathValue("id")); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			writeError(response, http.StatusNotFound, "项目不存在")
		} else {
			writeError(response, http.StatusConflict, err.Error())
		}
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
