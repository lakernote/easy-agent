package server

import (
	"fmt"
	"time"

	"github.com/lakernote/easy-agent/internal/store"
)

// appendSelectedCapabilityEvents 记录用户在本轮明确选择的能力。Skill 会直接
// 注入上下文，不一定产生 load_skill 调用；MCP 选择也不等于真正调用远端工具，
// 因此分别使用“应用”和“已选择”语义，避免页面伪造执行事实。
func (server *Server) appendSelectedCapabilityEvents(sessionID string, turn int, skills, mcps []string) error {
	for _, name := range skills {
		if err := server.store.AppendEvent(sessionID, store.Event{
			Kind: "capability", Turn: turn, Status: "success", Name: name,
			ActivityKind: "skill", ActivitySource: "easyagent", DisplayName: name,
			Detail: "用户明确选择，Skill 说明已应用到本轮任务", CreatedAt: time.Now(),
		}); err != nil {
			return fmt.Errorf("保存 Skill 使用记录: %w", err)
		}
	}
	for _, id := range mcps {
		if err := server.store.AppendEvent(sessionID, store.Event{
			Kind: "capability", Turn: turn, Status: "success", Name: id,
			ActivityKind: "mcp_selected", ActivitySource: id, DisplayName: id,
			Detail: "用户明确选择，本轮可使用该 MCP；实际调用见后续记录", CreatedAt: time.Now(),
		}); err != nil {
			return fmt.Errorf("保存 MCP 选择记录: %w", err)
		}
	}
	return nil
}
