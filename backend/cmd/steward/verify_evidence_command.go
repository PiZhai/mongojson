package main

import (
	"flag"
	"fmt"
	"strings"
)

func (c cli) verifyEvidence(args []string) error {
	fs := flag.NewFlagSet("steward verify evidence", flag.ExitOnError)
	options := evidenceManifestOptions{}
	var requireKinds stringListFlag
	var requirePlatforms stringListFlag
	var requireAgentIDs stringListFlag
	var requireKindPlatforms stringListFlag
	var requirePlatformAgents stringListFlag
	var requireKindPlatformAgents stringListFlag
	var requireServiceScopes stringListFlag
	var requirePlatformServiceScopes stringListFlag
	var requireKindPlatformServiceScopes stringListFlag
	var requireServiceNames stringListFlag
	var requirePlatformServiceNames stringListFlag
	var requireKindPlatformServiceNames stringListFlag
	var requireAdvisorProviders stringListFlag
	var requirePlatformAdvisorProviders stringListFlag
	var requireKindPlatformAdvisorProviders stringListFlag
	var requireAdvisorModels stringListFlag
	var requirePlatformAdvisorModels stringListFlag
	var requireKindPlatformAdvisorModels stringListFlag
	var requireAdvisorMaxDataLevels stringListFlag
	var requirePlatformAdvisorMaxDataLevels stringListFlag
	var requireKindPlatformAdvisorMaxDataLevels stringListFlag
	var requireChecks stringListFlag
	var requireCheckPlatforms stringListFlag
	var requireKindCheckPlatforms stringListFlag
	fs.StringVar(&options.Dir, "dir", "", "Directory containing steward verification evidence JSON files")
	fs.StringVar(&options.Output, "output", "", "Optional path to write the evidence manifest JSON")
	fs.StringVar(&options.Preset, "preset", "", "Apply a named coverage preset, for example s3s4-final or s3s4-final-system")
	fs.BoolVar(&options.RequirePassing, "require-passing", false, "Fail if any evidence file is failing or unreadable")
	fs.Var(&requireKinds, "require-kind", "Require at least one evidence file of this kind; repeat for runtime/service/peers/mesh/service-install/service-env")
	fs.Var(&requirePlatforms, "require-platform", "Require evidence mentioning this platform; repeat for windows/darwin/linux")
	fs.Var(&requireAgentIDs, "require-agent-id", "Require evidence mentioning this steward agent id; repeat as needed")
	fs.Var(&requireKindPlatforms, "require-kind-platform", "Require at least one evidence file whose kind and platform match KIND:PLATFORM; repeat as needed")
	fs.Var(&requirePlatformAgents, "require-platform-agent", "Require evidence mentioning this platform and agent id as PLATFORM:AGENT_ID; repeat as needed")
	fs.Var(&requireKindPlatformAgents, "require-kind-platform-agent", "Require evidence whose kind, platform, and agent id match KIND:PLATFORM:AGENT_ID; repeat as needed")
	fs.Var(&requireServiceScopes, "require-service-scope", "Require evidence mentioning this service manager scope; repeat for user/system")
	fs.Var(&requirePlatformServiceScopes, "require-platform-service-scope", "Require evidence mentioning this platform and service scope as PLATFORM:SCOPE; repeat as needed")
	fs.Var(&requireKindPlatformServiceScopes, "require-kind-platform-service-scope", "Require evidence whose kind, platform, and service scope match KIND:PLATFORM:SCOPE; repeat as needed")
	fs.Var(&requireServiceNames, "require-service-name", "Require evidence mentioning this service name; repeat as needed")
	fs.Var(&requirePlatformServiceNames, "require-platform-service-name", "Require evidence mentioning this platform and service name as PLATFORM:NAME; repeat as needed")
	fs.Var(&requireKindPlatformServiceNames, "require-kind-platform-service-name", "Require evidence whose kind, platform, and service name match KIND:PLATFORM:NAME; repeat as needed")
	fs.Var(&requireAdvisorProviders, "require-advisor-provider", "Require passing advisor evidence mentioning this provider; repeat as needed")
	fs.Var(&requirePlatformAdvisorProviders, "require-platform-advisor-provider", "Require passing advisor evidence mentioning this platform and provider as PLATFORM:PROVIDER; repeat as needed")
	fs.Var(&requireKindPlatformAdvisorProviders, "require-kind-platform-advisor-provider", "Require passing advisor evidence whose kind, platform, and provider match KIND:PLATFORM:PROVIDER; repeat as needed")
	fs.Var(&requireAdvisorModels, "require-advisor-model", "Require passing advisor evidence mentioning this model; repeat as needed")
	fs.Var(&requirePlatformAdvisorModels, "require-platform-advisor-model", "Require passing advisor evidence mentioning this platform and model as PLATFORM:MODEL; repeat as needed")
	fs.Var(&requireKindPlatformAdvisorModels, "require-kind-platform-advisor-model", "Require passing advisor evidence whose kind, platform, and model match KIND:PLATFORM:MODEL; repeat as needed")
	fs.Var(&requireAdvisorMaxDataLevels, "require-advisor-max-data-level", "Require passing advisor evidence mentioning this max data level; repeat as needed")
	fs.Var(&requirePlatformAdvisorMaxDataLevels, "require-platform-advisor-max-data-level", "Require passing advisor evidence mentioning this platform and max data level as PLATFORM:LEVEL; repeat as needed")
	fs.Var(&requireKindPlatformAdvisorMaxDataLevels, "require-kind-platform-advisor-max-data-level", "Require passing advisor evidence whose kind, platform, and max data level match KIND:PLATFORM:LEVEL; repeat as needed")
	fs.Var(&requireChecks, "require-check", "Require at least one passing verification check with this id; repeat as needed")
	fs.Var(&requireCheckPlatforms, "require-check-platform", "Require at least one passing verification check for a platform as CHECK:PLATFORM; repeat as needed")
	fs.Var(&requireKindCheckPlatforms, "require-kind-check-platform", "Require at least one passing verification check for an evidence kind and platform as KIND:CHECK:PLATFORM; repeat as needed")
	fs.BoolVar(&options.LatestPerKind, "latest-per-kind", false, "Only include the latest evidence file for each kind when evaluating coverage")
	fs.DurationVar(&options.MinWatchDuration, "min-watch-duration", 0, "Fail unless evidence covers at least this watch span")
	fs.BoolVar(&options.MinWatchDurationPerPlatform, "min-watch-duration-per-platform", false, "Apply --min-watch-duration to each required platform instead of only the best evidence file")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(options.Dir) == "" {
		return fmt.Errorf("verify evidence requires --dir")
	}
	options.RequireKinds = []string(requireKinds)
	options.RequirePlatforms = []string(requirePlatforms)
	options.RequireAgentIDs = []string(requireAgentIDs)
	options.RequireKindPlatforms = []string(requireKindPlatforms)
	options.RequirePlatformAgents = []string(requirePlatformAgents)
	options.RequireKindPlatformAgents = []string(requireKindPlatformAgents)
	options.RequireServiceScopes = []string(requireServiceScopes)
	options.RequirePlatformServiceScopes = []string(requirePlatformServiceScopes)
	options.RequireKindPlatformServiceScopes = []string(requireKindPlatformServiceScopes)
	options.RequireServiceNames = []string(requireServiceNames)
	options.RequirePlatformServiceNames = []string(requirePlatformServiceNames)
	options.RequireKindPlatformServiceNames = []string(requireKindPlatformServiceNames)
	options.RequireAdvisorProviders = []string(requireAdvisorProviders)
	options.RequirePlatformAdvisorProviders = []string(requirePlatformAdvisorProviders)
	options.RequireKindPlatformAdvisorProviders = []string(requireKindPlatformAdvisorProviders)
	options.RequireAdvisorModels = []string(requireAdvisorModels)
	options.RequirePlatformAdvisorModels = []string(requirePlatformAdvisorModels)
	options.RequireKindPlatformAdvisorModels = []string(requireKindPlatformAdvisorModels)
	options.RequireAdvisorMaxDataLevels = []string(requireAdvisorMaxDataLevels)
	options.RequirePlatformAdvisorMaxDataLevels = []string(requirePlatformAdvisorMaxDataLevels)
	options.RequireKindPlatformAdvisorMaxDataLevels = []string(requireKindPlatformAdvisorMaxDataLevels)
	options.RequireChecks = []string(requireChecks)
	options.RequireCheckPlatforms = []string(requireCheckPlatforms)
	options.RequireKindCheckPlatforms = []string(requireKindCheckPlatforms)
	var err error
	options, err = applyEvidenceManifestPreset(options)
	if err != nil {
		return err
	}
	if options.MinWatchDurationPerPlatform && options.MinWatchDuration <= 0 {
		return fmt.Errorf("verify evidence --min-watch-duration-per-platform requires --min-watch-duration")
	}

	manifest := buildVerificationEvidenceManifest(options)
	manifestPath, err := writeEvidenceManifest(options.Output, manifest)
	if err != nil {
		return err
	}
	payload := map[string]any{"manifest": manifest}
	if manifestPath != "" {
		payload["manifest_path"] = manifestPath
	}
	if err := printJSON(payload); err != nil {
		return err
	}
	if !manifest.OK {
		return fmt.Errorf("verification evidence manifest failed")
	}
	return nil
}
