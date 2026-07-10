package main

import (
	"fmt"
	"strings"
	"time"
)

func addEvidenceManifestChecks(manifest *verificationEvidenceManifest) {
	if manifest.Coverage.TotalFiles == 0 {
		addEvidenceManifestCheck(manifest, "evidence.files", "error", "no steward verification evidence files were found", nil)
		return
	}
	addEvidenceManifestCheck(manifest, "evidence.files", "ok", fmt.Sprintf("%d evidence files found", manifest.Coverage.TotalFiles), map[string]int{"files": manifest.Coverage.TotalFiles})

	if manifest.Options.RequirePassing {
		if manifest.Coverage.FailingFiles > 0 {
			addEvidenceManifestCheck(manifest, "evidence.passing", "error", "one or more evidence files are failing or unreadable", map[string]int{"failing_files": manifest.Coverage.FailingFiles})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.passing", "ok", "all evidence files are passing", map[string]int{"passing_files": manifest.Coverage.PassingFiles})
		}
	}
	for _, kind := range manifest.Options.RequireKinds {
		if stringSetContains(manifest.Coverage.Kinds, kind) {
			addEvidenceManifestCheck(manifest, "evidence.kind."+kind, "ok", "required evidence kind is present", map[string]string{"kind": kind})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.kind."+kind, "error", "required evidence kind is missing", map[string]string{"kind": kind})
		}
	}
	for _, platform := range manifest.Options.RequirePlatforms {
		if stringSetContains(manifest.Coverage.Platforms, platform) {
			addEvidenceManifestCheck(manifest, "evidence.platform."+platform, "ok", "required platform is present", map[string]string{"platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.platform."+platform, "error", "required platform is missing", map[string]string{"platform": platform})
		}
	}
	for _, agentID := range manifest.Options.RequireAgentIDs {
		checkID := "evidence.agent." + sanitizeEvidenceName(agentID)
		if stringSetContains(manifest.Coverage.AgentIDs, agentID) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required agent id is present", map[string]string{"agent_id": agentID})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required agent id is missing", map[string]string{"agent_id": agentID})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatforms {
		kind, platform, ok := splitKindPlatformRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform.format", "error", "required kind/platform must use KIND:PLATFORM", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform." + kind + "." + platform
		if stringSetContains(manifest.Coverage.KindPlatforms, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required evidence kind/platform is present", map[string]string{"kind": kind, "platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required evidence kind/platform is missing", map[string]string{"kind": kind, "platform": platform})
		}
	}
	for _, requirement := range manifest.Options.RequirePlatformAgents {
		platform, agentID, ok := splitPlatformAgentRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_agent.format", "error", "required platform/agent must use PLATFORM:AGENT_ID", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_agent." + platform + "." + sanitizeEvidenceName(agentID)
		if stringSetContains(manifest.Coverage.PlatformAgents, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required agent id is present for platform", map[string]string{"platform": platform, "agent_id": agentID})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required agent id is missing for platform", map[string]string{"platform": platform, "agent_id": agentID})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatformAgents {
		kind, platform, agentID, ok := splitKindPlatformAgentRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_agent.format", "error", "required kind/platform/agent must use KIND:PLATFORM:AGENT_ID", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_agent." + kind + "." + platform + "." + sanitizeEvidenceName(agentID)
		if stringSetContains(manifest.Coverage.KindPlatformAgents, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required agent id is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "agent_id": agentID})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required agent id is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "agent_id": agentID})
		}
	}
	for _, scope := range manifest.Options.RequireServiceScopes {
		checkID := "evidence.service_scope." + sanitizeEvidenceName(scope)
		if stringSetContains(manifest.Coverage.ServiceScopes, scope) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service scope is present", map[string]string{"scope": scope})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service scope is missing", map[string]string{"scope": scope})
		}
	}
	for _, requirement := range manifest.Options.RequirePlatformServiceScopes {
		platform, scope, ok := splitPlatformServiceScopeRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_service_scope.format", "error", "required platform/service-scope must use PLATFORM:SCOPE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_service_scope." + platform + "." + sanitizeEvidenceName(scope)
		if stringSetContains(manifest.Coverage.PlatformServiceScopes, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service scope is present for platform", map[string]string{"platform": platform, "scope": scope})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service scope is missing for platform", map[string]string{"platform": platform, "scope": scope})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatformServiceScopes {
		kind, platform, scope, ok := splitKindPlatformServiceScopeRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_service_scope.format", "error", "required kind/platform/service-scope must use KIND:PLATFORM:SCOPE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_service_scope." + kind + "." + platform + "." + sanitizeEvidenceName(scope)
		if stringSetContains(manifest.Coverage.KindPlatformServiceScopes, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service scope is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "scope": scope})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service scope is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "scope": scope})
		}
	}
	for _, name := range manifest.Options.RequireServiceNames {
		checkID := "evidence.service_name." + sanitizeEvidenceName(name)
		if stringSetContains(manifest.Coverage.ServiceNames, name) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service name is present", map[string]string{"service_name": name})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service name is missing", map[string]string{"service_name": name})
		}
	}
	for _, requirement := range manifest.Options.RequirePlatformServiceNames {
		platform, name, ok := splitPlatformServiceNameRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_service_name.format", "error", "required platform/service-name must use PLATFORM:NAME", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_service_name." + platform + "." + sanitizeEvidenceName(name)
		if stringSetContains(manifest.Coverage.PlatformServiceNames, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service name is present for platform", map[string]string{"platform": platform, "service_name": name})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service name is missing for platform", map[string]string{"platform": platform, "service_name": name})
		}
	}
	for _, requirement := range manifest.Options.RequireKindPlatformServiceNames {
		kind, platform, name, ok := splitKindPlatformServiceNameRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_service_name.format", "error", "required kind/platform/service-name must use KIND:PLATFORM:NAME", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_service_name." + kind + "." + platform + "." + sanitizeEvidenceName(name)
		if stringSetContains(manifest.Coverage.KindPlatformServiceNames, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required service name is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "service_name": name})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required service name is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, "service_name": name})
		}
	}
	addAdvisorEvidenceManifestChecks(manifest, "provider", manifest.Options.RequireAdvisorProviders, manifest.Options.RequirePlatformAdvisorProviders, manifest.Options.RequireKindPlatformAdvisorProviders, manifest.Coverage.AdvisorProviders, manifest.Coverage.PlatformAdvisorProviders, manifest.Coverage.KindPlatformAdvisorProviders)
	addAdvisorEvidenceManifestChecks(manifest, "model", manifest.Options.RequireAdvisorModels, manifest.Options.RequirePlatformAdvisorModels, manifest.Options.RequireKindPlatformAdvisorModels, manifest.Coverage.AdvisorModels, manifest.Coverage.PlatformAdvisorModels, manifest.Coverage.KindPlatformAdvisorModels)
	addAdvisorEvidenceManifestChecks(manifest, "max_data_level", manifest.Options.RequireAdvisorMaxDataLevels, manifest.Options.RequirePlatformAdvisorMaxDataLevels, manifest.Options.RequireKindPlatformAdvisorMaxDataLevels, manifest.Coverage.AdvisorMaxDataLevels, manifest.Coverage.PlatformAdvisorMaxDataLevels, manifest.Coverage.KindPlatformAdvisorMaxDataLevels)
	for _, check := range manifest.Options.RequireChecks {
		if stringSetContains(manifest.Coverage.PassingChecks, check) {
			addEvidenceManifestCheck(manifest, "evidence.check."+sanitizeEvidenceName(check), "ok", "required passing check is present", map[string]string{"check": check})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.check."+sanitizeEvidenceName(check), "error", "required passing check is missing", map[string]string{"check": check})
		}
	}
	for _, requirement := range manifest.Options.RequireCheckPlatforms {
		check, platform, ok := splitCheckPlatformRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.check_platform.format", "error", "required check/platform must use CHECK:PLATFORM", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.check_platform." + sanitizeEvidenceName(check) + "." + platform
		if stringSetContains(manifest.Coverage.PassingCheckPlatforms, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required passing check is present for platform", map[string]string{"check": check, "platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required passing check is missing for platform", map[string]string{"check": check, "platform": platform})
		}
	}
	for _, requirement := range manifest.Options.RequireKindCheckPlatforms {
		kind, check, platform, ok := splitKindCheckPlatformRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_check_platform.format", "error", "required kind/check/platform must use KIND:CHECK:PLATFORM", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_check_platform." + kind + "." + sanitizeEvidenceName(check) + "." + platform
		if stringSetContains(manifest.Coverage.PassingKindCheckPlatforms, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required passing check is present for evidence kind and platform", map[string]string{"kind": kind, "check": check, "platform": platform})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required passing check is missing for evidence kind and platform", map[string]string{"kind": kind, "check": check, "platform": platform})
		}
	}
	if manifest.Options.MinWatchDuration > 0 {
		if time.Duration(manifest.Coverage.MaxWatchSpanMillis)*time.Millisecond >= manifest.Options.MinWatchDuration {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration", "ok", "minimum watch duration is covered", map[string]string{"min": manifest.Options.MinWatchDuration.String(), "max": manifest.Coverage.MaxWatchSpan})
		} else {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration", "error", "minimum watch duration is not covered", map[string]string{"min": manifest.Options.MinWatchDuration.String(), "max": manifest.Coverage.MaxWatchSpan})
		}
		if manifest.Options.MinWatchDurationPerPlatform {
			addPerPlatformWatchDurationChecks(manifest)
		}
	}
}

