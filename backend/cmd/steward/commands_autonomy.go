package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (c cli) autonomy(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printAutonomyUsage()
		return nil
	}
	if len(args) == 0 {
		return c.printRequest(http.MethodGet, "/steward/autonomy", nil)
	}
	switch args[0] {
	case "status":
		return c.printRequest(http.MethodGet, "/steward/autonomy", nil)
	case "pause":
		return c.printRequest(http.MethodPatch, "/steward/autonomy/settings", map[string]any{"paused": true})
	case "resume":
		return c.printRequest(http.MethodPatch, "/steward/autonomy/settings", map[string]any{"paused": false})
	case "run":
		return c.printRequest(http.MethodPost, "/steward/autonomy/run", nil)
	case "mode":
		if len(args) < 2 {
			return fmt.Errorf("autonomy mode requires suggest_only or controlled")
		}
		mode := strings.TrimSpace(args[1])
		if mode != "suggest_only" && mode != "controlled" {
			return fmt.Errorf("unsupported autonomy mode %q", mode)
		}
		return c.printRequest(http.MethodPatch, "/steward/autonomy/settings", map[string]any{"mode": mode})
	case "rules":
		return c.printRequest(http.MethodGet, "/steward/autonomy/rules", nil)
	case "rule-policy":
		if len(args) < 3 {
			return fmt.Errorf("autonomy rule-policy requires a rule id or name and suggest, confirm, auto, or never")
		}
		policy := strings.TrimSpace(args[2])
		if policy != "suggest" && policy != "confirm" && policy != "auto" && policy != "never" {
			return fmt.Errorf("unsupported autonomy rule policy %q", policy)
		}
		return c.updateAutonomyRule(strings.TrimSpace(args[1]), map[string]any{"policy": policy})
	case "rule-enable", "rule-disable":
		if len(args) < 2 {
			return fmt.Errorf("autonomy %s requires a rule id or name", args[0])
		}
		return c.updateAutonomyRule(strings.TrimSpace(args[1]), map[string]any{"enabled": args[0] == "rule-enable"})
	case "dismiss-candidates", "bulk-dismiss":
		return c.dismissAutonomyProposals(args[1:])
	default:
		return fmt.Errorf("unknown autonomy command %q", args[0])
	}
}

func (c cli) updateAutonomyRule(idOrName string, payload map[string]any) error {
	ruleID, err := c.resolveAutonomyRuleID(idOrName)
	if err != nil {
		return err
	}
	return c.printRequest(http.MethodPatch, "/steward/autonomy/rules/"+url.PathEscape(ruleID), payload)
}

func (c cli) resolveAutonomyRuleID(idOrName string) (string, error) {
	value := strings.TrimSpace(idOrName)
	if value == "" {
		return "", fmt.Errorf("autonomy rule id or name is required")
	}
	body, err := c.request(http.MethodGet, "/steward/autonomy/rules", nil)
	if err != nil {
		return "", err
	}
	var response struct {
		Rules []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		} `json:"rules"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return "", fmt.Errorf("decode autonomy rules: %w", err)
	}
	for _, rule := range response.Rules {
		if rule.ID == value || rule.Name == value {
			return rule.ID, nil
		}
	}
	return "", fmt.Errorf("autonomy rule %q not found", value)
}

func (c cli) dismissAutonomyProposals(args []string) error {
	fs := flag.NewFlagSet("steward autonomy bulk-dismiss", flag.ExitOnError)
	status := fs.String("status", "candidate", "Proposal status to dismiss: candidate, approved, or blocked")
	limit := fs.Int("limit", 50, "Maximum proposals to dismiss")
	reason := fs.String("reason", "manual CLI cleanup", "Audit reason for the bulk cleanup")
	if err := fs.Parse(args); err != nil {
		return err
	}
	return c.printRequest(http.MethodPost, "/steward/autonomy/proposals/bulk-dismiss", map[string]any{
		"status": strings.TrimSpace(*status),
		"limit":  *limit,
		"reason": strings.TrimSpace(*reason),
	})
}

func printAutonomyUsage() {
	fmt.Fprintln(stdout, `usage: steward autonomy <status|pause|resume|run|mode|rules|rule-policy|rule-enable|rule-disable|dismiss-candidates|bulk-dismiss> [args]

autonomy commands:
  status                         print rules, proposals, approvals, runs, and advisor status
  pause                          stop background proposal/execution creation
  resume                         allow background autonomy cycles again
  run                            run one candidate scan now
  mode <suggest_only|controlled> set global autonomy mode
  rules                          list configurable autonomy rules
  rule-policy <id-or-name> <suggest|confirm|auto|never>
  rule-enable <id-or-name>
  rule-disable <id-or-name>
  bulk-dismiss --status candidate --limit 50

S4 guardrail:
  all A0-A9 levels remain denied unless the permission matrix, autonomy ceiling, action rule,
  simulation/rollback requirements, and a registered or configured executor all allow them.`)
}
