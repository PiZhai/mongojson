package main

import (
	"sort"
	"strings"
)

func normalizeStringSet(values []string) []string {
	seen := map[string]struct{}{}
	out := []string{}
	for _, value := range values {
		value = strings.ToLower(strings.TrimSpace(value))
		if value == "" {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func normalizeKindPlatformRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, ok := splitKindPlatformRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformAgentRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, agentID, ok := splitPlatformAgentRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+agentID)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformAgentRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, agentID, ok := splitKindPlatformAgentRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+agentID)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformServiceScopeRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, scope, ok := splitPlatformServiceScopeRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+scope)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformServiceScopeRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, scope, ok := splitKindPlatformServiceScopeRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+scope)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformServiceNameRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, name, ok := splitPlatformServiceNameRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+name)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformServiceNameRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, name, ok := splitKindPlatformServiceNameRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+name)
	}
	return normalizeStringSet(normalized)
}

func normalizePlatformAdvisorValueRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		platform, advisorValue, ok := splitPlatformAdvisorValueRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, platform+":"+advisorValue)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindPlatformAdvisorValueRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, platform, advisorValue, ok := splitKindPlatformAdvisorValueRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+platform+":"+advisorValue)
	}
	return normalizeStringSet(normalized)
}

func normalizeCheckPlatformRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		check, platform, ok := splitCheckPlatformRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, check+":"+platform)
	}
	return normalizeStringSet(normalized)
}

func normalizeKindCheckPlatformRequirements(values []string) []string {
	normalized := make([]string, 0, len(values))
	for _, value := range values {
		kind, check, platform, ok := splitKindCheckPlatformRequirement(value)
		if !ok {
			normalized = append(normalized, strings.ToLower(strings.TrimSpace(value)))
			continue
		}
		normalized = append(normalized, kind+":"+check+":"+platform)
	}
	return normalizeStringSet(normalized)
}

func splitKindPlatformRequirement(value string) (string, string, bool) {
	kind, platform, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	return kind, platform, ok && kind != "" && platform != ""
}

func splitPlatformAgentRequirement(value string) (string, string, bool) {
	platform, agentID, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	agentID = strings.TrimSpace(agentID)
	return platform, agentID, ok && platform != "" && agentID != ""
}

func splitKindPlatformAgentRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, agentID, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	agentID = strings.TrimSpace(agentID)
	return kind, platform, agentID, ok && platformOK && kind != "" && platform != "" && agentID != ""
}

func splitPlatformServiceScopeRequirement(value string) (string, string, bool) {
	platform, scope, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	scope = strings.TrimSpace(scope)
	return platform, scope, ok && platform != "" && scope != ""
}

func splitKindPlatformServiceScopeRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, scope, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	scope = strings.TrimSpace(scope)
	return kind, platform, scope, ok && platformOK && kind != "" && platform != "" && scope != ""
}

func splitPlatformServiceNameRequirement(value string) (string, string, bool) {
	platform, name, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	name = strings.TrimSpace(name)
	return platform, name, ok && platform != "" && name != ""
}

func splitKindPlatformServiceNameRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, name, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	name = strings.TrimSpace(name)
	return kind, platform, name, ok && platformOK && kind != "" && platform != "" && name != ""
}

func splitPlatformAdvisorValueRequirement(value string) (string, string, bool) {
	platform, advisorValue, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform = strings.TrimSpace(platform)
	advisorValue = strings.TrimSpace(advisorValue)
	return platform, advisorValue, ok && platform != "" && advisorValue != ""
}

func splitKindPlatformAdvisorValueRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	platform, advisorValue, platformOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	platform = strings.TrimSpace(platform)
	advisorValue = strings.TrimSpace(advisorValue)
	return kind, platform, advisorValue, ok && platformOK && kind != "" && platform != "" && advisorValue != ""
}

func splitCheckPlatformRequirement(value string) (string, string, bool) {
	check, platform, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	check = strings.TrimSpace(check)
	platform = strings.TrimSpace(platform)
	return check, platform, ok && check != "" && platform != ""
}

func splitKindCheckPlatformRequirement(value string) (string, string, string, bool) {
	kind, rest, ok := strings.Cut(strings.ToLower(strings.TrimSpace(value)), ":")
	check, platform, checkOK := strings.Cut(rest, ":")
	kind = strings.TrimSpace(kind)
	check = strings.TrimSpace(check)
	platform = strings.TrimSpace(platform)
	return kind, check, platform, ok && checkOK && kind != "" && check != "" && platform != ""
}

func stringSetContains(values []string, target string) bool {
	target = strings.ToLower(strings.TrimSpace(target))
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
