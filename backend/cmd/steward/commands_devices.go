package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

func (c cli) devices(args []string) error {
	if len(args) > 0 && isHelpArg(args[0]) {
		printDevicesUsage()
		return nil
	}
	if len(args) == 0 {
		return c.printRequest(http.MethodGet, "/steward/devices", nil)
	}
	switch args[0] {
	case "list", "status":
		return c.printRequest(http.MethodGet, "/steward/devices", nil)
	case "register":
		return c.registerDevice(args[1:])
	case "revoke":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices revoke requires a device id")
		}
		return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/revoke", nil)
	case "permissions":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices permissions requires a device id")
		}
		return c.printRequest(http.MethodGet, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/permissions", nil)
	case "permission-set":
		return c.setDevicePermission(args[1:])
	case "verify":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices verify requires a device id")
		}
		return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/verify", nil)
	case "sync":
		if len(args) < 2 || strings.TrimSpace(args[1]) == "" {
			return fmt.Errorf("devices sync requires a device id")
		}
		return c.printRequest(http.MethodPost, "/steward/devices/"+url.PathEscape(strings.TrimSpace(args[1]))+"/sync", nil)
	default:
		return fmt.Errorf("unknown devices command %q", args[0])
	}
}

func (c cli) registerDevice(args []string) error {
	fs := flag.NewFlagSet("steward devices register", flag.ExitOnError)
	id := fs.String("id", "", "Peer device id")
	name := fs.String("name", "", "Peer device name")
	platform := fs.String("platform", "unknown", "Peer platform: windows, darwin, linux, or unknown")
	apiBaseURL := fs.String("api-base-url", "", "Peer Steward API base URL, for example http://192.168.1.12:18080/api")
	permissionLevel := fs.String("permission-level", "A3", "Default peer permission ceiling")
	publicKey := fs.String("public-key", "", "Peer Ed25519 public key from steward keygen")
	syncEnabled := fs.Bool("sync-enabled", true, "Whether the peer can participate in sync")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*id) == "" {
		return fmt.Errorf("devices register requires --id")
	}
	payload := map[string]any{
		"id":               strings.TrimSpace(*id),
		"device_name":      defaultString(*name, strings.TrimSpace(*id)),
		"platform":         strings.TrimSpace(*platform),
		"role":             "peer",
		"sync_enabled":     *syncEnabled,
		"permission_level": strings.TrimSpace(*permissionLevel),
		"public_key":       strings.TrimSpace(*publicKey),
		"api_base_url":     strings.TrimRight(strings.TrimSpace(*apiBaseURL), "/"),
	}
	return c.printRequest(http.MethodPost, "/steward/devices", payload)
}

type cliDevicePermission struct {
	DeviceID           string `json:"device_id"`
	Capability         string `json:"capability"`
	Policy             string `json:"policy"`
	MaxPermissionLevel string `json:"max_permission_level"`
	ScopeSummary       string `json:"scope_summary"`
}

func (c cli) setDevicePermission(args []string) error {
	if len(args) < 3 {
		return fmt.Errorf("devices permission-set requires a device id, capability, and allow, confirm, or deny")
	}
	deviceID := strings.TrimSpace(args[0])
	capability := strings.TrimSpace(args[1])
	policy := strings.TrimSpace(args[2])
	if deviceID == "" || capability == "" {
		return fmt.Errorf("devices permission-set requires a device id and capability")
	}
	if !validCLIDevicePermissionPolicy(policy) {
		return fmt.Errorf("unsupported device permission policy %q", policy)
	}
	current, found, err := c.findDevicePermission(deviceID, capability)
	if err != nil {
		return err
	}
	maxPermissionLevel := ""
	if len(args) >= 4 {
		maxPermissionLevel = strings.TrimSpace(args[3])
	}
	if maxPermissionLevel == "" {
		if found {
			maxPermissionLevel = current.MaxPermissionLevel
		} else {
			maxPermissionLevel = "A3"
		}
	}
	if !validCLIPermissionLevel(maxPermissionLevel) {
		return fmt.Errorf("invalid max permission level %q", maxPermissionLevel)
	}
	payload := map[string]any{
		"policy":               policy,
		"max_permission_level": maxPermissionLevel,
	}
	if found && strings.TrimSpace(current.ScopeSummary) != "" {
		payload["scope_summary"] = current.ScopeSummary
	}
	return c.printRequest(
		http.MethodPut,
		"/steward/devices/"+url.PathEscape(deviceID)+"/permissions/"+url.PathEscape(capability),
		payload,
	)
}

func (c cli) findDevicePermission(deviceID string, capability string) (cliDevicePermission, bool, error) {
	body, err := c.request(http.MethodGet, "/steward/devices/"+url.PathEscape(deviceID)+"/permissions", nil)
	if err != nil {
		return cliDevicePermission{}, false, err
	}
	var response struct {
		Permissions []cliDevicePermission `json:"permissions"`
	}
	if err := json.Unmarshal(body, &response); err != nil {
		return cliDevicePermission{}, false, fmt.Errorf("decode device permissions: %w", err)
	}
	for _, permission := range response.Permissions {
		if permission.Capability == capability {
			return permission, true, nil
		}
	}
	return cliDevicePermission{}, false, nil
}

func validCLIDevicePermissionPolicy(policy string) bool {
	switch strings.TrimSpace(policy) {
	case "allow", "confirm", "deny":
		return true
	default:
		return false
	}
}

func validCLIPermissionLevel(value string) bool {
	value = strings.ToUpper(strings.TrimSpace(value))
	return len(value) == 2 && value[0] == 'A' && value[1] >= '0' && value[1] <= '9'
}

func printDevicesUsage() {
	fmt.Fprintln(stdout, `usage: steward devices <list|register|revoke|permissions|permission-set|verify|sync> [args]

device commands:
  list
  register --id <id> --name <name> --platform <windows|darwin|linux> --api-base-url <url> --public-key <key>
  revoke <id>
  permissions <id>
  permission-set <id> <capability> <allow|confirm|deny> [A0-A9]
  verify <id>
  sync <id>

notes:
  management APIs should stay on loopback; peer APIs should use the restricted sync surface only.`)
}
