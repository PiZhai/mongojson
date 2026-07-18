# R5.1 Windows production installation and privilege isolation

R5.1 separates the local Steward into three Windows identities:

- `MongojsonSteward` runs as `NT AUTHORITY\LocalService` with a restricted service SID.
- `MongojsonStewardBroker` remains `LocalSystem`; only hash-pinned Broker capabilities with Broker-validated parameter schemas may cross the privilege boundary.
- `MongojsonStewardCompanion` runs as the current interactive user with `RunLevel=Limited`.

The main service stores public configuration in the SCM service environment and sensitive values in `C:\ProgramData\MongojsonSteward\config\service-secrets.json`. The private file DACL permits SYSTEM, Administrators, and read access for the exact service SID. Service environment updates preserve this split and refuse to put new secrets in SCM when no private file is configured.

The Companion named pipe no longer grants access to the broad Interactive Users group. Its DACL contains only SYSTEM, the current Companion user SID, and `NT SERVICE\MongojsonSteward`. Requests still require the HMAC key shared through independently protected private files.

When `STEWARD_RESTRICTED_SERVICE=true`, a package declared with `execution_target=system` is routed to `steward-system-tool-host` through the Broker. The Broker validates the native tool input schema, binds the canonical input SHA-256 into the request, audit and signed receipt, verifies the host executable hash, and places the process tree under its Watchdog/Job Object. The main process never falls back to spawning a system package itself. Session tools continue to route through the Companion. Legacy fixed capabilities remain available through `privilege.execute`, but native parameterized tools do not use the old A-level taxonomy.

The capability child keeps the Broker's LocalSystem SID enabled so the Windows loader can initialize system DLLs, but runs with optional privileges removed and an additional restricting-SID check. Ordinary system DLLs pass through the built-in Users SID; only the immutable System Tool Host is granted the Restricted Code SID. Broker policy, keys, state, audit and checkpoints grant neither restricting SID, so capability children cannot read the Broker trust domain.

## Release contents

The Windows release contains the main service, Broker, approval helper, Companion and compiled System Tool Host, bundled UI, production install/update/migrate/uninstall/verification/key-rotation scripts, and Companion lifecycle scripts. All are covered by the release manifest and SHA-256 checksum file.

## Installation

For a clean machine, follow the step-by-step [full Windows production deployment guide](windows-fresh-production-deployment.md). This document describes the isolation contract; it is not a replacement for the full host, Docker PostgreSQL, model configuration, cold-start and reboot-acceptance procedure.

Prepare PostgreSQL on a loopback-only address. The installer generates a protected policy, Broker request key, independent resume key, signing identity and separate approval key automatically:

```powershell
.\install-steward-production.ps1 `
  -SourceDir . `
  -DatabaseURL 'postgres://steward_app:...@127.0.0.1:55439/mongojson?sslmode=disable' `
  -InstallCompanion -Start -Verify
```

The installer rolls back services and the Companion task created by that invocation when a later step fails. Existing services are never overwritten by install; use the update script.

Run the verifier independently after Windows restart:

```powershell
.\test-steward-production.ps1 -RequireCompanion
```

It checks the main account, restricted SID, Broker account, loopback listeners, private-environment split, absence of secrets in SCM, System Tool Host catalog, parameterized policy and a real `system.uptime` execution with a signed Broker receipt.

## Update and uninstall

```powershell
.\update-steward-production.ps1 -SourceDir .
.\migrate-steward-production.ps1 -SourceDir . -InstallCompanion -Verify
.\rotate-steward-broker-keys.ps1
.\uninstall-steward-production.ps1
```

Update refuses a host whose main service is not LocalService or whose Broker is not LocalSystem. It backs up the current main and Broker binaries, atomically refreshes the parameterized policy for the new System Tool Host hash, restores them if verification fails, and preserves keys, audit, state, database, evidence and private environment files. Migration converts an existing LocalSystem main service and retains a rollback copy. It preserves local encryption identities and, only when the legacy service provides an API key, enables a one-start recovery that re-encrypts an otherwise undecryptable persisted model secret without changing the other model settings. The installer disables that recovery marker after the started service has consumed it, so subsequent restarts remain fail-closed. Key rotation changes the main/Broker request key and independent resume key while retaining the signing identity so checkpoint and audit continuity are not broken.

Uninstall preserves `C:\ProgramData\MongojsonSteward` unless `-RemoveData` is explicitly supplied. Companion user data is similarly retained unless `-RemoveCompanionData` is supplied.
