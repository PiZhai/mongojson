package steward

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RunProactiveToolsmithCycle lets the model inspect catalog health and decide
// whether to stay silent or improve the tool platform. The normal Agent loop
// performs every actual catalog mutation, so restart recovery and evidence are
// identical to user-triggered Toolsmith work.
func (s *Service) RunProactiveToolsmithCycle(ctx context.Context) error {
	if !s.autonomyAdvisor().Status().Enabled {
		return nil
	}
	var active int
	if err := s.db.Pool.QueryRow(ctx, `select count(*) from steward_agent_episodes where trigger_kind='proactive_toolsmith' and status in ('thinking','executing','awaiting_input','paused')`).Scan(&active); err != nil || active > 0 {
		return err
	}
	tools, catalog, err := s.agentToolContext(ctx, nil)
	if err != nil {
		return err
	}
	var recentFailures []byte
	_ = s.db.Pool.QueryRow(ctx, `select coalesce(jsonb_agg(x),'[]'::jsonb) from (
		select tool_name,tool_version,test_name,error_summary,started_at from steward_tool_test_runs
		where status='failed' order by started_at desc limit 20
	) x`).Scan(&recentFailures)
	prompt := strings.Join([]string{
		"这是后台 proactive_toolsmith 检查。请结合完整工具目录、近期失败和当前设备状态，自主判断是否需要创建、更新、合并、停用或删除工具。",
		"先判断能否复用或组合已有工具；依赖方案按原生 API、组合、标准库、工具内隔离、隔离 CLI、共享依赖、机器全局的顺序比较。",
		"创建脚本工具时必须遵守 tool.create 中的 steward-tool/1 精确协议：测试 input 直接填写业务参数，stdout 最后一行返回 ok/output/evidence 外壳；PowerShell 不得把自动变量 $args 当作业务参数对象。",
		"如果没有值得采取的动作，单独调用 steward.stay_silent。不要为了制造活动而创建重复工具。",
		"近期失败：" + string(recentFailures),
	}, "\n")
	advisor, ok := s.autonomyAdvisor().(AgentTurnAdvisor)
	if !ok {
		return nil
	}
	decision, err := nextValidAgentTurn(ctx, advisor, AgentTurnInput{
		Message: prompt, TriggerKind: "proactive_toolsmith", Tools: tools, ToolCatalog: catalog,
		Devices: s.conversationAdvisorDevices(ctx), KnownFolders: runtimeKnownFolders(), CurrentTime: time.Now(), Round: 1,
	})
	if err != nil {
		return err
	}
	if len(decision.ToolCalls) == 0 {
		return nil
	}
	conversation, err := s.ensureProactiveConversation(ctx)
	if err != nil {
		return err
	}
	trigger, err := s.insertConversationMessage(ctx, conversation.ID, conversationRoleSystem, prompt, "", s.autonomyAdvisor().Status().Model, "proactive_toolsmith:"+time.Now().UTC().Format(time.RFC3339))
	if err != nil {
		return err
	}
	_, episode, err := s.startAgentEpisode(ctx, conversation, trigger, "维护和扩展 Windows 工具平台", "", "proactive_toolsmith", decision)
	if err != nil {
		return err
	}
	details, _ := json.Marshal(map[string]any{"catalog_generation": s.runtimeTools.generationValue(), "tool_calls": len(decision.ToolCalls)})
	_ = s.recordToolCatalogEvent(ctx, "tool.toolsmith", episode.ID, "proactive_started", "model", fmt.Sprintf("proactive Toolsmith episode %s started", episode.ID), map[string]any{"details": string(details)})
	return nil
}
