package servicecontrol

import (
	"fmt"
	"strconv"
	"strings"
)

func renderSystemdEnvironmentFile(env map[string]string) string {
	lines := make([]string, 0, len(env))
	for _, item := range envList(env) {
		key, value, _ := strings.Cut(item, "=")
		lines = append(lines, key+"="+strconv.Quote(value))
	}
	return strings.Join(lines, "\n") + "\n"
}

func parseSystemdEnvironmentFile(content string) (map[string]string, error) {
	env := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, ";") {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid systemd environment entry %q", line)
		}
		if err := validateEnvKey(key); err != nil {
			return nil, err
		}
		value = strings.TrimSpace(value)
		if strings.HasPrefix(value, `"`) {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("parse systemd environment value for %s: %w", key, err)
			}
			value = unquoted
		}
		env[key] = value
	}
	return env, nil
}

func parseInlineSystemdEnvironment(content string) (map[string]string, error) {
	env := map[string]string{}
	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "Environment=") {
			continue
		}
		value := strings.TrimSpace(strings.TrimPrefix(line, "Environment="))
		if strings.HasPrefix(value, `"`) {
			unquoted, err := strconv.Unquote(value)
			if err != nil {
				return nil, fmt.Errorf("parse systemd Environment entry %q: %w", line, err)
			}
			value = unquoted
		}
		key, envValue, ok := strings.Cut(value, "=")
		key = strings.TrimSpace(key)
		if !ok || key == "" {
			return nil, fmt.Errorf("invalid systemd Environment entry %q", line)
		}
		if err := validateEnvKey(key); err != nil {
			return nil, err
		}
		env[key] = envValue
	}
	if len(env) == 0 {
		return nil, fmt.Errorf("systemd unit does not contain an EnvironmentFile or legacy Environment entries")
	}
	return env, nil
}

func replaceSystemdEnvironmentDirectives(content string, envFilePath string) (string, error) {
	lines := strings.Split(content, "\n")
	next := make([]string, 0, len(lines)+1)
	inService := false
	inserted := false
	sawService := false
	directive := "EnvironmentFile=" + strconv.Quote(envFilePath)
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			if inService && !inserted {
				next = append(next, directive)
				inserted = true
			}
			inService = trimmed == "[Service]"
			if inService {
				sawService = true
			}
		}
		if inService && (strings.HasPrefix(trimmed, "Environment=") || strings.HasPrefix(trimmed, "EnvironmentFile=")) {
			continue
		}
		if inService && !inserted && strings.HasPrefix(trimmed, "ExecStart=") {
			next = append(next, directive)
			inserted = true
		}
		next = append(next, line)
	}
	if !sawService {
		return "", fmt.Errorf("systemd unit does not contain a [Service] section")
	}
	if inService && !inserted {
		next = append(next, directive)
	}
	return strings.Join(next, "\n"), nil
}
