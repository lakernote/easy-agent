package server

import (
	"net/http"
	"regexp"
	"strings"

	builtinskills "github.com/lakernote/easy-agent/internal/builtin/skills"
	"github.com/lakernote/easy-agent/internal/store"
)

var skillNamePattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

func (server *Server) saveSkill(response http.ResponseWriter, request *http.Request) {
	var input store.SkillOverride
	if !decodeJSON(response, request, &input) {
		return
	}
	input.Name = request.PathValue("name")
	if strings.TrimSpace(input.Name) == "" || strings.TrimSpace(input.Description) == "" || strings.TrimSpace(input.Content) == "" {
		writeError(response, http.StatusBadRequest, "Skill 名称、描述和内容不能为空")
		return
	}
	if !skillNamePattern.MatchString(input.Name) {
		writeError(response, http.StatusBadRequest, "Skill 名称只能包含小写英文、数字和短横线")
		return
	}
	parsed, err := builtinskills.Parse(input.Content)
	if err != nil {
		writeError(response, http.StatusBadRequest, "SKILL.md 格式错误："+err.Error())
		return
	}
	if parsed.Name != input.Name {
		writeError(response, http.StatusBadRequest, "SKILL.md 中的 name 必须与 Skill 名称一致")
		return
	}
	// frontmatter 是 Skill 元数据的唯一事实来源，避免列表描述和真正交给
	// Agent 的 SKILL.md 内容不一致。
	input.Description = parsed.Description
	catalog, err := loadSkillCatalog(server.store)
	if err == nil {
		for _, value := range catalog.All() {
			if value.Name == input.Name {
				input.Builtin = value.Builtin
			}
		}
	}
	if err := server.store.SaveSkill(input); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if _, _, err := server.syncCodexCapabilities(); err != nil {
		writeError(response, http.StatusInternalServerError, "Skill 已保存，但同步 Codex 失败："+err.Error())
		return
	}
	writeJSON(response, http.StatusOK, input)
}

func (server *Server) resetSkill(response http.ResponseWriter, request *http.Request) {
	if err := server.store.DeleteSkill(request.PathValue("name")); err != nil {
		writeError(response, http.StatusInternalServerError, err.Error())
		return
	}
	if _, _, err := server.syncCodexCapabilities(); err != nil {
		writeError(response, http.StatusInternalServerError, "Skill 已恢复，但同步 Codex 失败："+err.Error())
		return
	}
	response.WriteHeader(http.StatusNoContent)
}
