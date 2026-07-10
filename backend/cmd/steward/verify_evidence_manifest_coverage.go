package main

import (
	"strings"
	"time"
)

func verificationEvidenceCoverageFromFiles(files []verificationEvidenceFileSummary) verificationEvidenceCoverage {
	kinds := []string{}
	platforms := []string{}
	agentIDs := []string{}
	serviceScopes := []string{}
	serviceNames := []string{}
	kindPlatforms := []string{}
	platformAgents := []string{}
	kindPlatformAgents := []string{}
	platformServiceScopes := []string{}
	kindPlatformServiceScopes := []string{}
	platformServiceNames := []string{}
	kindPlatformServiceNames := []string{}
	advisorProviders := []string{}
	platformAdvisorProviders := []string{}
	kindPlatformAdvisorProviders := []string{}
	advisorModels := []string{}
	platformAdvisorModels := []string{}
	kindPlatformAdvisorModels := []string{}
	advisorMaxDataLevels := []string{}
	platformAdvisorMaxDataLevels := []string{}
	kindPlatformAdvisorMaxDataLevels := []string{}
	checks := []string{}
	passingChecks := []string{}
	failingChecks := []string{}
	checkPlatforms := []string{}
	passingCheckPlatforms := []string{}
	failingCheckPlatforms := []string{}
	kindCheckPlatforms := []string{}
	passingKindCheckPlatforms := []string{}
	failingKindCheckPlatforms := []string{}
	coverage := verificationEvidenceCoverage{TotalFiles: len(files)}
	var maxWatchSpan time.Duration
	platformMaxSpans := map[string]time.Duration{}
	for _, file := range files {
		if file.Error != "" {
			coverage.FailingFiles++
			continue
		}
		if file.OK {
			coverage.PassingFiles++
		} else {
			coverage.FailingFiles++
		}
		kinds = append(kinds, file.Kind)
		platforms = append(platforms, file.Platforms...)
		agentIDs = append(agentIDs, file.AgentIDs...)
		serviceScopes = append(serviceScopes, file.ServiceScopes...)
		serviceNames = append(serviceNames, file.ServiceNames...)
		platformAgents = append(platformAgents, file.PlatformAgents...)
		kindPlatformAgents = append(kindPlatformAgents, file.KindPlatformAgents...)
		platformServiceScopes = append(platformServiceScopes, file.PlatformServiceScopes...)
		kindPlatformServiceScopes = append(kindPlatformServiceScopes, file.KindPlatformServiceScopes...)
		platformServiceNames = append(platformServiceNames, file.PlatformServiceNames...)
		kindPlatformServiceNames = append(kindPlatformServiceNames, file.KindPlatformServiceNames...)
		advisorProviders = append(advisorProviders, file.AdvisorProviders...)
		platformAdvisorProviders = append(platformAdvisorProviders, file.PlatformAdvisorProviders...)
		kindPlatformAdvisorProviders = append(kindPlatformAdvisorProviders, file.KindPlatformAdvisorProviders...)
		advisorModels = append(advisorModels, file.AdvisorModels...)
		platformAdvisorModels = append(platformAdvisorModels, file.PlatformAdvisorModels...)
		kindPlatformAdvisorModels = append(kindPlatformAdvisorModels, file.KindPlatformAdvisorModels...)
		advisorMaxDataLevels = append(advisorMaxDataLevels, file.AdvisorMaxDataLevels...)
		platformAdvisorMaxDataLevels = append(platformAdvisorMaxDataLevels, file.PlatformAdvisorMaxDataLevels...)
		kindPlatformAdvisorMaxDataLevels = append(kindPlatformAdvisorMaxDataLevels, file.KindPlatformAdvisorMaxDataLevels...)
		checks = append(checks, file.Checks...)
		passingChecks = append(passingChecks, file.PassingChecks...)
		failingChecks = append(failingChecks, file.FailingChecks...)
		checkPlatforms = append(checkPlatforms, file.CheckPlatforms...)
		passingCheckPlatforms = append(passingCheckPlatforms, file.PassingCheckPlatforms...)
		failingCheckPlatforms = append(failingCheckPlatforms, file.FailingCheckPlatforms...)
		kindCheckPlatforms = append(kindCheckPlatforms, file.KindCheckPlatforms...)
		passingKindCheckPlatforms = append(passingKindCheckPlatforms, file.PassingKindCheckPlatforms...)
		failingKindCheckPlatforms = append(failingKindCheckPlatforms, file.FailingKindCheckPlatforms...)
		fileWatchSpan := time.Duration(file.WatchSpanMillis) * time.Millisecond
		for _, platform := range file.Platforms {
			kindPlatforms = append(kindPlatforms, file.Kind+":"+platform)
			if fileWatchSpan > platformMaxSpans[platform] {
				platformMaxSpans[platform] = fileWatchSpan
			}
		}
		if file.WatchSamples > coverage.MaxWatchSamples {
			coverage.MaxWatchSamples = file.WatchSamples
		}
		if fileWatchSpan > maxWatchSpan {
			maxWatchSpan = fileWatchSpan
		}
	}
	coverage.Kinds = normalizeStringSet(kinds)
	coverage.Platforms = normalizeStringSet(platforms)
	coverage.AgentIDs = normalizeStringSet(agentIDs)
	coverage.ServiceScopes = normalizeStringSet(serviceScopes)
	coverage.ServiceNames = normalizeStringSet(serviceNames)
	coverage.KindPlatforms = normalizeStringSet(kindPlatforms)
	coverage.PlatformAgents = normalizeStringSet(platformAgents)
	coverage.KindPlatformAgents = normalizeStringSet(kindPlatformAgents)
	coverage.PlatformServiceScopes = normalizeStringSet(platformServiceScopes)
	coverage.KindPlatformServiceScopes = normalizeStringSet(kindPlatformServiceScopes)
	coverage.PlatformServiceNames = normalizeStringSet(platformServiceNames)
	coverage.KindPlatformServiceNames = normalizeStringSet(kindPlatformServiceNames)
	coverage.AdvisorProviders = normalizeStringSet(advisorProviders)
	coverage.PlatformAdvisorProviders = normalizeStringSet(platformAdvisorProviders)
	coverage.KindPlatformAdvisorProviders = normalizeStringSet(kindPlatformAdvisorProviders)
	coverage.AdvisorModels = normalizeStringSet(advisorModels)
	coverage.PlatformAdvisorModels = normalizeStringSet(platformAdvisorModels)
	coverage.KindPlatformAdvisorModels = normalizeStringSet(kindPlatformAdvisorModels)
	coverage.AdvisorMaxDataLevels = normalizeStringSet(advisorMaxDataLevels)
	coverage.PlatformAdvisorMaxDataLevels = normalizeStringSet(platformAdvisorMaxDataLevels)
	coverage.KindPlatformAdvisorMaxDataLevels = normalizeStringSet(kindPlatformAdvisorMaxDataLevels)
	coverage.Checks = normalizeStringSet(checks)
	coverage.PassingChecks = normalizeStringSet(passingChecks)
	coverage.FailingChecks = normalizeStringSet(failingChecks)
	coverage.CheckPlatforms = normalizeStringSet(checkPlatforms)
	coverage.PassingCheckPlatforms = normalizeStringSet(passingCheckPlatforms)
	coverage.FailingCheckPlatforms = normalizeStringSet(failingCheckPlatforms)
	coverage.KindCheckPlatforms = normalizeStringSet(kindCheckPlatforms)
	coverage.PassingKindCheckPlatforms = normalizeStringSet(passingKindCheckPlatforms)
	coverage.FailingKindCheckPlatforms = normalizeStringSet(failingKindCheckPlatforms)
	coverage.MaxWatchSpan = maxWatchSpan.String()
	coverage.MaxWatchSpanMillis = maxWatchSpan.Milliseconds()
	coverage.PlatformMaxWatchSpans = map[string]string{}
	coverage.PlatformMaxWatchSpanMillis = map[string]int64{}
	for _, platform := range normalizeStringSet(platforms) {
		span := platformMaxSpans[platform]
		coverage.PlatformMaxWatchSpans[platform] = span.String()
		coverage.PlatformMaxWatchSpanMillis[platform] = span.Milliseconds()
	}
	return coverage
}

