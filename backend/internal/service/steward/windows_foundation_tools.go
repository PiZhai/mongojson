package steward

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

type windowsFoundationToolDefinition struct {
	name, description, target, sideEffect string
	required                              []string
	properties                            map[string]any
}

func (s *Service) ensureWindowsFoundationTools(ctx context.Context, now time.Time) error {
	if runtime.GOOS != "windows" || !s.runtimeR2 {
		return nil
	}
	for _, definition := range windowsFoundationToolDefinitions() {
		if _, exists := s.runtimeTools.get(definition.name); exists {
			continue
		}
		manifest := ToolPackageManifest{
			Name: definition.name, Version: "1.0.0", Title: definition.name,
			Description: definition.description, Origin: "platform", Runtime: toolRuntimePowerShell,
			ExecutionTarget: definition.target, Entrypoint: "tool.ps1",
			InputSchema:  map[string]any{"type": "object", "properties": definition.properties, "required": definition.required, "additionalProperties": false},
			OutputSchema: map[string]any{"type": "object"}, Files: []ToolPackageFile{{Path: "tool.ps1", Content: windowsFoundationPowerShell}},
			DependencyStrategy: ToolDependencyStrategy{Requested: "none", Selected: "none", SelectionReason: "Windows and PowerShell built-in capability"},
			DefaultTimeoutSec:  120, OutputLimitBytes: 8 << 20, SupportsCancel: true,
			IdempotencyMode: RuntimeIdempotencyNonIdempotent, SideEffect: definition.sideEffect,
		}
		if err := s.ensurePlatformToolPackage(ctx, manifest, now); err != nil {
			return err
		}
		s.runtimeTools.register(newPackageRuntimeTool(s, manifest))
	}
	return nil
}

func (s *Service) ensurePlatformToolPackage(ctx context.Context, manifest ToolPackageManifest, now time.Time) error {
	packageDir := s.toolPackageDir(manifest.Name, manifest.Version)
	if err := writeToolPackageFiles(packageDir, manifest.Files); err != nil {
		return err
	}
	digest := toolPackageDigest(manifest)
	manifestJSON, _ := json.Marshal(manifest)
	sbomJSON, _ := json.Marshal(buildToolSBOM(manifest, digest))
	provenanceJSON, _ := json.Marshal(buildToolProvenance(manifest, digest, CreateToolPackageInput{CreatedByModel: "platform"}))
	_, err := s.db.Pool.Exec(ctx, `
		insert into steward_tools (name,title,description,origin,enabled,active_version,execution_target,health_status,health_summary,catalog_generation,created_by_model,created_at,updated_at)
		values ($1,$2,$3,'platform',true,$4,$5,'healthy','Windows platform adapter ready',1,'platform',$6,$6)
		on conflict (name) do update set title=excluded.title,description=excluded.description,enabled=true,active_version=excluded.active_version,
			execution_target=excluded.execution_target,health_status='healthy',health_summary='Windows platform adapter ready',updated_at=excluded.updated_at
	`, manifest.Name, manifest.Title, manifest.Description, manifest.Version, manifest.ExecutionTarget, now)
	if err != nil {
		return err
	}
	_, err = s.db.Pool.Exec(ctx, `
		insert into steward_tool_versions (tool_name,version,runtime,status,manifest,package_path,content_sha256,sbom,provenance,validation_summary,created_at,validated_at)
		values ($1,$2,'powershell','enabled',$3::jsonb,$4,$5,$6::jsonb,$7::jsonb,'versioned Windows platform adapter',$8,$8)
		on conflict (tool_name,version) do update set manifest=excluded.manifest,package_path=excluded.package_path,content_sha256=excluded.content_sha256,
			sbom=excluded.sbom,provenance=excluded.provenance,status='enabled',validated_at=excluded.validated_at
	`, manifest.Name, manifest.Version, string(manifestJSON), packageDir, digest, string(sbomJSON), string(provenanceJSON), now)
	return err
}