func addPerPlatformWatchDurationChecks(manifest *verificationEvidenceManifest) {
	targets := manifest.Options.RequirePlatforms
	if len(targets) == 0 {
		targets = manifest.Coverage.Platforms
	}
	if len(targets) == 0 {
		addEvidenceManifestCheck(manifest, "evidence.watch_duration.platforms", "error", "no platforms are available for per-platform watch duration checks", nil)
		return
	}
	for _, platform := range targets {
		maxMillis := manifest.Coverage.PlatformMaxWatchSpanMillis[platform]
		maxSpan := time.Duration(maxMillis) * time.Millisecond
		detail := map[string]string{
			"platform": platform,
			"min":      manifest.Options.MinWatchDuration.String(),
			"max":      maxSpan.String(),
		}
		if maxSpan >= manifest.Options.MinWatchDuration {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration."+platform, "ok", "minimum watch duration is covered for platform", detail)
		} else {
			addEvidenceManifestCheck(manifest, "evidence.watch_duration."+platform, "error", "minimum watch duration is not covered for platform", detail)
		}
	}
}

func addAdvisorEvidenceManifestChecks(manifest *verificationEvidenceManifest, field string, values []string, platformValues []string, kindPlatformValues []string, coverageValues []string, coveragePlatformValues []string, coverageKindPlatformValues []string) {
	field = strings.ToLower(strings.TrimSpace(field))
	if field == "" {
		return
	}
	label := strings.ReplaceAll(field, "_", " ")
	checkField := sanitizeEvidenceName(field)
	for _, value := range values {
		checkID := "evidence.advisor_" + checkField + "." + sanitizeEvidenceName(value)
		if stringSetContains(coverageValues, value) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required advisor "+label+" is present", map[string]string{field: value})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required advisor "+label+" is missing", map[string]string{field: value})
		}
	}
	for _, requirement := range platformValues {
		platform, value, ok := splitPlatformAdvisorValueRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.platform_advisor_"+checkField+".format", "error", "required platform/advisor-"+field+" must use PLATFORM:VALUE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.platform_advisor_" + checkField + "." + platform + "." + sanitizeEvidenceName(value)
		if stringSetContains(coveragePlatformValues, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required advisor "+label+" is present for platform", map[string]string{"platform": platform, field: value})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required advisor "+label+" is missing for platform", map[string]string{"platform": platform, field: value})
		}
	}
	for _, requirement := range kindPlatformValues {
		kind, platform, value, ok := splitKindPlatformAdvisorValueRequirement(requirement)
		if !ok {
			addEvidenceManifestCheck(manifest, "evidence.kind_platform_advisor_"+checkField+".format", "error", "required kind/platform/advisor-"+field+" must use KIND:PLATFORM:VALUE", map[string]string{"requirement": requirement})
			continue
		}
		checkID := "evidence.kind_platform_advisor_" + checkField + "." + kind + "." + platform + "." + sanitizeEvidenceName(value)
		if stringSetContains(coverageKindPlatformValues, requirement) {
			addEvidenceManifestCheck(manifest, checkID, "ok", "required advisor "+label+" is present for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, field: value})
		} else {
			addEvidenceManifestCheck(manifest, checkID, "error", "required advisor "+label+" is missing for evidence kind and platform", map[string]string{"kind": kind, "platform": platform, field: value})
		}
	}
}

func addEvidenceManifestCheck(manifest *verificationEvidenceManifest, id string, status string, message string, detail any) {
	if status != "ok" {
		manifest.OK = false
	}
	manifest.Checks = append(manifest.Checks, runtimeVerificationCheck{ID: id, Status: status, Message: message, Detail: detail})
}