func collectEvidenceStringValues(value any, key string) []string {
	values := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			for itemKey, itemValue := range typed {
				if strings.EqualFold(itemKey, key) {
					if text, ok := itemValue.(string); ok {
						values = append(values, text)
					}
				}
				visit(itemValue)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return values
}

func verificationEvidenceWatchStats(value any) (int, time.Duration) {
	maxSamples := 0
	var maxSpan time.Duration
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if samples, ok := typed["samples"].([]any); ok {
				if len(samples) > maxSamples {
					maxSamples = len(samples)
				}
				if span := sampleWatchSpan(samples); span > maxSpan {
					maxSpan = span
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return maxSamples, maxSpan
}

func verificationEvidenceCheckStats(value any) ([]string, []string, []string) {
	all := []string{}
	passing := []string{}
	failing := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			id, idOK := typed["id"].(string)
			status, statusOK := typed["status"].(string)
			if idOK && statusOK {
				id = strings.TrimSpace(id)
				status = strings.ToLower(strings.TrimSpace(status))
				if id != "" {
					all = append(all, id)
					if status == "ok" {
						passing = append(passing, id)
					} else if status != "" {
						failing = append(failing, id)
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(all), normalizeStringSet(passing), normalizeStringSet(failing)
}

func verificationEvidencePlatformAgentStats(value any) []string {
	pairs := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			platform, platformOK := typed["platform"].(string)
			agentID, agentOK := typed["agent_id"].(string)
			platform = strings.ToLower(strings.TrimSpace(platform))
			agentID = strings.ToLower(strings.TrimSpace(agentID))
			if platformOK && agentOK && platform != "" && agentID != "" {
				pairs = append(pairs, platform+":"+agentID)
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(pairs)
}

func verificationEvidenceServiceScopeStats(value any) []string {
	scopes := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if scope, ok := typed["service_scope"].(string); ok {
				scopes = append(scopes, scope)
			}
			if platform, platformOK := typed["platform"].(string); platformOK && strings.TrimSpace(platform) != "" {
				if scope, ok := typed["scope"].(string); ok {
					scopes = append(scopes, scope)
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(scopes)
}

func verificationEvidencePlatformServiceScopeStats(value any) []string {
	pairs := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			platform, platformOK := typed["platform"].(string)
			platform = strings.ToLower(strings.TrimSpace(platform))
			if platformOK && platform != "" {
				if scope, ok := typed["service_scope"].(string); ok {
					scope = strings.ToLower(strings.TrimSpace(scope))
					if scope != "" {
						pairs = append(pairs, platform+":"+scope)
					}
				}
				if scope, ok := typed["scope"].(string); ok {
					scope = strings.ToLower(strings.TrimSpace(scope))
					if scope != "" {
						pairs = append(pairs, platform+":"+scope)
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(pairs)
}

func verificationEvidenceServiceNameStats(value any) []string {
	names := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if name, ok := typed["service_name"].(string); ok {
				names = append(names, name)
			}
			if evidenceMapLooksLikeService(typed) {
				if name, ok := typed["name"].(string); ok {
					names = append(names, name)
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(names)
}

func verificationEvidencePlatformServiceNameStats(value any) []string {
	pairs := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			platform, platformOK := typed["platform"].(string)
			platform = strings.ToLower(strings.TrimSpace(platform))
			if platformOK && platform != "" {
				if name, ok := typed["service_name"].(string); ok {
					name = strings.ToLower(strings.TrimSpace(name))
					if name != "" {
						pairs = append(pairs, platform+":"+name)
					}
				}
				if evidenceMapLooksLikeService(typed) {
					if name, ok := typed["name"].(string); ok {
						name = strings.ToLower(strings.TrimSpace(name))
						if name != "" {
							pairs = append(pairs, platform+":"+name)
						}
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(pairs)
}

func evidenceMapLooksLikeService(value map[string]any) bool {
	if _, ok := value["platform"].(string); !ok {
		return false
	}
	if _, ok := value["name"].(string); !ok {
		return false
	}
	for _, key := range []string{"scope", "service_scope", "status", "service_type", "run_args"} {
		if _, ok := value[key]; ok {
			return true
		}
	}
	if artifacts, ok := value["artifacts"].(map[string]any); ok {
		if _, ok := artifacts["service_type"]; ok {
			return true
		}
	}
	return false
}

func verificationEvidenceAdvisorFieldStats(value any, field string) []string {
	values, _ := verificationEvidenceAdvisorFieldSets(value, field)
	return values
}

func verificationEvidencePlatformAdvisorFieldStats(value any, field string) []string {
	_, platformValues := verificationEvidenceAdvisorFieldSets(value, field)
	return platformValues
}

func verificationEvidenceAdvisorFieldSets(value any, field string) ([]string, []string) {
	field = strings.ToLower(strings.TrimSpace(field))
	values := []string{}
	platformValues := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if rawChecks, ok := typed["checks"].([]any); ok {
				containerPlatforms := evidenceCheckContainerPlatforms(typed, rawChecks)
				for _, rawCheck := range rawChecks {
					check, ok := rawCheck.(map[string]any)
					if !ok {
						continue
					}
					checkValues := advisorFieldValuesFromCheck(check, field)
					if len(checkValues) == 0 {
						continue
					}
					values = append(values, checkValues...)
					platforms := append([]string{}, containerPlatforms...)
					platforms = append(platforms, evidencePlatformsFromCheck(check)...)
					for _, platform := range normalizeStringSet(platforms) {
						for _, checkValue := range checkValues {
							checkValue = strings.ToLower(strings.TrimSpace(checkValue))
							if checkValue != "" {
								platformValues = append(platformValues, platform+":"+checkValue)
							}
						}
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(values), normalizeStringSet(platformValues)
}

func advisorFieldValuesFromCheck(check map[string]any, field string) []string {
	id, _ := check["id"].(string)
	status, _ := check["status"].(string)
	id = strings.ToLower(strings.TrimSpace(id))
	status = strings.ToLower(strings.TrimSpace(status))
	if !strings.HasPrefix(id, "s4.advisor.") || status != "ok" {
		return nil
	}
	detail, ok := check["detail"].(map[string]any)
	if !ok {
		return nil
	}
	values := advisorFieldValuesFromMap(detail, field)
	if actual, ok := detail["actual"].(string); ok && advisorExpectedCheckMatchesField(id, field) {
		values = append(values, actual)
	}
	return normalizeStringSet(values)
}

func advisorExpectedCheckMatchesField(checkID string, field string) bool {
	switch field {
	case "provider":
		return checkID == "s4.advisor.expected_provider"
	case "model":
		return checkID == "s4.advisor.expected_model"
	case "max_data_level":
		return checkID == "s4.advisor.expected_max_data_level"
	default:
		return false
	}
}

func advisorFieldValuesFromMap(value map[string]any, field string) []string {
	values := []string{}
	if direct, ok := value[field].(string); ok {
		values = append(values, direct)
	}
	for _, key := range []string{"advisor", "status"} {
		if nested, ok := value[key].(map[string]any); ok {
			values = append(values, advisorFieldValuesFromMap(nested, field)...)
		}
	}
	return normalizeStringSet(values)
}

func verificationEvidenceCheckPlatformStats(value any) ([]string, []string, []string) {
	all := []string{}
	passing := []string{}
	failing := []string{}
	var visit func(any)
	visit = func(current any) {
		switch typed := current.(type) {
		case map[string]any:
			if rawChecks, ok := typed["checks"].([]any); ok {
				platforms := evidenceCheckContainerPlatforms(typed, rawChecks)
				for _, rawCheck := range rawChecks {
					check, ok := rawCheck.(map[string]any)
					if !ok {
						continue
					}
					id, idOK := check["id"].(string)
					status, statusOK := check["status"].(string)
					id = strings.ToLower(strings.TrimSpace(id))
					status = strings.ToLower(strings.TrimSpace(status))
					if !idOK || !statusOK || id == "" {
						continue
					}
					checkPlatforms := append([]string{}, platforms...)
					checkPlatforms = append(checkPlatforms, evidencePlatformsFromCheck(check)...)
					for _, platform := range normalizeStringSet(checkPlatforms) {
						pair := id + ":" + platform
						all = append(all, pair)
						if status == "ok" {
							passing = append(passing, pair)
						} else if status != "" {
							failing = append(failing, pair)
						}
					}
				}
			}
			for _, item := range typed {
				visit(item)
			}
		case []any:
			for _, item := range typed {
				visit(item)
			}
		}
	}
	visit(value)
	return normalizeStringSet(all), normalizeStringSet(passing), normalizeStringSet(failing)
}

func evidenceCheckContainerPlatforms(container map[string]any, checks []any) []string {
	platforms := []string{}
	if platform, ok := container["platform"].(string); ok {
		platforms = append(platforms, platform)
	}
	if service, ok := container["service"].(map[string]any); ok {
		platforms = append(platforms, collectEvidenceStringValues(service, "platform")...)
	}
	if host, ok := container["host"].(map[string]any); ok {
		platforms = append(platforms, collectEvidenceStringValues(host, "platform")...)
	}
	for _, rawCheck := range checks {
		check, ok := rawCheck.(map[string]any)
		if !ok {
			continue
		}
		platforms = append(platforms, evidencePlatformsFromCheck(check)...)
	}
	return normalizeStringSet(platforms)
}

func evidencePlatformsFromCheck(check map[string]any) []string {
	platforms := collectEvidenceStringValues(check, "platform")
	id, _ := check["id"].(string)
	if strings.EqualFold(strings.TrimSpace(id), "steward.agent.expected_platform") {
		if detail, ok := check["detail"].(map[string]any); ok {
			if actual, ok := detail["actual"].(string); ok {
				platforms = append(platforms, actual)
			}
			if expected, ok := detail["expected"].(string); ok {
				platforms = append(platforms, expected)
			}
		}
	}
	return normalizeStringSet(platforms)
}

func withEvidenceKindPrefix(kind string, checkPlatforms []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(checkPlatforms))
	for _, checkPlatform := range checkPlatforms {
		check, platform, ok := splitCheckPlatformRequirement(checkPlatform)
		if !ok {
			continue
		}
		values = append(values, kind+":"+check+":"+platform)
	}
	return normalizeStringSet(values)
}

func withEvidenceKindPlatformServiceScopePrefix(kind string, platformScopes []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(platformScopes))
	for _, platformScope := range platformScopes {
		platform, scope, ok := splitPlatformServiceScopeRequirement(platformScope)
		if !ok {
			continue
		}
		values = append(values, kind+":"+platform+":"+scope)
	}
	return normalizeStringSet(values)
}

func withEvidenceKindPlatformServiceNamePrefix(kind string, platformNames []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(platformNames))
	for _, platformName := range platformNames {
		platform, name, ok := splitPlatformServiceNameRequirement(platformName)
		if !ok {
			continue
		}
		values = append(values, kind+":"+platform+":"+name)
	}
	return normalizeStringSet(values)
}

func withEvidenceKindPlatformAdvisorValuePrefix(kind string, platformValues []string) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "" {
		return nil
	}
	values := make([]string, 0, len(platformValues))
	for _, platformValue := range platformValues {
		platform, value, ok := splitPlatformAdvisorValueRequirement(platformValue)
		if !ok {
			continue
		}
		values = append(values, kind+":"+platform+":"+value)
	}
	return normalizeStringSet(values)
}

func sampleWatchSpan(samples []any) time.Duration {
	if len(samples) < 2 {
		return 0
	}
	first, ok := sampleTime(samples[0], "started_at")
	if !ok {
		return 0
	}
	last, ok := sampleTime(samples[len(samples)-1], "completed_at")
	if !ok {
		return 0
	}
	if last.Before(first) {
		return 0
	}
	return last.Sub(first)
}

func sampleTime(value any, key string) (time.Time, bool) {
	item, ok := value.(map[string]any)
	if !ok {
		return time.Time{}, false
	}
	raw, ok := item[key].(string)
	if !ok {
		return time.Time{}, false
	}
	parsed, err := time.Parse(time.RFC3339Nano, raw)
	return parsed, err == nil
}
