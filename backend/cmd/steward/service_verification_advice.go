package main

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"runtime"
	"strings"
	"time"

	"mongojson/backend/internal/platform/servicecontrol"
)

type servicePostVerifyOptions struct {
	Verify                 bool
	StartupTimeout         time.Duration
	WatchDuration          time.Duration
	WatchInterval          time.Duration
	AdvisorProbe           bool
	AdvisorProbeEachSample bool
	AdvisorPrivacyProbe    bool
	EvidenceDir            string
}

type serviceEnvVerificationAdvice struct {
	ServiceName    string   `json:"service_name"`
	ServiceScope   string   `json:"service_scope"`
	APIBase        string   `json:"api_base"`
	RuntimeArgs    []string `json:"runtime_args"`
	RuntimeCommand string   `json:"runtime_command"`
	ServiceArgs    []string `json:"service_args"`
	ServiceCommand string   `json:"service_command"`
	WatchArgs      []string `json:"watch_args"`
	WatchCommand   string   `json:"watch_command"`
	Notes          []string `json:"notes,omitempty"`
}

func serviceEnvVerificationAdviceFromEnvironment(serviceName string, env map[string]string) *serviceEnvVerificationAdvice {
	return serviceEnvVerificationAdviceFromEnvironmentForPlatform(serviceName, servicecontrol.DefaultScope(), env, runtime.GOOS)
}

func serviceEnvVerificationAdviceFromEnvironmentForPlatform(serviceName string, scope string, env map[string]string, platform string) *serviceEnvVerificationAdvice {
	if len(env) == 0 {
		return nil
	}
	serviceName = defaultString(strings.TrimSpace(serviceName), servicecontrol.DefaultName())
	scope, err := servicecontrol.NormalizeScopeForPlatform(platform, scope)
	if err != nil {
		scope = servicecontrol.DefaultScopeForPlatform(platform)
	}
	apiBase := managementAPIBaseFromHTTPAddr(env["HTTP_ADDR"])
	expectArgs := serviceEnvExpectedVerifyArgs(env, platform)

	runtimeArgs := append([]string{"steward", "--api", apiBase, "verify", "runtime", "--strict-security"}, expectArgs...)
	serviceArgs := append([]string{"steward", "--api", apiBase, "verify", "service", "--name", serviceName, "--scope", scope, "--strict-security"}, expectArgs...)
	watchArgs := append([]string{"steward", "--api", apiBase, "verify", "service", "--name", serviceName, "--scope", scope, "--strict-security", "--watch-duration", "24h", "--watch-interval", "5m"}, expectArgs...)

	return &serviceEnvVerificationAdvice{
		ServiceName:    serviceName,
		ServiceScope:   scope,
		APIBase:        apiBase,
		RuntimeArgs:    runtimeArgs,
		RuntimeCommand: displayCommand(runtimeArgs),
		ServiceArgs:    serviceArgs,
		ServiceCommand: displayCommand(serviceArgs),
		WatchArgs:      watchArgs,
		WatchCommand:   displayCommand(watchArgs),
		Notes: []string{
			"Run these commands after applying environment changes and restarting the service.",
			"The watch command requires at least two samples and proves the service heartbeat advances.",
		},
	}
}

func serviceEnvExpectedVerifyArgs(env map[string]string, platform string) []string {
	args := []string{}
	add := func(flag string, value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		args = append(args, flag, value)
	}

	add("--expect-agent-id", visibleServiceEnvValue(env, "STEWARD_AGENT_ID"))
	add("--expect-agent-platform", defaultString(platform, runtime.GOOS))
	add("--expect-sync-key-id", visibleServiceEnvValue(env, "STEWARD_SYNC_ENCRYPTION_KEY_ID"))
	add("--expect-local-key-id", visibleServiceEnvValue(env, "STEWARD_LOCAL_ENCRYPTION_KEY_ID"))

	provider := normalizedAdvisorExpectationProvider(visibleServiceEnvValue(env, "STEWARD_LLM_PROVIDER"))
	model := visibleServiceEnvValue(env, "STEWARD_LLM_MODEL")
	if provider != "" && model != "" {
		add("--expect-advisor-provider", provider)
		add("--expect-advisor-model", model)
		add("--expect-advisor-max-data-level", defaultString(visibleServiceEnvValue(env, "STEWARD_LLM_MAX_DATA_LEVEL"), "D1"))
	}
	return args
}

