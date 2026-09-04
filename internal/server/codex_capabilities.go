package server

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/lakernote/easy-agent/internal/codexruntime"
	"github.com/lakernote/easy-agent/internal/store"
)

// syncCodexCapabilities materializes the shared EasyAgent capability catalog in
// formats Codex understands. EasyAgent Runtime continues to read the same SQLite
// records directly, making the database the single source of truth.
func (server *Server) syncCodexCapabilities() ([]codexruntime.SkillRef, map[string]string, error) {
	catalog, err := loadSkillCatalog(server.store)
	if err != nil {
		return nil, nil, err
	}
	privateRoot := filepath.Join(server.env.Runtime(), "codex-skills")
	if err := os.MkdirAll(privateRoot, 0o700); err != nil {
		return nil, nil, fmt.Errorf("创建 Codex Skill 目录: %w", err)
	}
	discoveryRoot := ""
	if server.externalCapabilitySync {
		if userHome, homeErr := os.UserHomeDir(); homeErr == nil {
			discoveryRoot = filepath.Join(userHome, ".agents", "skills")
			_ = os.MkdirAll(discoveryRoot, 0o700)
		}
	}
	refs := make([]codexruntime.SkillRef, 0)
	desiredLinks := make(map[string]struct{})
	for _, skill := range catalog.All() {
		link := ""
		if discoveryRoot != "" {
			link = filepath.Join(discoveryRoot, "easyagent-"+skill.Name)
		}
		if !skill.Enabled {
			if link != "" {
				if info, linkErr := os.Lstat(link); linkErr == nil && info.Mode()&os.ModeSymlink != 0 {
					_ = os.Remove(link)
				}
			}
			continue
		}
		directory := filepath.Join(privateRoot, skill.Name)
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return nil, nil, err
		}
		path := filepath.Join(directory, "SKILL.md")
		if err := os.WriteFile(path, []byte(strings.TrimSpace(skill.Content)+"\n"), 0o600); err != nil {
			return nil, nil, fmt.Errorf("同步 Skill %s: %w", skill.Name, err)
		}
		refs = append(refs, codexruntime.SkillRef{Name: skill.Name, Path: path})
		if link != "" {
			desiredLinks[link] = struct{}{}
			if _, err := os.Lstat(link); os.IsNotExist(err) {
				_ = os.Symlink(directory, link)
			}
		}
	}
	if discoveryRoot != "" {
		entries, _ := os.ReadDir(discoveryRoot)
		for _, entry := range entries {
			link := filepath.Join(discoveryRoot, entry.Name())
			if !strings.HasPrefix(entry.Name(), "easyagent-") {
				continue
			}
			if _, wanted := desiredLinks[link]; wanted {
				continue
			}
			if info, err := os.Lstat(link); err == nil && info.Mode()&os.ModeSymlink != 0 {
				target, _ := filepath.EvalSymlinks(link)
				if relative, relErr := filepath.Rel(privateRoot, target); relErr == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
					_ = os.Remove(link)
				}
			}
		}
	}

	configs, err := server.store.ListMCPConfigs()
	if err != nil {
		return nil, nil, err
	}
	mcps := make([]codexruntime.MCPServerConfig, 0, len(configs))
	for _, config := range configs {
		if !config.Enabled {
			continue
		}
		command := config.Command
		if config.Transport == "stdio" {
			resolved, resolveErr := server.env.ResolveCommand(command)
			if resolveErr != nil {
				return nil, nil, fmt.Errorf("解析 MCP %s 命令: %w", config.Name, resolveErr)
			}
			command = resolved
		}
		mcps = append(mcps, codexruntime.MCPServerConfig{
			ID: config.ID, Transport: config.Transport, Command: command,
			Args: append([]string(nil), config.Args...), Endpoint: config.Endpoint,
			AuthType: config.AuthType, Token: config.Token, Username: config.Username,
			Password: config.Password, Headers: cloneMap(config.Headers), Environment: cloneMap(config.Environment),
		})
	}
	if !server.externalCapabilitySync {
		return refs, map[string]string{}, nil
	}
	environment, err := codexruntime.SyncMCPServers(mcps)
	if err != nil {
		return nil, nil, fmt.Errorf("同步 Codex MCP: %w", err)
	}
	return refs, environment, nil
}

func selectedCodexSkills(messages []store.Message, refs []codexruntime.SkillRef) []codexruntime.SkillRef {
	selected := selectedSkillNames(messages)
	result := make([]codexruntime.SkillRef, 0, len(selected))
	for _, ref := range refs {
		if _, ok := selected[ref.Name]; ok {
			result = append(result, ref)
		}
	}
	return result
}