func windowsFoundationToolDefinitions() []windowsFoundationToolDefinition {
	s := func(kind string) map[string]any { return map[string]any{"type": kind} }
	path := map[string]any{"path": s("string")}
	defs := []windowsFoundationToolDefinition{
		{name: "fs.exists", description: "Check whether a Windows filesystem path exists.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"path"}, properties: path},
		{name: "fs.stat", description: "Read structured metadata for a file or directory.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"path"}, properties: path},
		{name: "fs.search", description: "Recursively search files by wildcard name and optional text content.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"root"}, properties: map[string]any{"root": s("string"), "pattern": s("string"), "content": s("string"), "max_results": s("integer")}},
		{name: "fs.get_known_folders", description: "Return the interactive user's known Windows folders.", target: toolTargetSession, sideEffect: RuntimeSideEffectNone, properties: map[string]any{}},
		{name: "fs.read_bytes", description: "Read a file as base64 bytes.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"path"}, properties: path},
		{name: "fs.write_text", description: "Write UTF-8 text to a file, creating parent directories when requested.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"path", "content"}, properties: map[string]any{"path": s("string"), "content": s("string"), "create_parents": s("boolean")}},
		{name: "fs.append_text", description: "Append UTF-8 text to a file.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"path", "content"}, properties: map[string]any{"path": s("string"), "content": s("string")}},
		{name: "fs.patch_text", description: "Replace exact text in a UTF-8 file.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"path", "old_text", "new_text"}, properties: map[string]any{"path": s("string"), "old_text": s("string"), "new_text": s("string"), "replace_all": s("boolean")}},
		{name: "fs.copy", description: "Copy a file or directory.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"source", "destination"}, properties: map[string]any{"source": s("string"), "destination": s("string"), "overwrite": s("boolean")}},
		{name: "fs.move", description: "Move or rename a file or directory.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"source", "destination"}, properties: map[string]any{"source": s("string"), "destination": s("string"), "overwrite": s("boolean")}},
		{name: "fs.delete", description: "Delete a file or directory tree.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"path"}, properties: map[string]any{"path": s("string"), "recursive": s("boolean"), "force": s("boolean")}},
		{name: "fs.hash", description: "Compute a cryptographic file hash.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"path"}, properties: map[string]any{"path": s("string"), "algorithm": s("string")}},
		{name: "fs.create_temp", description: "Create a temporary file or directory.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, properties: map[string]any{"kind": s("string"), "prefix": s("string")}},
		{name: "archive.list", description: "List entries in a ZIP archive.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"path"}, properties: path},
		{name: "archive.create", description: "Create a ZIP archive from one or more paths.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"source", "destination"}, properties: map[string]any{"source": s("string"), "destination": s("string"), "overwrite": s("boolean")}},
		{name: "archive.extract", description: "Extract a ZIP archive.", target: toolTargetAuto, sideEffect: RuntimeSideEffectWrite, required: []string{"path", "destination"}, properties: map[string]any{"path": s("string"), "destination": s("string"), "overwrite": s("boolean")}},
		{name: "archive.test", description: "Open and enumerate a ZIP archive to verify its structure.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"path"}, properties: path},
		{name: "process.list", description: "List Windows processes with identifiers and resource usage.", target: toolTargetSystem, sideEffect: RuntimeSideEffectNone, properties: map[string]any{"name": s("string")}},
		{name: "process.get", description: "Get one process including command line and parent process.", target: toolTargetSystem, sideEffect: RuntimeSideEffectNone, required: []string{"pid"}, properties: map[string]any{"pid": s("integer")}},
		{name: "process.start", description: "Start a process with structured arguments.", target: toolTargetAuto, sideEffect: RuntimeSideEffectProcess, required: []string{"command"}, properties: map[string]any{"command": s("string"), "arguments": s("array"), "working_directory": s("string"), "wait": s("boolean")}},
		{name: "process.wait", description: "Wait for a process to exit.", target: toolTargetSystem, sideEffect: RuntimeSideEffectNone, required: []string{"pid"}, properties: map[string]any{"pid": s("integer"), "timeout_seconds": s("integer")}},
		{name: "process.stop", description: "Request process termination.", target: toolTargetSystem, sideEffect: RuntimeSideEffectProcess, required: []string{"pid"}, properties: map[string]any{"pid": s("integer"), "force": s("boolean")}},
		{name: "process.kill_tree", description: "Terminate a process and its descendants.", target: toolTargetSystem, sideEffect: RuntimeSideEffectProcess, required: []string{"pid"}, properties: map[string]any{"pid": s("integer")}},
		{name: "process.find_by_name", description: "Find processes by executable name.", target: toolTargetSystem, sideEffect: RuntimeSideEffectNone, required: []string{"name"}, properties: map[string]any{"name": s("string")}},
		{name: "process.find_port_owner", description: "Find the process listening on a TCP port.", target: toolTargetSystem, sideEffect: RuntimeSideEffectNone, required: []string{"port"}, properties: map[string]any{"port": s("integer")}},
		{name: "application.open", description: "Open an application, document, folder, or URI in the interactive session.", target: toolTargetSession, sideEffect: RuntimeSideEffectLaunch, required: []string{"target"}, properties: map[string]any{"target": s("string"), "arguments": s("array")}},
		{name: "application.resolve", description: "Resolve an executable or command through Windows command discovery.", target: toolTargetAuto, sideEffect: RuntimeSideEffectNone, required: []string{"name"}, properties: map[string]any{"name": s("string")}},
		{name: "application.list_installed", description: "List installed desktop applications from Windows uninstall registries.", target: toolTargetSystem, sideEffect: RuntimeSideEffectNone, properties: map[string]any{"query": s("string")}},
	}
	for _, name := range []string{"list", "get", "start", "stop", "restart", "set_start_type", "create", "delete"} {
		properties := map[string]any{"name": s("string"), "display_name": s("string"), "binary_path": s("string"), "start_type": s("string"), "description": s("string")}
		required := []string{}
		if name != "list" {
			required = []string{"name"}
		}
		side := RuntimeSideEffectNone
		if name != "list" && name != "get" {
			side = RuntimeSideEffectProcess
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "windows.service." + name, description: "Manage or inspect Windows services: " + name + ".", target: toolTargetSystem, sideEffect: side, required: required, properties: properties})
	}
	for _, name := range []string{"list", "get", "focus", "minimize", "maximize", "restore", "move_resize", "close"} {
		properties := map[string]any{"handle": s("integer"), "pid": s("integer"), "title": s("string"), "x": s("integer"), "y": s("integer"), "width": s("integer"), "height": s("integer")}
		required := []string{}
		if name != "list" {
			required = []string{"handle"}
		}
		side := RuntimeSideEffectNone
		if name != "list" && name != "get" {
			side = RuntimeSideEffectLaunch
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "window." + name, description: "Inspect or control an interactive desktop window: " + name + ".", target: toolTargetSession, sideEffect: side, required: required, properties: properties})
	}
	defs = append(defs,
		windowsFoundationToolDefinition{name: "screen.capture", description: "Capture the interactive desktop or one rectangular region to PNG.", target: toolTargetSession, sideEffect: RuntimeSideEffectWrite, required: []string{"path"}, properties: map[string]any{"path": s("string"), "x": s("integer"), "y": s("integer"), "width": s("integer"), "height": s("integer")}},
		windowsFoundationToolDefinition{name: "uia.snapshot", description: "Return an accessible UI Automation tree snapshot.", target: toolTargetSession, sideEffect: RuntimeSideEffectNone, properties: map[string]any{"handle": s("integer"), "max_elements": s("integer")}},
		windowsFoundationToolDefinition{name: "uia.find", description: "Find an accessible UI element by name or automation ID.", target: toolTargetSession, sideEffect: RuntimeSideEffectNone, properties: map[string]any{"handle": s("integer"), "name": s("string"), "automation_id": s("string")}},
		windowsFoundationToolDefinition{name: "uia.invoke", description: "Invoke an accessible UI element.", target: toolTargetSession, sideEffect: RuntimeSideEffectLaunch, required: []string{"runtime_id"}, properties: map[string]any{"runtime_id": s("string")}},
		windowsFoundationToolDefinition{name: "uia.set_value", description: "Set the value of an accessible UI element.", target: toolTargetSession, sideEffect: RuntimeSideEffectWrite, required: []string{"runtime_id", "value"}, properties: map[string]any{"runtime_id": s("string"), "value": s("string")}},
	)
	for _, name := range []string{"type_text", "send_keys", "mouse_move", "mouse_click", "mouse_scroll"} {
		defs = append(defs, windowsFoundationToolDefinition{name: "input." + name, description: "Send interactive desktop input: " + name + ".", target: toolTargetSession, sideEffect: RuntimeSideEffectLaunch, properties: map[string]any{"text": s("string"), "keys": s("string"), "x": s("integer"), "y": s("integer"), "button": s("string"), "delta": s("integer")}})
	}
	for _, name := range []string{"read_text", "write_text", "read_files", "write_files"} {
		side := RuntimeSideEffectNone
		if strings.HasPrefix(name, "write") {
			side = RuntimeSideEffectWrite
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "clipboard." + name, description: "Read or write the interactive Windows clipboard: " + name + ".", target: toolTargetSession, sideEffect: side, properties: map[string]any{"text": s("string"), "paths": s("array")}})
	}
	for _, name := range []string{"adapters", "addresses", "connections", "routes", "dns_lookup", "ping", "traceroute", "port_probe", "download"} {
		side := RuntimeSideEffectNone
		if name == "download" {
			side = RuntimeSideEffectNetwork
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "net." + name, description: "Inspect or use Windows networking: " + name + ".", target: toolTargetSystem, sideEffect: side, properties: map[string]any{"host": s("string"), "port": s("integer"), "url": s("string"), "path": s("string"), "protocol": s("string"), "timeout_seconds": s("integer")}})
	}
	for _, name := range []string{"list_rules", "upsert_rule", "delete_rule"} {
		side := RuntimeSideEffectNone
		if name != "list_rules" {
			side = RuntimeSideEffectWrite
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "windows.firewall." + name, description: "Inspect or change Windows Firewall rules: " + name + ".", target: toolTargetSystem, sideEffect: side, properties: map[string]any{"name": s("string"), "direction": s("string"), "action": s("string"), "protocol": s("string"), "local_port": s("string"), "program": s("string")}})
	}
	for _, name := range []string{"list", "get", "set", "delete", "export", "import"} {
		side := RuntimeSideEffectNone
		if name != "list" && name != "get" && name != "export" {
			side = RuntimeSideEffectWrite
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "registry." + name, description: "Inspect or modify the Windows Registry: " + name + ".", target: toolTargetAuto, sideEffect: side, properties: map[string]any{"path": s("string"), "name": s("string"), "value": map[string]any{}, "value_type": s("string"), "file": s("string")}})
	}
	for _, name := range []string{"list", "get", "create", "update", "run", "enable", "disable", "delete"} {
		side := RuntimeSideEffectNone
		if name != "list" && name != "get" {
			side = RuntimeSideEffectProcess
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "scheduled_task." + name, description: "Inspect or manage Windows scheduled tasks: " + name + ".", target: toolTargetSystem, sideEffect: side, properties: map[string]any{"name": s("string"), "path": s("string"), "executable": s("string"), "arguments": s("string"), "trigger": s("string"), "at": s("string")}})
	}
	for _, name := range []string{"list", "search", "install", "uninstall", "upgrade", "upgrade_all", "sources"} {
		side := RuntimeSideEffectNone
		if name != "list" && name != "search" && name != "sources" {
			side = RuntimeSideEffectProcess
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "software." + name, description: "Inspect or manage Windows software through WinGet: " + name + ".", target: toolTargetSystem, sideEffect: side, properties: map[string]any{"id": s("string"), "query": s("string"), "version": s("string"), "source": s("string"), "scope": s("string")}})
	}
	for _, name := range []string{"info", "os_version", "cpu", "memory", "disks", "uptime", "users", "groups", "env.list", "env.get", "env.set", "env.delete"} {
		side := RuntimeSideEffectNone
		if strings.HasPrefix(name, "env.set") || strings.HasPrefix(name, "env.delete") {
			side = RuntimeSideEffectWrite
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "system." + name, description: "Inspect or update Windows system information: " + name + ".", target: toolTargetSystem, sideEffect: side, properties: map[string]any{"name": s("string"), "value": s("string"), "scope": s("string")}})
	}
	for _, name := range []string{"list_logs", "query", "export", "clear"} {
		side := RuntimeSideEffectNone
		if name == "export" || name == "clear" {
			side = RuntimeSideEffectWrite
		}
		defs = append(defs, windowsFoundationToolDefinition{name: "windows.eventlog." + name, description: "Inspect or manage Windows event logs: " + name + ".", target: toolTargetSystem, sideEffect: side, properties: map[string]any{"log_name": s("string"), "provider": s("string"), "level": s("integer"), "since": s("string"), "max_events": s("integer"), "path": s("string")}})
	}
	defs = append(defs,
		windowsFoundationToolDefinition{name: "notify.toast", description: "Show a notification in the interactive user session.", target: toolTargetSession, sideEffect: RuntimeSideEffectLaunch, required: []string{"message"}, properties: map[string]any{"title": s("string"), "message": s("string"), "timeout_seconds": s("integer")}},
		windowsFoundationToolDefinition{name: "notify.sound", description: "Play a Windows system sound.", target: toolTargetSession, sideEffect: RuntimeSideEffectLaunch, properties: map[string]any{"sound": s("string")}},
	)
	for _, name := range []string{"lock", "sleep", "restart", "shutdown"} {
		defs = append(defs, windowsFoundationToolDefinition{name: "power." + name, description: "Change Windows power or session state: " + name + ".", target: toolTargetSystem, sideEffect: RuntimeSideEffectProcess, properties: map[string]any{"force": s("boolean"), "delay_seconds": s("integer")}})
	}
	return defs
}

const windowsFoundationPowerShell = `
$ErrorActionPreference = 'Stop'
[Console]::InputEncoding = [Text.UTF8Encoding]::new($false)
[Console]::OutputEncoding = [Text.UTF8Encoding]::new($false)
function A([string]$Name, $Default = $null) { if ($null -ne $script:a.PSObject.Properties[$Name]) { return $script:a.$Name }; return $Default }
function Obj($Value) { if ($null -eq $Value) { return @{} }; return $Value }
function WindowNative {
  if (-not ('StewardNative' -as [type])) { Add-Type @'
using System; using System.Text; using System.Runtime.InteropServices;
public static class StewardNative {
 public delegate bool EnumWindowsProc(IntPtr h, IntPtr l);
 [DllImport("user32.dll")] public static extern bool EnumWindows(EnumWindowsProc cb, IntPtr l);
 [DllImport("user32.dll")] public static extern bool IsWindowVisible(IntPtr h);
 [DllImport("user32.dll", CharSet=CharSet.Unicode)] public static extern int GetWindowText(IntPtr h, StringBuilder s, int n);
 [DllImport("user32.dll")] public static extern uint GetWindowThreadProcessId(IntPtr h, out uint p);
 [DllImport("user32.dll")] public static extern bool SetForegroundWindow(IntPtr h);
 [DllImport("user32.dll")] public static extern bool ShowWindow(IntPtr h, int n);
 [DllImport("user32.dll")] public static extern bool MoveWindow(IntPtr h,int x,int y,int w,int z,bool r);
 [DllImport("user32.dll")] public static extern bool PostMessage(IntPtr h,uint m,IntPtr w,IntPtr l);
 [DllImport("user32.dll")] public static extern bool SetCursorPos(int x,int y);
 [DllImport("user32.dll")] public static extern void mouse_event(uint f,uint dx,uint dy,uint d,UIntPtr e);
}
'@ }
}
function WindowsList {
 WindowNative; $items = [System.Collections.Generic.List[object]]::new()
 $callback = [StewardNative+EnumWindowsProc]{ param($h,$l); if ([StewardNative]::IsWindowVisible($h)) { $b=[Text.StringBuilder]::new(2048); [void][StewardNative]::GetWindowText($h,$b,$b.Capacity); if ($b.Length -gt 0) { [uint32]$p=0; [void][StewardNative]::GetWindowThreadProcessId($h,[ref]$p); $items.Add([ordered]@{handle=$h.ToInt64();pid=$p;title=$b.ToString()}) } }; return $true }
 [void][StewardNative]::EnumWindows($callback,[IntPtr]::Zero); return @($items)
}
function GetHandle { return [IntPtr]::new([int64](A 'handle' 0)) }
function UIARoot {
 Add-Type -AssemblyName UIAutomationClient; Add-Type -AssemblyName UIAutomationTypes
 $h=[int64](A 'handle' 0); if ($h -gt 0) { return [Windows.Automation.AutomationElement]::FromHandle([IntPtr]::new($h)) }; return [Windows.Automation.AutomationElement]::RootElement
}
function UIAFindByRuntime([string]$id) {
 $root=UIARoot; $all=$root.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition)
 foreach($e in $all){ if ((@($e.GetRuntimeId()) -join '.') -eq $id) { return $e } }; throw "UI element runtime_id not found"
}
try {
 $line=[Console]::In.ReadLine(); $request=$line|ConvertFrom-Json -Depth 100; $script:a=$request.arguments; $action=$env:STEWARD_TOOL_NAME; $r=@{}
 switch ($action) {
  'fs.exists' { $r=[ordered]@{path=(A 'path');exists=(Test-Path -LiteralPath (A 'path'));type=if(Test-Path -LiteralPath (A 'path') -PathType Container){'directory'}elseif(Test-Path -LiteralPath (A 'path') -PathType Leaf){'file'}else{'missing'}} }
  'fs.stat' { $i=Get-Item -LiteralPath (A 'path') -Force; $r=[ordered]@{path=$i.FullName;name=$i.Name;is_directory=$i.PSIsContainer;length=if($i.PSIsContainer){0}else{$i.Length};created_at=$i.CreationTimeUtc;modified_at=$i.LastWriteTimeUtc;attributes=$i.Attributes.ToString()} }
  'fs.search' { $max=[int](A 'max_results' 200); $pattern=[string](A 'pattern' '*'); $content=[string](A 'content' ''); $items=Get-ChildItem -LiteralPath (A 'root') -Recurse -Force -File -Filter $pattern -ErrorAction SilentlyContinue; if($content){$items=$items|Select-String -SimpleMatch $content|ForEach-Object{$_.Path}|Sort-Object -Unique|ForEach-Object{Get-Item -LiteralPath $_}}; $r=[ordered]@{matches=@($items|Select-Object -First $max|ForEach-Object{[ordered]@{path=$_.FullName;length=$_.Length;modified_at=$_.LastWriteTimeUtc}})} }
  'fs.get_known_folders' { $home=[Environment]::GetFolderPath('UserProfile'); $r=[ordered]@{home=$home;desktop=[Environment]::GetFolderPath('Desktop');documents=[Environment]::GetFolderPath('MyDocuments');downloads=(Join-Path $home 'Downloads');pictures=[Environment]::GetFolderPath('MyPictures');music=[Environment]::GetFolderPath('MyMusic');videos=[Environment]::GetFolderPath('MyVideos');app_data=[Environment]::GetFolderPath('ApplicationData')} }
  'fs.read_bytes' { $b=[IO.File]::ReadAllBytes((A 'path')); $r=[ordered]@{path=(A 'path');size=$b.Length;base64=[Convert]::ToBase64String($b)} }
  'fs.write_text' { $p=[string](A 'path'); if(A 'create_parents' $false){[IO.Directory]::CreateDirectory([IO.Path]::GetDirectoryName([IO.Path]::GetFullPath($p)))|Out-Null}; [IO.File]::WriteAllText($p,[string](A 'content'),[Text.UTF8Encoding]::new($false)); $r=[ordered]@{path=[IO.Path]::GetFullPath($p);bytes=(Get-Item $p).Length} }
  'fs.append_text' { [IO.File]::AppendAllText((A 'path'),[string](A 'content'),[Text.UTF8Encoding]::new($false)); $r=[ordered]@{path=[IO.Path]::GetFullPath((A 'path'));bytes=(Get-Item (A 'path')).Length} }
  'fs.patch_text' { $p=[string](A 'path');$old=[string](A 'old_text');$new=[string](A 'new_text');$text=[IO.File]::ReadAllText($p);if(-not $text.Contains($old)){throw 'old_text not found'};$count=([regex]::Matches($text,[regex]::Escape($old))).Count;if(A 'replace_all' $false){$text=$text.Replace($old,$new)}else{$idx=$text.IndexOf($old);$text=$text.Substring(0,$idx)+$new+$text.Substring($idx+$old.Length)};[IO.File]::WriteAllText($p,$text,[Text.UTF8Encoding]::new($false));$r=[ordered]@{path=[IO.Path]::GetFullPath($p);matches=$count} }
  'fs.copy' { Copy-Item -LiteralPath (A 'source') -Destination (A 'destination') -Recurse -Force:([bool](A 'overwrite' $false)); $r=[ordered]@{source=(A 'source');destination=[IO.Path]::GetFullPath((A 'destination'))} }
  'fs.move' { Move-Item -LiteralPath (A 'source') -Destination (A 'destination') -Force:([bool](A 'overwrite' $false)); $r=[ordered]@{source=(A 'source');destination=[IO.Path]::GetFullPath((A 'destination'))} }
  'fs.delete' { Remove-Item -LiteralPath (A 'path') -Recurse:([bool](A 'recursive' $false)) -Force:([bool](A 'force' $false)); $r=[ordered]@{path=(A 'path');deleted=$true} }
  'fs.hash' { $h=Get-FileHash -LiteralPath (A 'path') -Algorithm ([string](A 'algorithm' 'SHA256'));$r=[ordered]@{path=(A 'path');algorithm=$h.Algorithm;hash=$h.Hash.ToLowerInvariant()} }
  'fs.create_temp' { $prefix=[string](A 'prefix' 'steward'); if((A 'kind' 'file') -eq 'directory'){$p=Join-Path ([IO.Path]::GetTempPath()) ($prefix+'-'+[guid]::NewGuid());[IO.Directory]::CreateDirectory($p)|Out-Null;$kind='directory'}else{$p=Join-Path ([IO.Path]::GetTempPath()) ($prefix+'-'+[guid]::NewGuid()+'.tmp');[IO.File]::WriteAllBytes($p,[byte[]]@());$kind='file'};$r=[ordered]@{path=$p;kind=$kind} }
  'archive.list' { Add-Type -AssemblyName System.IO.Compression.FileSystem;$z=[IO.Compression.ZipFile]::OpenRead((A 'path'));try{$r=[ordered]@{entries=@($z.Entries|ForEach-Object{[ordered]@{name=$_.FullName;length=$_.Length;compressed_length=$_.CompressedLength;modified_at=$_.LastWriteTime}})}}finally{$z.Dispose()} }
  'archive.create' { if((Test-Path (A 'destination')) -and (A 'overwrite' $false)){Remove-Item -Force (A 'destination')};Compress-Archive -LiteralPath (A 'source') -DestinationPath (A 'destination') -Force:$([bool](A 'overwrite' $false));$r=[ordered]@{path=[IO.Path]::GetFullPath((A 'destination'));bytes=(Get-Item (A 'destination')).Length} }
  'archive.extract' { Expand-Archive -LiteralPath (A 'path') -DestinationPath (A 'destination') -Force:$([bool](A 'overwrite' $false));$r=[ordered]@{path=(A 'path');destination=[IO.Path]::GetFullPath((A 'destination'))} }
  'archive.test' { Add-Type -AssemblyName System.IO.Compression.FileSystem;$z=[IO.Compression.ZipFile]::OpenRead((A 'path'));try{$count=$z.Entries.Count;$total=($z.Entries|Measure-Object Length -Sum).Sum;$r=[ordered]@{valid=$true;entries=$count;uncompressed_bytes=$total}}finally{$z.Dispose()} }
  'process.list' { $q=Get-Process -ErrorAction SilentlyContinue;if(A 'name'){$q=$q|Where-Object{$_.ProcessName -like ('*'+(A 'name')+'*')}};$r=[ordered]@{processes=@($q|ForEach-Object{[ordered]@{pid=$_.Id;name=$_.ProcessName;cpu_seconds=$_.CPU;working_set=$_.WorkingSet64;started_at=try{$_.StartTime.ToUniversalTime()}catch{$null}}})} }
  'process.get' { $p=Get-CimInstance Win32_Process -Filter ('ProcessId='+(A 'pid'));if(-not $p){throw 'process not found'};$r=[ordered]@{pid=$p.ProcessId;parent_pid=$p.ParentProcessId;name=$p.Name;executable=$p.ExecutablePath;command_line=$p.CommandLine;created_at=$p.CreationDate} }
  'process.start' { $args=@(A 'arguments' @());$sp=@{FilePath=(A 'command');ArgumentList=$args;PassThru=$true};if(A 'working_directory'){$sp.WorkingDirectory=A 'working_directory'};if(A 'wait' $false){$sp.Wait=$true};$p=Start-Process @sp;$r=[ordered]@{pid=$p.Id;name=$p.ProcessName;exited=$p.HasExited;exit_code=if($p.HasExited){$p.ExitCode}else{$null}} }
  'process.wait' { $p=Get-Process -Id (A 'pid') -ErrorAction Stop;$ms=[int](A 'timeout_seconds' 60)*1000;if(-not $p.WaitForExit($ms)){throw 'process wait timed out'};$r=[ordered]@{pid=$p.Id;exited=$true;exit_code=$p.ExitCode} }
  'process.stop' { Stop-Process -Id (A 'pid') -Force:([bool](A 'force' $false));$r=[ordered]@{pid=(A 'pid');stopped=$true} }
  'process.kill_tree' { & taskkill.exe /PID ([string](A 'pid')) /T /F | Out-Null;if($LASTEXITCODE -ne 0){throw 'taskkill failed'};$r=[ordered]@{pid=(A 'pid');tree_stopped=$true} }
  'process.find_by_name' { $q=Get-Process -Name (A 'name') -ErrorAction SilentlyContinue;$r=[ordered]@{processes=@($q|ForEach-Object{[ordered]@{pid=$_.Id;name=$_.ProcessName}})} }
  'process.find_port_owner' { $q=Get-NetTCPConnection -LocalPort (A 'port') -ErrorAction SilentlyContinue;$r=[ordered]@{owners=@($q|ForEach-Object{$p=Get-Process -Id $_.OwningProcess -ErrorAction SilentlyContinue;[ordered]@{pid=$_.OwningProcess;name=$p.ProcessName;state=$_.State;local_address=$_.LocalAddress;port=$_.LocalPort}})} }
  'application.open' { $args=@(A 'arguments' @());$p=Start-Process -FilePath (A 'target') -ArgumentList $args -PassThru;$r=[ordered]@{target=(A 'target');pid=$p.Id} }
  'application.resolve' { $c=Get-Command (A 'name') -ErrorAction Stop|Select-Object -First 1;$r=[ordered]@{name=$c.Name;path=$c.Source;command_type=$c.CommandType.ToString();version=$c.Version.ToString()} }
  'application.list_installed' { $paths=@('HKLM:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*','HKLM:\Software\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\*','HKCU:\Software\Microsoft\Windows\CurrentVersion\Uninstall\*');$q=Get-ItemProperty $paths -ErrorAction SilentlyContinue|Where-Object DisplayName;if(A 'query'){$q=$q|Where-Object{$_.DisplayName -like ('*'+(A 'query')+'*')}};$r=[ordered]@{applications=@($q|Sort-Object DisplayName -Unique|ForEach-Object{[ordered]@{name=$_.DisplayName;version=$_.DisplayVersion;publisher=$_.Publisher;install_location=$_.InstallLocation;uninstall=$_.UninstallString}})} }
  {$_ -like 'windows.service.*'} { $op=$action.Substring(16);$n=A 'name';switch($op){'list'{$x=Get-CimInstance Win32_Service;$r=[ordered]@{services=@($x|ForEach-Object{[ordered]@{name=$_.Name;display_name=$_.DisplayName;status=$_.State;start_type=$_.StartMode;pid=$_.ProcessId;binary_path=$_.PathName}})}}'get'{$x=Get-CimInstance Win32_Service -Filter ("Name='"+$n.Replace("'","''")+"'");if(-not $x){throw 'service not found'};$r=[ordered]@{name=$x.Name;display_name=$x.DisplayName;status=$x.State;start_type=$x.StartMode;pid=$x.ProcessId;binary_path=$x.PathName}}'start'{Start-Service $n;$r=[ordered]@{name=$n;status=(Get-Service $n).Status.ToString()}}'stop'{Stop-Service $n -Force;$r=[ordered]@{name=$n;status=(Get-Service $n).Status.ToString()}}'restart'{Restart-Service $n -Force;$r=[ordered]@{name=$n;status=(Get-Service $n).Status.ToString()}}'set_start_type'{Set-Service $n -StartupType (A 'start_type');$r=[ordered]@{name=$n;start_type=(A 'start_type')}}'create'{New-Service -Name $n -BinaryPathName (A 'binary_path') -DisplayName (A 'display_name' $n) -Description (A 'description' '') -StartupType (A 'start_type' 'Manual')|Out-Null;$r=[ordered]@{name=$n;created=$true}}'delete'{& sc.exe delete $n|Out-Null;if($LASTEXITCODE -ne 0){throw 'service delete failed'};$r=[ordered]@{name=$n;deleted=$true}}} }
  {$_ -like 'window.*'} { $op=$action.Substring(7);if($op -eq 'list'){$r=[ordered]@{windows=@(WindowsList)}}else{$h=GetHandle;WindowNative;if($op -eq 'get'){$w=@(WindowsList)|Where-Object handle -eq $h.ToInt64();if(-not $w){throw 'window not found'};$r=$w}elseif($op -eq 'focus'){[void][StewardNative]::SetForegroundWindow($h);$r=[ordered]@{handle=$h.ToInt64();focused=$true}}elseif($op -eq 'minimize'){[void][StewardNative]::ShowWindow($h,6);$r=[ordered]@{handle=$h.ToInt64();state='minimized'}}elseif($op -eq 'maximize'){[void][StewardNative]::ShowWindow($h,3);$r=[ordered]@{handle=$h.ToInt64();state='maximized'}}elseif($op -eq 'restore'){[void][StewardNative]::ShowWindow($h,9);$r=[ordered]@{handle=$h.ToInt64();state='restored'}}elseif($op -eq 'move_resize'){[void][StewardNative]::MoveWindow($h,(A 'x'),(A 'y'),(A 'width'),(A 'height'),$true);$r=[ordered]@{handle=$h.ToInt64();moved=$true}}elseif($op -eq 'close'){[void][StewardNative]::PostMessage($h,0x10,[IntPtr]::Zero,[IntPtr]::Zero);$r=[ordered]@{handle=$h.ToInt64();close_requested=$true}} } }
  'screen.capture' { Add-Type -AssemblyName System.Drawing;Add-Type -AssemblyName System.Windows.Forms;$x=[int](A 'x' 0);$y=[int](A 'y' 0);$w=[int](A 'width' [Windows.Forms.SystemInformation]::VirtualScreen.Width);$h=[int](A 'height' [Windows.Forms.SystemInformation]::VirtualScreen.Height);$bmp=[Drawing.Bitmap]::new($w,$h);$g=[Drawing.Graphics]::FromImage($bmp);try{$g.CopyFromScreen($x,$y,0,0,[Drawing.Size]::new($w,$h));$bmp.Save((A 'path'),[Drawing.Imaging.ImageFormat]::Png)}finally{$g.Dispose();$bmp.Dispose()};$r=[ordered]@{path=[IO.Path]::GetFullPath((A 'path'));width=$w;height=$h} }
  'uia.snapshot' { $root=UIARoot;$max=[int](A 'max_elements' 200);$all=$root.FindAll([Windows.Automation.TreeScope]::Descendants,[Windows.Automation.Condition]::TrueCondition);$items=@();for($i=0;$i -lt [Math]::Min($all.Count,$max);$i++){$e=$all.Item($i);$items+=[ordered]@{runtime_id=(@($e.GetRuntimeId()) -join '.');name=$e.Current.Name;automation_id=$e.Current.AutomationId;control_type=$e.Current.ControlType.ProgrammaticName;enabled=$e.Current.IsEnabled;offscreen=$e.Current.IsOffscreen}};$r=[ordered]@{elements=$items;truncated=($all.Count -gt $max);total=$all.Count} }
  'uia.find' { $root=UIARoot;$conds=@();if(A 'name'){$conds+=[Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::NameProperty,(A 'name'))};if(A 'automation_id'){$conds+=[Windows.Automation.PropertyCondition]::new([Windows.Automation.AutomationElement]::AutomationIdProperty,(A 'automation_id'))};if($conds.Count -eq 0){throw 'name or automation_id is required'};$cond=if($conds.Count -eq 1){$conds[0]}else{[Windows.Automation.AndCondition]::new($conds)};$all=$root.FindAll([Windows.Automation.TreeScope]::Descendants,$cond);$r=[ordered]@{elements=@($all|ForEach-Object{[ordered]@{runtime_id=(@($_.GetRuntimeId()) -join '.');name=$_.Current.Name;automation_id=$_.Current.AutomationId;control_type=$_.Current.ControlType.ProgrammaticName}})} }
  'uia.invoke' { $e=UIAFindByRuntime (A 'runtime_id');$p=$e.GetCurrentPattern([Windows.Automation.InvokePattern]::Pattern);$p.Invoke();$r=[ordered]@{runtime_id=(A 'runtime_id');invoked=$true} }
  'uia.set_value' { $e=UIAFindByRuntime (A 'runtime_id');$p=$e.GetCurrentPattern([Windows.Automation.ValuePattern]::Pattern);$p.SetValue((A 'value'));$r=[ordered]@{runtime_id=(A 'runtime_id');value_set=$true} }
  'input.type_text' { Add-Type -AssemblyName System.Windows.Forms;[Windows.Forms.SendKeys]::SendWait(([string](A 'text')).Replace('{','{{}').Replace('}','{}}'));$r=[ordered]@{characters=([string](A 'text')).Length} }
  'input.send_keys' { Add-Type -AssemblyName System.Windows.Forms;[Windows.Forms.SendKeys]::SendWait((A 'keys'));$r=[ordered]@{keys=(A 'keys');sent=$true} }
  'input.mouse_move' { WindowNative;[void][StewardNative]::SetCursorPos((A 'x'),(A 'y'));$r=[ordered]@{x=(A 'x');y=(A 'y')} }
  'input.mouse_click' { WindowNative;$b=[string](A 'button' 'left');if($b -eq 'right'){[StewardNative]::mouse_event(8,0,0,0,[UIntPtr]::Zero);[StewardNative]::mouse_event(16,0,0,0,[UIntPtr]::Zero)}else{[StewardNative]::mouse_event(2,0,0,0,[UIntPtr]::Zero);[StewardNative]::mouse_event(4,0,0,0,[UIntPtr]::Zero)};$r=[ordered]@{button=$b;clicked=$true} }
  'input.mouse_scroll' { WindowNative;[StewardNative]::mouse_event(0x800,0,0,[uint32][int32](A 'delta' 120),[UIntPtr]::Zero);$r=[ordered]@{delta=(A 'delta' 120)} }
  'clipboard.read_text' { Add-Type -AssemblyName System.Windows.Forms;$r=[ordered]@{text=[Windows.Forms.Clipboard]::GetText()} }
  'clipboard.write_text' { Add-Type -AssemblyName System.Windows.Forms;[Windows.Forms.Clipboard]::SetText([string](A 'text'));$r=[ordered]@{characters=([string](A 'text')).Length} }
  'clipboard.read_files' { Add-Type -AssemblyName System.Windows.Forms;$r=[ordered]@{paths=@([Windows.Forms.Clipboard]::GetFileDropList())} }
  'clipboard.write_files' { Add-Type -AssemblyName System.Windows.Forms;$c=[Collections.Specialized.StringCollection]::new();foreach($p in @(A 'paths' @())){[void]$c.Add([IO.Path]::GetFullPath($p))};[Windows.Forms.Clipboard]::SetFileDropList($c);$r=[ordered]@{count=$c.Count} }
  {$_ -like 'net.*'} { $op=$action.Substring(4);switch($op){'adapters'{$r=[ordered]@{adapters=@(Get-NetAdapter|ForEach-Object{[ordered]@{name=$_.Name;description=$_.InterfaceDescription;status=$_.Status;mac=$_.MacAddress;speed=$_.LinkSpeed}})}}'addresses'{$r=[ordered]@{addresses=@(Get-NetIPAddress|ForEach-Object{[ordered]@{interface=$_.InterfaceAlias;address=$_.IPAddress;family=$_.AddressFamily.ToString();prefix_length=$_.PrefixLength}})}}'connections'{$r=[ordered]@{connections=@(Get-NetTCPConnection|ForEach-Object{[ordered]@{local_address=$_.LocalAddress;local_port=$_.LocalPort;remote_address=$_.RemoteAddress;remote_port=$_.RemotePort;state=$_.State;pid=$_.OwningProcess}})}}'routes'{$r=[ordered]@{routes=@(Get-NetRoute|ForEach-Object{[ordered]@{destination=$_.DestinationPrefix;next_hop=$_.NextHop;interface=$_.InterfaceAlias;metric=$_.RouteMetric}})}}'dns_lookup'{$x=Resolve-DnsName (A 'host');$r=[ordered]@{answers=@($x|ForEach-Object{[ordered]@{name=$_.Name;type=$_.Type;address=$_.IPAddress;name_host=$_.NameHost}})}}'ping'{$x=Test-Connection (A 'host') -Count 4;$r=[ordered]@{replies=@($x|ForEach-Object{[ordered]@{address=$_.Address;latency_ms=$_.Latency;status=$_.Status}})}}'traceroute'{$text=& tracert.exe -d (A 'host') 2>&1;$r=[ordered]@{output=($text -join [Environment]::NewLine);exit_code=$LASTEXITCODE}}'port_probe'{$x=Test-NetConnection (A 'host') -Port (A 'port') -InformationLevel Detailed;$r=[ordered]@{host=(A 'host');port=(A 'port');reachable=$x.TcpTestSucceeded;remote_address=$x.RemoteAddress.IPAddressToString}}'download'{Invoke-WebRequest -Uri (A 'url') -OutFile (A 'path') -UseBasicParsing;$r=[ordered]@{url=(A 'url');path=[IO.Path]::GetFullPath((A 'path'));bytes=(Get-Item (A 'path')).Length}}} }
  {$_ -like 'windows.firewall.*'} { $op=$action.Substring(17);if($op -eq 'list_rules'){$q=Get-NetFirewallRule;if(A 'name'){$q=$q|Where-Object DisplayName -like ('*'+(A 'name')+'*')};$r=[ordered]@{rules=@($q|ForEach-Object{[ordered]@{name=$_.DisplayName;enabled=$_.Enabled.ToString();direction=$_.Direction.ToString();action=$_.Action.ToString();profile=$_.Profile.ToString()}})}}elseif($op -eq 'upsert_rule'){Remove-NetFirewallRule -DisplayName (A 'name') -ErrorAction SilentlyContinue;$params=@{DisplayName=(A 'name');Direction=(A 'direction' 'Inbound');Action=(A 'action' 'Allow');Protocol=(A 'protocol' 'TCP')};if(A 'local_port'){$params.LocalPort=A 'local_port'};if(A 'program'){$params.Program=A 'program'};New-NetFirewallRule @params|Out-Null;$r=[ordered]@{name=(A 'name');updated=$true}}else{Remove-NetFirewallRule -DisplayName (A 'name');$r=[ordered]@{name=(A 'name');deleted=$true}} }
  {$_ -like 'registry.*'} { $op=$action.Substring(9);$p=A 'path';switch($op){'list'{$r=[ordered]@{items=@(Get-ChildItem -LiteralPath $p -ErrorAction SilentlyContinue|ForEach-Object{[ordered]@{name=$_.PSChildName;path=$_.Name}});values=@((Get-ItemProperty -LiteralPath $p).PSObject.Properties|Where-Object Name -notlike 'PS*'|ForEach-Object{[ordered]@{name=$_.Name;value=$_.Value}})}}'get'{$v=Get-ItemPropertyValue -LiteralPath $p -Name (A 'name');$r=[ordered]@{path=$p;name=(A 'name');value=$v}}'set'{if(-not(Test-Path $p)){New-Item -Path $p -Force|Out-Null};New-ItemProperty -LiteralPath $p -Name (A 'name') -Value (A 'value') -PropertyType (A 'value_type' 'String') -Force|Out-Null;$r=[ordered]@{path=$p;name=(A 'name');set=$true}}'delete'{if(A 'name'){Remove-ItemProperty -LiteralPath $p -Name (A 'name') -Force}else{Remove-Item -LiteralPath $p -Recurse -Force};$r=[ordered]@{path=$p;deleted=$true}}'export'{& reg.exe export $p (A 'file') /y|Out-Null;if($LASTEXITCODE -ne 0){throw 'registry export failed'};$r=[ordered]@{path=$p;file=(A 'file')}}'import'{& reg.exe import (A 'file')|Out-Null;if($LASTEXITCODE -ne 0){throw 'registry import failed'};$r=[ordered]@{file=(A 'file');imported=$true}}} }
  {$_ -like 'scheduled_task.*'} { $op=$action.Substring(15);$n=A 'name';$tp=[string](A 'path' '\');switch($op){'list'{$r=[ordered]@{tasks=@(Get-ScheduledTask|ForEach-Object{[ordered]@{name=$_.TaskName;path=$_.TaskPath;state=$_.State.ToString();author=$_.Author}})}}'get'{$t=Get-ScheduledTask -TaskName $n -TaskPath $tp;$i=Get-ScheduledTaskInfo $t;$r=[ordered]@{name=$t.TaskName;path=$t.TaskPath;state=$t.State.ToString();last_run=$i.LastRunTime;next_run=$i.NextRunTime;last_result=$i.LastTaskResult;actions=@($t.Actions);triggers=@($t.Triggers)}}'create'{$act=New-ScheduledTaskAction -Execute (A 'executable') -Argument (A 'arguments' '');$kind=A 'trigger' 'logon';if($kind -eq 'daily'){$tr=New-ScheduledTaskTrigger -Daily -At ([datetime](A 'at'))}elseif($kind -eq 'once'){$tr=New-ScheduledTaskTrigger -Once -At ([datetime](A 'at'))}else{$tr=New-ScheduledTaskTrigger -AtLogOn};Register-ScheduledTask -TaskName $n -TaskPath $tp -Action $act -Trigger $tr -Force|Out-Null;$r=[ordered]@{name=$n;created=$true}}'update'{$act=New-ScheduledTaskAction -Execute (A 'executable') -Argument (A 'arguments' '');Set-ScheduledTask -TaskName $n -TaskPath $tp -Action $act|Out-Null;$r=[ordered]@{name=$n;updated=$true}}'run'{Start-ScheduledTask -TaskName $n -TaskPath $tp;$r=[ordered]@{name=$n;started=$true}}'enable'{Enable-ScheduledTask -TaskName $n -TaskPath $tp|Out-Null;$r=[ordered]@{name=$n;enabled=$true}}'disable'{Disable-ScheduledTask -TaskName $n -TaskPath $tp|Out-Null;$r=[ordered]@{name=$n;enabled=$false}}'delete'{Unregister-ScheduledTask -TaskName $n -TaskPath $tp -Confirm:$false;$r=[ordered]@{name=$n;deleted=$true}}} }
  {$_ -like 'software.*'} { $op=$action.Substring(9);$args=@();switch($op){'list'{$args=@('list')}'search'{$args=@('search',(A 'query'))}'install'{$args=@('install','--id',(A 'id'),'--exact','--accept-package-agreements','--accept-source-agreements','--disable-interactivity');if(A 'version'){$args+=@('--version',(A 'version'))};if(A 'scope'){$args+=@('--scope',(A 'scope'))}}'uninstall'{$args=@('uninstall','--id',(A 'id'),'--exact','--disable-interactivity')}'upgrade'{$args=@('upgrade','--id',(A 'id'),'--exact','--accept-package-agreements','--accept-source-agreements','--disable-interactivity')}'upgrade_all'{$args=@('upgrade','--all','--accept-package-agreements','--accept-source-agreements','--disable-interactivity')}'sources'{$args=@('source','list')}};$text=& winget @args 2>&1;$r=[ordered]@{command=('winget '+($args -join ' '));output=($text -join [Environment]::NewLine);exit_code=$LASTEXITCODE};if($LASTEXITCODE -ne 0){throw ($text -join [Environment]::NewLine)} }
  {$_ -like 'system.*'} { $op=$action.Substring(7);switch($op){'info'{$x=Get-ComputerInfo;$r=[ordered]@{computer_name=$env:COMPUTERNAME;windows_product=$x.WindowsProductName;windows_version=$x.WindowsVersion;os_build=$x.OsBuildNumber;architecture=$env:PROCESSOR_ARCHITECTURE;bios=$x.BiosSMBIOSBIOSVersion}}'os_version'{$x=Get-CimInstance Win32_OperatingSystem;$r=[ordered]@{caption=$x.Caption;version=$x.Version;build=$x.BuildNumber;architecture=$x.OSArchitecture;installed_at=$x.InstallDate}}'cpu'{$r=[ordered]@{processors=@(Get-CimInstance Win32_Processor|ForEach-Object{[ordered]@{name=$_.Name;cores=$_.NumberOfCores;logical_processors=$_.NumberOfLogicalProcessors;load_percent=$_.LoadPercentage;max_mhz=$_.MaxClockSpeed}})}}'memory'{$x=Get-CimInstance Win32_OperatingSystem;$r=[ordered]@{total_bytes=[int64]$x.TotalVisibleMemorySize*1024;free_bytes=[int64]$x.FreePhysicalMemory*1024;used_bytes=[int64]($x.TotalVisibleMemorySize-$x.FreePhysicalMemory)*1024}}'disks'{$r=[ordered]@{disks=@(Get-CimInstance Win32_LogicalDisk|ForEach-Object{[ordered]@{name=$_.DeviceID;type=$_.DriveType;filesystem=$_.FileSystem;size=$_.Size;free=$_.FreeSpace;volume=$_.VolumeName}})}}'uptime'{$x=Get-CimInstance Win32_OperatingSystem;$r=[ordered]@{boot_time=$x.LastBootUpTime;uptime_seconds=[int64]((Get-Date)-$x.LastBootUpTime).TotalSeconds}}'users'{$r=[ordered]@{users=@(Get-LocalUser|ForEach-Object{[ordered]@{name=$_.Name;enabled=$_.Enabled;last_logon=$_.LastLogon;description=$_.Description}})}}'groups'{$r=[ordered]@{groups=@(Get-LocalGroup|ForEach-Object{[ordered]@{name=$_.Name;description=$_.Description}})}}'env.list'{$scope=A 'scope' 'Process';$vars=[Environment]::GetEnvironmentVariables($scope);$items=@{};foreach($k in $vars.Keys){$items[$k]=$vars[$k]};$r=[ordered]@{scope=$scope;variables=$items}}'env.get'{$scope=A 'scope' 'Process';$r=[ordered]@{name=(A 'name');scope=$scope;value=[Environment]::GetEnvironmentVariable((A 'name'),$scope)}}'env.set'{$scope=A 'scope' 'User';[Environment]::SetEnvironmentVariable((A 'name'),(A 'value'),$scope);$r=[ordered]@{name=(A 'name');scope=$scope;set=$true}}'env.delete'{$scope=A 'scope' 'User';[Environment]::SetEnvironmentVariable((A 'name'),$null,$scope);$r=[ordered]@{name=(A 'name');scope=$scope;deleted=$true}}} }
  {$_ -like 'windows.eventlog.*'} { $op=$action.Substring(17);switch($op){'list_logs'{$r=[ordered]@{logs=@(Get-WinEvent -ListLog * -ErrorAction SilentlyContinue|ForEach-Object{[ordered]@{name=$_.LogName;records=$_.RecordCount;enabled=$_.IsEnabled;size=$_.FileSize}})}}'query'{$filter=@{LogName=(A 'log_name' 'System')};if(A 'provider'){$filter.ProviderName=A 'provider'};if(A 'level'){$filter.Level=A 'level'};if(A 'since'){$filter.StartTime=[datetime](A 'since')};$max=[int](A 'max_events' 100);$r=[ordered]@{events=@(Get-WinEvent -FilterHashtable $filter -MaxEvents $max|ForEach-Object{[ordered]@{id=$_.Id;record_id=$_.RecordId;level=$_.LevelDisplayName;provider=$_.ProviderName;created_at=$_.TimeCreated;message=$_.Message}})}}'export'{& wevtutil.exe epl (A 'log_name') (A 'path');if($LASTEXITCODE -ne 0){throw 'event log export failed'};$r=[ordered]@{log_name=(A 'log_name');path=(A 'path')}}'clear'{Clear-EventLog -LogName (A 'log_name');$r=[ordered]@{log_name=(A 'log_name');cleared=$true}}} }
  'notify.toast' { Add-Type -AssemblyName System.Windows.Forms;$n=[Windows.Forms.NotifyIcon]::new();$n.Icon=[Drawing.SystemIcons]::Information;$n.Visible=$true;$n.BalloonTipTitle=[string](A 'title' 'Steward');$n.BalloonTipText=[string](A 'message');$n.ShowBalloonTip([int](A 'timeout_seconds' 5)*1000);Start-Sleep -Milliseconds 800;$n.Dispose();$r=[ordered]@{shown=$true;title=(A 'title' 'Steward')} }
  'notify.sound' { $name=[string](A 'sound' 'Asterisk');$sound=[Media.SystemSounds]::$name;if(-not $sound){$sound=[Media.SystemSounds]::Asterisk};$sound.Play();$r=[ordered]@{sound=$name;played=$true} }
  'power.lock' { & rundll32.exe user32.dll,LockWorkStation;$r=[ordered]@{locked=$true} }
  'power.sleep' { Add-Type -AssemblyName System.Windows.Forms;[Windows.Forms.Application]::SetSuspendState([Windows.Forms.PowerState]::Suspend,$false,$false);$r=[ordered]@{sleep_requested=$true} }
  'power.restart' { $d=[int](A 'delay_seconds' 0);& shutdown.exe /r /t $d $(if(A 'force' $false){'/f'});$r=[ordered]@{restart_requested=$true;delay_seconds=$d} }
  'power.shutdown' { $d=[int](A 'delay_seconds' 0);& shutdown.exe /s /t $d $(if(A 'force' $false){'/f'});$r=[ordered]@{shutdown_requested=$true;delay_seconds=$d} }
  default { throw "unsupported Windows foundation action: $action" }
 }
 [ordered]@{ok=$true;output=(Obj $r);evidence=@([ordered]@{kind='windows_tool';summary=($action+' completed');payload=[ordered]@{action=$action}})}|ConvertTo-Json -Depth 100 -Compress
} catch { [ordered]@{ok=$false;output=@{};error=$_.Exception.Message;evidence=@()}|ConvertTo-Json -Depth 20 -Compress }
`

func windowsFoundationToolCount() int { return len(windowsFoundationToolDefinitions()) }

func windowsToolPackagePath(root, name string) string { return filepath.Join(root, name, "1.0.0") }

func windowsToolDescription(name string) string {
	return fmt.Sprintf("Windows foundation tool %s", name)
}

func windowsToolRootFromEnv() string { return os.Getenv("STEWARD_TOOL_ROOT") }