func serviceVerifyOptionsFromEnvironment(serviceName string, scope string, env map[string]string, postVerify servicePostVerifyOptions) (string, serviceVerifyOptions) {
	apiBase := managementAPIBaseFromHTTPAddr(env["HTTP_ADDR"])
	serviceName = defaultString(strings.TrimSpace(serviceName), servicecontrol.DefaultName())
	scope, err := servicecontrol.NormalizeScopeForPlatform(runtime.GOOS, scope)
	if err != nil {
		scope = servicecontrol.DefaultScope()
	}
	watchInterval := postVerify.WatchInterval
	if watchInterval <= 0 {
		watchInterval = time.Minute
	}
	runtimeOptions := runtimeVerifyOptions{
		StrictSecurity:              true,
		AdvisorProbe:                postVerify.AdvisorProbe,
		AdvisorProbeEachSample:      postVerify.AdvisorProbeEachSample,
		AdvisorPrivacyProbe:         postVerify.AdvisorPrivacyProbe,
		ExpectAgentID:               visibleServiceEnvValue(env, "STEWARD_AGENT_ID"),
		ExpectAgentPlatform:         runtime.GOOS,
		ExpectSyncKeyID:             visibleServiceEnvValue(env, "STEWARD_SYNC_ENCRYPTION_KEY_ID"),
		ExpectLocalKeyID:            visibleServiceEnvValue(env, "STEWARD_LOCAL_ENCRYPTION_KEY_ID"),
		ExpectAdvisorProvider:       normalizedAdvisorExpectationProvider(visibleServiceEnvValue(env, "STEWARD_LLM_PROVIDER")),
		ExpectAdvisorModel:          "",
		ExpectAdvisorMaxDataLevel:   "",
		ExpectSyncPreviousKeyCount:  nil,
		ExpectLocalPreviousKeyCount: nil,
	}
	if runtimeOptions.ExpectAdvisorProvider != "" {
		runtimeOptions.ExpectAdvisorModel = visibleServiceEnvValue(env, "STEWARD_LLM_MODEL")
		if runtimeOptions.ExpectAdvisorModel != "" {
			runtimeOptions.ExpectAdvisorMaxDataLevel = defaultString(visibleServiceEnvValue(env, "STEWARD_LLM_MAX_DATA_LEVEL"), "D1")
		} else {
			runtimeOptions.ExpectAdvisorProvider = ""
		}
	}
	return apiBase, serviceVerifyOptions{
		Name:           serviceName,
		Scope:          scope,
		StrictSecurity: true,
		WatchDuration:  postVerify.WatchDuration,
		WatchInterval:  watchInterval,
		Runtime:        runtimeOptions,
	}
}

func runServiceVerificationForEnvironment(serviceName string, scope string, env map[string]string, managementToken string, postVerify servicePostVerifyOptions) serviceVerificationResult {
	apiBase, opts := serviceVerifyOptionsFromEnvironment(serviceName, scope, env, postVerify)
	verifier := cli{
		apiBase:         strings.TrimRight(apiBase, "/"),
		client:          &http.Client{Timeout: 5 * time.Second},
		managementToken: strings.TrimSpace(managementToken),
	}
	postVerify = normalizeServicePostVerifyOptions(postVerify)
	if postVerify.StartupTimeout > 0 {
		readyOptions := opts
		readyOptions.WatchDuration = 0
		readyOptions.WatchInterval = 0
		readyResult := waitForServiceVerification(verifier, readyOptions, postVerify.StartupTimeout)
		if !readyResult.OK || opts.WatchDuration <= 0 {
			return readyResult
		}
	}
	return verifier.runServiceVerification(opts)
}

func normalizeServicePostVerifyOptions(input servicePostVerifyOptions) servicePostVerifyOptions {
	out := input
	if out.StartupTimeout < 0 {
		out.StartupTimeout = 0
	}
	if out.StartupTimeout == 0 {
		out.StartupTimeout = 30 * time.Second
	}
	if out.WatchInterval <= 0 {
		out.WatchInterval = time.Minute
	}
	return out
}

func validateServicePostVerifyOptions(command string, opts servicePostVerifyOptions) error {
	if opts.WatchDuration > 0 && !opts.Verify {
		return fmt.Errorf("%s --verify-watch-duration requires --verify", command)
	}
	if opts.AdvisorProbe && !opts.Verify {
		return fmt.Errorf("%s --verify-advisor-probe requires --verify", command)
	}
	if opts.AdvisorPrivacyProbe && !opts.Verify {
		return fmt.Errorf("%s --verify-advisor-privacy-probe requires --verify", command)
	}
	if opts.AdvisorProbeEachSample && !opts.Verify {
		return fmt.Errorf("%s --verify-advisor-probe-each-sample requires --verify", command)
	}
	if opts.AdvisorProbeEachSample && !opts.AdvisorProbe {
		return fmt.Errorf("%s --verify-advisor-probe-each-sample requires --verify-advisor-probe", command)
	}
	if opts.AdvisorProbeEachSample && opts.WatchDuration <= 0 {
		return fmt.Errorf("%s --verify-advisor-probe-each-sample requires --verify-watch-duration", command)
	}
	if strings.TrimSpace(opts.EvidenceDir) != "" && !opts.Verify {
		return fmt.Errorf("%s --verify-evidence-dir requires --verify", command)
	}
	return nil
}

func writeServicePostVerificationEvidence(kind string, opts servicePostVerifyOptions, result serviceVerificationResult) (string, error) {
	if strings.TrimSpace(opts.EvidenceDir) == "" {
		return "", nil
	}
	return writeVerificationEvidence(kind, opts.EvidenceDir, map[string]any{"verification": result}, result.OK)
}

func waitForServiceVerification(verifier cli, opts serviceVerifyOptions, timeout time.Duration) serviceVerificationResult {
	deadline := time.Now().Add(timeout)
	var result serviceVerificationResult
	for {
		result = verifier.runServiceVerification(opts)
		if result.OK {
			return result
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			result.Checks = append(result.Checks, runtimeVerificationCheck{
				ID:      "service.verify.startup",
				Status:  "error",
				Message: "service verification did not pass before startup timeout",
				Detail:  map[string]string{"timeout": timeout.String()},
			})
			result.OK = false
			return result
		}
		sleepFor := time.Second
		if remaining < sleepFor {
			sleepFor = remaining
		}
		time.Sleep(sleepFor)
	}
}

func visibleServiceEnvValue(env map[string]string, key string) string {
	value := strings.TrimSpace(env[key])
	if value == "" || value == "<redacted>" {
		return ""
	}
	return value
}

func normalizedAdvisorExpectationProvider(provider string) string {
	switch strings.ToLower(strings.TrimSpace(provider)) {
	case "", "off", "disabled", "none":
		return ""
	case "openai", "openai-compatible":
		return "openai-compatible"
	default:
		return ""
	}
}

func managementAPIBaseFromHTTPAddr(addr string) string {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return defaultAPIBase
	}
	if strings.Contains(addr, "://") {
		parsed, err := url.Parse(addr)
		if err == nil && parsed.Scheme != "" && parsed.Host != "" {
			parsed.Path = "/api"
			parsed.RawQuery = ""
			parsed.Fragment = ""
			return strings.TrimRight(parsed.String(), "/")
		}
		return defaultAPIBase
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		if strings.HasPrefix(addr, ":") && len(addr) > 1 {
			host, port = "127.0.0.1", strings.TrimPrefix(addr, ":")
		} else {
			return defaultAPIBase
		}
	}
	host = serviceVerificationHost(host)
	if strings.TrimSpace(port) == "" {
		return defaultAPIBase
	}
	return "http://" + net.JoinHostPort(host, port) + "/api"
}

func serviceVerificationHost(host string) string {
	host = strings.Trim(strings.TrimSpace(host), "[]")
	switch strings.ToLower(host) {
	case "", "0.0.0.0", "::", "*":
		return "127.0.0.1"
	default:
		return host
	}
}

func displayCommand(args []string) string {
	parts := make([]string, 0, len(args))
	for _, arg := range args {
		parts = append(parts, displayCommandArg(arg))
	}
	return strings.Join(parts, " ")
}

func displayCommandArg(arg string) string {
	if arg == "" {
		return `""`
	}
	if strings.IndexFunc(arg, func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n' || r == '"' || r == '\''
	}) < 0 {
		return arg
	}
	return `"` + strings.ReplaceAll(arg, `"`, `\"`) + `"`
}
