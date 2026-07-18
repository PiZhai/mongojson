[CmdletBinding()]
param(
  [Parameter(Mandatory = $true)][string]$SourceDir,
  [Parameter(Mandatory = $true)][string]$DatabaseURL,
  [string]$BrokerPolicyPath = "",
  [string]$InstallDir = "C:\Program Files\MongojsonSteward",
  [string]$DataDir = "C:\ProgramData\MongojsonSteward",
  [string]$BrokerInstallDir = "C:\Program Files\MongoJSON\StewardBroker",
  [string]$BrokerDataDir = "C:\ProgramData\MongoJSON\StewardBroker",
  [string]$ServiceName = "MongojsonSteward",
  [string]$BrokerServiceName = "MongojsonStewardBroker",
  [string]$CompanionTaskName = "MongojsonStewardCompanion",
  [string]$HTTPAddress = "127.0.0.1:18080",
  [string]$PeerHTTPAddress = "127.0.0.1:18081",
  [string]$AgentID = $env:COMPUTERNAME,
  [string]$SyncSecret = "",
  [string]$DevicePrivateKey = "",
  [string]$DevicePublicKey = "",
  [string]$SyncEncryptionKey = "",
  [string]$SyncEncryptionKeyID = "",
  [string]$SyncEncryptionPreviousKeys = "",
  [string]$LocalEncryptionKey = "",
  [string]$LocalEncryptionKeyID = "",
  [string]$LocalEncryptionPreviousKeys = "",
  [string]$OrchestrationSigningKey = "",
  [string]$ManagementAuthToken = "",
  [string]$ManagementAccessTokenFile = "",
  [string]$LLMBaseURL = "",
  [string]$LLMModel = "",
  [string]$LLMAPIKey = "",
  [switch]$RecoverModelSettingsFromEnvironment,
  [switch]$AllowUnsignedPackage,
  [switch]$AllowDirtyPackage,
  [string]$TrustedSignerThumbprint = "",
  [switch]$SkipCertificateRevocationCheck,
  [string]$ReleaseStagingRoot = (Join-Path $env:ProgramData 'MongojsonStewardReleaseStaging'),
  [switch]$InstallCompanion,
  [switch]$Start,
  [switch]$Verify
)

$ErrorActionPreference = "Stop"
function Test-Administrator {
  $p=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  return $p.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function New-Key([int]$Length=32) { $b=New-Object byte[] $Length; $rng=[Security.Cryptography.RandomNumberGenerator]::Create();try{$rng.GetBytes($b)}finally{$rng.Dispose()};return [Convert]::ToBase64String($b) }
function Assert-Base64Key([string]$Value,[int]$Length,[string]$Name) {
  try{$bytes=[Convert]::FromBase64String($Value)}catch{throw "$Name must be base64 encoding of exactly $Length random bytes"}
  if($bytes.Length -ne $Length){throw "$Name must be base64 encoding of exactly $Length random bytes"}
}
function Assert-Loopback([string]$Address,[string]$Name) {
  $hostPart=($Address -split ':')[0].Trim('[',']')
  if ($hostPart -notin @('127.0.0.1','localhost','::1')) { throw "$Name must bind to loopback: $Address" }
}
function Get-CanonicalPath([string]$Path,[string]$Name,[bool]$MustExist=$false) {
  if([string]::IsNullOrWhiteSpace($Path)){throw "$Name must not be empty"}
  if([WildcardPattern]::ContainsWildcardCharacters($Path)){throw "$Name must not contain wildcard characters"}
  if(-not[IO.Path]::IsPathRooted($Path)){throw "$Name must be absolute"}
  try{$full=[IO.Path]::GetFullPath($Path);if($full.Length -gt [IO.Path]::GetPathRoot($full).Length){$full=$full.TrimEnd('\')}}catch{throw "$Name is not a valid absolute path"}
  if($MustExist -and -not(Test-Path -LiteralPath $full)){throw "$Name does not exist: $full"}
  return $full
}
function Assert-SafeSystemName([string]$Value,[string]$Name) {if($Value -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$'){throw "$Name contains unsupported characters"}}
function Assert-DedicatedChildPath([string]$Path,[string]$Root,[string]$Name) {
  $full=Get-CanonicalPath $Path $Name $false
  $rootFull=Get-CanonicalPath $Root "$Name root" $true
  if($full.Equals($rootFull,[StringComparison]::OrdinalIgnoreCase) -or -not $full.StartsWith($rootFull+'\',[StringComparison]::OrdinalIgnoreCase)){
    throw "$Name must be a dedicated child below $rootFull"
  }
  if((Split-Path -Leaf $full) -in @('.','..')){throw "$Name must name a dedicated leaf directory"}
  return $full
}
function Assert-NoReparseAncestors([string]$Path,[string]$Name) {
  $current=Get-CanonicalPath $Path $Name $false
  while($true){
    if(Test-Path -LiteralPath $current){
      $item=Get-Item -LiteralPath $current -Force
      if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Name contains a reparse point at $current"}
    }
    $parent=Split-Path -Parent $current
    if([string]::IsNullOrWhiteSpace($parent) -or $parent -eq $current){break}
    $current=$parent
  }
}
function Assert-DisjointPaths([hashtable]$Paths) {
  $entries=@($Paths.GetEnumerator())
  for($i=0;$i -lt $entries.Count;$i++){
    for($j=$i+1;$j -lt $entries.Count;$j++){
      $left=[string]$entries[$i].Value;$right=[string]$entries[$j].Value
      if($left.Equals($right,[StringComparison]::OrdinalIgnoreCase) -or $left.StartsWith($right+'\',[StringComparison]::OrdinalIgnoreCase) -or $right.StartsWith($left+'\',[StringComparison]::OrdinalIgnoreCase)){
        throw "$($entries[$i].Key) and $($entries[$j].Key) must not overlap"
      }
    }
  }
}
function Test-DirectoryNotEmpty([string]$Path) {
  return (Test-Path -LiteralPath $Path) -and $null -ne (Get-ChildItem -LiteralPath $Path -Force|Select-Object -First 1)
}
function Remove-DirectoryWithRetry([string]$Path) {
  if(-not(Test-Path -LiteralPath $Path)){return}
  $lastError=''
  for($attempt=0;$attempt -lt 10;$attempt++){
    try{Remove-Item -LiteralPath $Path -Recurse -Force;return}catch{$lastError=$_.Exception.Message;Start-Sleep -Milliseconds (200*($attempt+1))}
  }
  throw "failed to remove directory: $lastError"
}
function Remove-ServiceAndWait([string]$Name) {
  if(-not(Get-Service -Name $Name -ErrorAction SilentlyContinue)){return}
  Stop-Service -Name $Name -Force -ErrorAction SilentlyContinue
  & sc.exe delete $Name|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to delete Windows service $Name"}
  for($i=0;$i -lt 50 -and (Get-Service -Name $Name -ErrorAction SilentlyContinue);$i++){Start-Sleep -Milliseconds 200}
  if(Get-Service -Name $Name -ErrorAction SilentlyContinue){throw "Windows service is still pending deletion: $Name"}
}
function Get-PathSnapshot([string]$Path,[string]$SnapshotRoot,[string]$Name) {
  $exists=Test-Path -LiteralPath $Path
  $snapshot=[ordered]@{path=$Path;exists=$exists;is_directory=$false;sddl='';copy=''}
  if(-not $exists){return [pscustomobject]$snapshot}
  $item=Get-Item -LiteralPath $Path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Name must not be a reparse point: $Path"}
  $snapshot.is_directory=$item.PSIsContainer
  $snapshot.sddl=(Get-Acl -LiteralPath $Path).Sddl
  if(-not $item.PSIsContainer){
    $copy=Join-Path $SnapshotRoot ($Name+'-'+[guid]::NewGuid().ToString('N')+'.bin')
    Copy-Item -LiteralPath $Path -Destination $copy
    Protect-AdminFile $copy
    $snapshot.copy=$copy
  }
  return [pscustomobject]$snapshot
}
function Set-PathSecurityDescriptor([string]$Path,[string]$Sddl,[bool]$Directory) {
  if([string]::IsNullOrWhiteSpace($Sddl)){return}
  $acl=if($Directory){[Security.AccessControl.DirectorySecurity]::new()}else{[Security.AccessControl.FileSecurity]::new()}
  $acl.SetSecurityDescriptorSddlForm($Sddl)
  Set-Acl -LiteralPath $Path -AclObject $acl
}
function Restore-PathSnapshot([object]$Snapshot) {
  if($null -eq $Snapshot){return}
  if(-not[bool]$Snapshot.exists){
    if(Test-Path -LiteralPath ([string]$Snapshot.path) -PathType Leaf){Remove-Item -LiteralPath ([string]$Snapshot.path) -Force}
    return
  }
  if(-not[bool]$Snapshot.is_directory){Copy-Item -LiteralPath ([string]$Snapshot.copy) -Destination ([string]$Snapshot.path) -Force}
  Set-PathSecurityDescriptor ([string]$Snapshot.path) ([string]$Snapshot.sddl) ([bool]$Snapshot.is_directory)
}
function Write-TransactionRecord([string]$Path,[string]$State,[hashtable]$Details=@{}) {
  $payload=[ordered]@{schema='mongojson.steward.install-transaction/v1';state=$State;updated_at=[DateTimeOffset]::UtcNow.ToString('o');details=$Details}
  [IO.File]::WriteAllText($Path,($payload|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
  Protect-AdminFile $Path
}
function ConvertTo-RedactedText([string]$Text,[string[]]$Secrets) {
  $safe=[string]$Text
  foreach($secret in $Secrets){if(-not[string]::IsNullOrWhiteSpace($secret)){$safe=$safe.Replace($secret,'[REDACTED]')}}
  return [regex]::Replace($safe,'(?i)(postgres(?:ql)?://[^:/\s]+:)[^@\s]+@','$1[REDACTED]@')
}
function Protect-AdminPath([string]$Path) {
  $item=Get-Item -LiteralPath $Path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "administrator-protected path must not be a reparse point: $Path"}
  & icacls.exe $Path /inheritance:r /grant:r "*S-1-5-18:(OI)(CI)F" "*S-1-5-32-544:(OI)(CI)F" | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to protect $Path" }
}
function Protect-AdminFile([string]$Path) {
  $item=Get-Item -LiteralPath $Path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "administrator-protected file must not be a reparse point: $Path"}
  & icacls.exe $Path /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' | Out-Null
  if ($LASTEXITCODE -ne 0) { throw "failed to protect $Path" }
}
function Protect-AdminTree([string]$Root) {
  $system=[Security.Principal.SecurityIdentifier]::new('S-1-5-18')
  $administrators=[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
  $allow=[Security.AccessControl.AccessControlType]::Allow
  $full=[Security.AccessControl.FileSystemRights]::FullControl
  $inheritance=[Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
  $noneInheritance=[Security.AccessControl.InheritanceFlags]::None
  $nonePropagation=[Security.AccessControl.PropagationFlags]::None
  foreach($directory in @((Get-Item -LiteralPath $Root -Force))+@(Get-ChildItem -LiteralPath $Root -Force -Recurse -Directory)){
    $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($system,$full,$inheritance,$nonePropagation,$allow))
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($administrators,$full,$inheritance,$nonePropagation,$allow))
    Set-Acl -LiteralPath $directory.FullName -AclObject $acl
  }
  foreach($file in @(Get-ChildItem -LiteralPath $Root -Force -Recurse -File)){
    $acl=[Security.AccessControl.FileSecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($system,$full,$noneInheritance,$nonePropagation,$allow))
    $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($administrators,$full,$noneInheritance,$nonePropagation,$allow))
    Set-Acl -LiteralPath $file.FullName -AclObject $acl
  }
}
function Protect-MainInstallTree([string]$Root,[string]$Name) {
  Assert-NoReparsePoints $Root 'main installation tree'
  $system=[Security.Principal.SecurityIdentifier]::new('S-1-5-18')
  $administrators=[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544')
  $localService=[Security.Principal.SecurityIdentifier]::new('S-1-5-19')
  $serviceSID=(New-Object Security.Principal.NTAccount("NT SERVICE\$Name")).Translate([Security.Principal.SecurityIdentifier])
  $allow=[Security.AccessControl.AccessControlType]::Allow
  $full=[Security.AccessControl.FileSystemRights]::FullControl
  $readExecute=[Security.AccessControl.FileSystemRights]::ReadAndExecute
  $inheritance=[Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit'
  $noneInheritance=[Security.AccessControl.InheritanceFlags]::None
  $nonePropagation=[Security.AccessControl.PropagationFlags]::None
  foreach($directory in @((Get-Item -LiteralPath $Root -Force))+@(Get-ChildItem -LiteralPath $Root -Force -Recurse -Directory)){
    $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    foreach($entry in @(@($system,$full),@($administrators,$full),@($localService,$readExecute),@($serviceSID,$readExecute))){
      $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($entry[0],$entry[1],$inheritance,$nonePropagation,$allow))
    }
    Set-Acl -LiteralPath $directory.FullName -AclObject $acl
  }
  foreach($file in @(Get-ChildItem -LiteralPath $Root -Force -Recurse -File)){
    $acl=[Security.AccessControl.FileSecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    foreach($entry in @(@($system,$full),@($administrators,$full),@($localService,$readExecute),@($serviceSID,$readExecute))){
      $acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($entry[0],$entry[1],$noneInheritance,$nonePropagation,$allow))
    }
    Set-Acl -LiteralPath $file.FullName -AclObject $acl
  }
  foreach($required in @((Join-Path $Root 'steward.exe'),(Join-Path $Root 'ui\index.html'))){
    if(-not(Test-Path -LiteralPath $required -PathType Leaf)){throw "required service-readable release file is missing: $required"}
    $rules=(Get-Acl -LiteralPath $required).GetAccessRules($true,$false,[Security.Principal.SecurityIdentifier])
    foreach($sid in @($localService,$serviceSID)){
      $matched=@($rules|Where-Object{$_.IdentityReference -eq $sid -and $_.AccessControlType -eq $allow -and (($_.FileSystemRights -band $readExecute) -eq $readExecute)})
      if($matched.Count -eq 0){throw "service read/execute ACL verification failed for $required and SID $sid"}
    }
  }
}
function Normalize-SignerThumbprint([string]$Value) {
  $normalized=($Value -replace '\s','').ToUpperInvariant()
  if($normalized -and $normalized -notmatch '^[A-F0-9]{40}$'){throw 'TrustedSignerThumbprint must be a 40-character certificate thumbprint'}
  return $normalized
}
function Assert-NoReparsePoints([string]$Root,[string]$Label) {
  $rootItem=Get-Item -LiteralPath $Root -Force
  if(($rootItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Label root must not be a reparse point: $Root"}
  $reparse=@(Get-ChildItem -LiteralPath $Root -Force -Recurse|Where-Object{($_.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0})
  if($reparse.Count -gt 0){throw "$Label contains a reparse point: $($reparse[0].FullName)"}
}
function New-ProtectedReleaseStage([string]$InputDir,[string]$StagingRoot) {
  $input=(Resolve-Path -LiteralPath $InputDir).Path
  Assert-NoReparsePoints $input 'release source'
  New-Item -ItemType Directory -Force -Path $StagingRoot|Out-Null
  Protect-AdminPath $StagingRoot
  $stage=Join-Path ([IO.Path]::GetFullPath($StagingRoot)) ('install-'+[guid]::NewGuid().ToString('N'))
  New-Item -ItemType Directory -Path $stage|Out-Null
  Protect-AdminPath $stage
  try{
    Get-ChildItem -LiteralPath $input -Force|Copy-Item -Destination $stage -Recurse -Force
    Assert-NoReparsePoints $stage 'staged release'
    Protect-AdminTree $stage
    return $stage
  }catch{
    Remove-Item -LiteralPath $stage -Recurse -Force -ErrorAction SilentlyContinue
    throw
  }
}
function Assert-StagedVerifierTrust([string]$Verifier,[string]$TrustedThumbprint,[bool]$UnsignedOverride) {
  if($TrustedThumbprint){
    $signature=Get-AuthenticodeSignature -LiteralPath $Verifier
    $actual=if($signature.SignerCertificate){($signature.SignerCertificate.Thumbprint -replace '\s','').ToUpperInvariant()}else{''}
    if($signature.Status -eq [Management.Automation.SignatureStatus]::Valid -and $actual -eq $TrustedThumbprint){return}
    if(-not $UnsignedOverride){throw "staged verifier is not validly signed by trusted signer '$TrustedThumbprint': status=$($signature.Status); signer=$actual"}
    Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: staged verifier signature does not match the requested trust pin.'
    return
  }elseif(-not $UnsignedOverride){throw 'TrustedSignerThumbprint is required for production installation'}
  else{Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: the staged verifier is unsigned and is being executed from an administrator-protected staging directory.'}
}
function Write-ReleaseTrust([string]$Root,[string]$TrustedThumbprint,[object]$Manifest) {
  $path=Join-Path $Root 'release-trust.json'
  $builtAt=[DateTimeOffset]::MinValue
  if($Manifest.built_at -is [DateTime]){$builtAt=[DateTimeOffset]([DateTime]$Manifest.built_at)}elseif(-not[DateTimeOffset]::TryParse([string]$Manifest.built_at,[ref]$builtAt)){throw 'verified release manifest has an invalid built_at timestamp'}
  $manifestPath=Join-Path $Root 'release-manifest.json'
  if(-not(Test-Path -LiteralPath $manifestPath -PathType Leaf)){throw 'verified release is missing release-manifest.json'}
  $authenticatedFiles=[Collections.Generic.List[object]]::new()
  $seen=@{}
  foreach($record in @($Manifest.files)){
    $relative=([string]$record.path -replace '\\','/')
    if([string]::IsNullOrWhiteSpace($relative) -or [IO.Path]::IsPathRooted($relative) -or $relative.StartsWith('/') -or $relative -match '(^|/)\.\.?(/|$)' -or $relative.Contains('//') -or $relative.EndsWith('/')){throw 'verified release manifest contains an unsafe file path'}
    $key=$relative.ToLowerInvariant();if($seen.ContainsKey($key)){throw "verified release manifest contains a duplicate file path: $relative"};$seen[$key]=$true
    $full=[IO.Path]::GetFullPath((Join-Path $Root ($relative -replace '/','\')))
    if(-not(Test-Path -LiteralPath $full -PathType Leaf)){throw "verified release payload is missing: $relative"}
    $actual=(Get-FileHash -Algorithm SHA256 -LiteralPath $full).Hash.ToLowerInvariant()
    if($actual -ne ([string]$record.sha256).ToLowerInvariant()){throw "verified release payload changed before trust baseline creation: $relative"}
    $authenticatedFiles.Add([ordered]@{path=$relative;sha256=$actual;kind='payload'})
  }
  $metadata=@([ordered]@{path='release-manifest.json';kind='manifest'},[ordered]@{path='SHA256SUMS.txt';kind='checksums'})
  $signatureRequired=[bool]$Manifest.signing.required
  $signaturePath=([string]$Manifest.signing.manifest_signature -replace '\\','/')
  $catalogPath=([string]$Manifest.signing.package_catalog -replace '\\','/')
  if($signatureRequired){
    if(-not $TrustedThumbprint){throw 'signed release trust baseline requires a pinned signer thumbprint'}
    if([string]::IsNullOrWhiteSpace($signaturePath) -or [string]::IsNullOrWhiteSpace($catalogPath) -or [IO.Path]::IsPathRooted($signaturePath) -or [IO.Path]::IsPathRooted($catalogPath) -or $signaturePath.StartsWith('/') -or $catalogPath.StartsWith('/') -or $signaturePath -match '(^|/)\.\.?(/|$)' -or $catalogPath -match '(^|/)\.\.?(/|$)'){throw 'signed Windows release is missing safe manifest signature or package catalog metadata'}
    $metadata+=@([ordered]@{path=$signaturePath;kind='manifest_signature'},[ordered]@{path=$catalogPath;kind='package_catalog'})
  }
  foreach($record in $metadata){
    $relative=[string]$record.path;$key=$relative.ToLowerInvariant();if($seen.ContainsKey($key)){throw "release trust metadata path collides with payload: $relative"};$seen[$key]=$true
    $full=[IO.Path]::GetFullPath((Join-Path $Root ($relative -replace '/','\')))
    if(-not(Test-Path -LiteralPath $full -PathType Leaf)){throw "verified release metadata is missing: $relative"}
    $authenticatedFiles.Add([ordered]@{path=$relative;sha256=(Get-FileHash -Algorithm SHA256 -LiteralPath $full).Hash.ToLowerInvariant();kind=[string]$record.kind})
  }
  $payload=[ordered]@{
    schema='mongojson.steward.release-trust/v2';signer_thumbprint=$TrustedThumbprint
    release_version=[string]$Manifest.version;release_commit=[string]$Manifest.commit;release_built_at=$builtAt.ToUniversalTime().ToString('o');release_built_at_utc_ticks=$builtAt.UtcTicks.ToString([Globalization.CultureInfo]::InvariantCulture)
    signature_required=$signatureRequired;manifest_signature_path=$signaturePath;package_catalog_path=$catalogPath
    undeclared_files_policy='deny';authenticated_files=@($authenticatedFiles|Sort-Object path);recorded_at=[DateTimeOffset]::UtcNow.ToString('o')
  }
  [IO.File]::WriteAllText($path,($payload|ConvertTo-Json -Depth 8),[Text.UTF8Encoding]::new($false))
  Protect-AdminFile $path
  return $path
}
function Write-CurrentUserSecretFile([string]$Path,[string]$Value) {
  $localAppData=[IO.Path]::GetFullPath([Environment]::GetFolderPath('LocalApplicationData')).TrimEnd('\')+'\'
  $full=[IO.Path]::GetFullPath($Path)
  if(-not $full.StartsWith($localAppData,[StringComparison]::OrdinalIgnoreCase)){throw "management access token file must be under the installing user's LocalAppData: $full"}
  Assert-NoReparseAncestors $full 'management access token path'
  $directory=Split-Path -Parent $full
  New-Item -ItemType Directory -Force -Path $directory|Out-Null
  Assert-NoReparseAncestors $full 'management access token path'
  $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  & icacls.exe $directory /inheritance:r /grant:r "*${sid}:(OI)(CI)F" '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to protect current-user secret directory $directory"}
  $temporary="$full.tmp-$([Guid]::NewGuid().ToString('N'))"
  try{
    [IO.File]::WriteAllText($temporary,$Value+"`r`n",[Text.UTF8Encoding]::new($false))
    Move-Item -LiteralPath $temporary -Destination $full -Force
  }finally{if(Test-Path -LiteralPath $temporary){Remove-Item -LiteralPath $temporary -Force}}
  & icacls.exe $full /inheritance:r /grant:r "*${sid}:F" '*S-1-5-18:F' '*S-1-5-32-544:F'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to protect current-user secret file $full"}
  & icacls.exe $full /setowner "*${sid}"|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to set current-user ownership on secret file $full"}
  return $full
}
function Grant-RestrictedCapabilityHostReadExecute([string]$Executable) {
  # The production capability token retains the LocalSystem user SID so native
  # and CLR runtimes can initialize, but disables Administrators, all service
  # SIDs and optional privileges. Only this immutable hash-pinned host receives
  # SYSTEM RX; Broker policy, keys, state and audit deliberately do not.
  & icacls.exe $Executable /grant '*S-1-5-18:(RX)' '*S-1-5-12:(RX)'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to grant the production capability token read/execute access to $Executable"}
}
function Set-ServicePublicEnvironmentValue([string]$Name,[string]$Key,[string]$Value) {
  $path="HKLM:\SYSTEM\CurrentControlSet\Services\$Name"
  $entries=@((Get-ItemProperty -LiteralPath $path -Name Environment -ErrorAction Stop).Environment)
  $updated=[Collections.Generic.List[string]]::new();$replaced=$false
  foreach($entry in $entries){
    if(([string]$entry).StartsWith("$Key=",[StringComparison]::OrdinalIgnoreCase)){
      $updated.Add("$Key=$Value");$replaced=$true
    }else{$updated.Add([string]$entry)}
  }
  if(-not $replaced){$updated.Add("$Key=$Value")}
  Set-ItemProperty -LiteralPath $path -Name Environment -Value ([string[]]$updated)
}
function Move-OrphanedBrokerData([string]$Path,[string]$Reason) {
  if(-not(Test-Path -LiteralPath $Path)){return ''}
  $programData=[IO.Path]::GetFullPath($env:ProgramData).TrimEnd('\')+'\'
  $full=[IO.Path]::GetFullPath($Path).TrimEnd('\')
  if(-not $full.StartsWith($programData,[StringComparison]::OrdinalIgnoreCase)){throw "refusing to quarantine Broker data outside ProgramData: $full"}
  $target="$full.$Reason-$(Get-Date -Format yyyyMMdd-HHmmss)-$([guid]::NewGuid().ToString('N'))"
  Move-Item -LiteralPath $full -Destination $target -Force
  return $target
}

if (-not (Test-Administrator)) { throw "Run this installer from an elevated PowerShell session." }
Assert-SafeSystemName $ServiceName 'ServiceName';Assert-SafeSystemName $BrokerServiceName 'BrokerServiceName';Assert-SafeSystemName $CompanionTaskName 'CompanionTaskName'
Assert-Loopback $HTTPAddress "HTTPAddress"; Assert-Loopback $PeerHTTPAddress "PeerHTTPAddress"
$InstallDir=Assert-DedicatedChildPath $InstallDir $env:ProgramFiles 'InstallDir'
$BrokerInstallDir=Assert-DedicatedChildPath $BrokerInstallDir $env:ProgramFiles 'BrokerInstallDir'
$DataDir=Assert-DedicatedChildPath $DataDir $env:ProgramData 'DataDir'
$BrokerDataDir=Assert-DedicatedChildPath $BrokerDataDir $env:ProgramData 'BrokerDataDir'
$ReleaseStagingRoot=Assert-DedicatedChildPath $ReleaseStagingRoot $env:ProgramData 'ReleaseStagingRoot'
$sourceInput=Get-CanonicalPath $SourceDir 'SourceDir' $true
if(-not(Test-Path -LiteralPath $sourceInput -PathType Container)){throw 'SourceDir must be a directory'}
foreach($entry in @(@($InstallDir,'InstallDir'),@($BrokerInstallDir,'BrokerInstallDir'),@($DataDir,'DataDir'),@($BrokerDataDir,'BrokerDataDir'),@($ReleaseStagingRoot,'ReleaseStagingRoot'),@($sourceInput,'SourceDir'))){Assert-NoReparseAncestors $entry[0] $entry[1]}
foreach($entry in @(@((Join-Path $DataDir 'config\service-secrets.json'),'private environment path'),@((Join-Path $DataDir 'installation.json'),'installation marker path'),@((Join-Path $DataDir 'data'),'storage path'),@((Join-Path $DataDir 'logs'),'log path'))){Assert-NoReparseAncestors $entry[0] $entry[1]}
Assert-DisjointPaths @{InstallDir=$InstallDir;BrokerInstallDir=$BrokerInstallDir;DataDir=$DataDir;BrokerDataDir=$BrokerDataDir;ReleaseStagingRoot=$ReleaseStagingRoot}
Assert-DisjointPaths @{InstallDir=$InstallDir;BrokerInstallDir=$BrokerInstallDir;DataDir=$DataDir;BrokerDataDir=$BrokerDataDir;SourceDir=$sourceInput}
if(Test-DirectoryNotEmpty $InstallDir){throw "InstallDir must be empty for a first installation: $InstallDir"}
if(Test-DirectoryNotEmpty $BrokerInstallDir){throw "BrokerInstallDir must be empty for a first installation: $BrokerInstallDir"}
$existingMarker=Join-Path $DataDir 'installation.json'
if(Test-Path -LiteralPath $existingMarker){throw "an installation marker already exists; use the production updater or migration recovery: $existingMarker"}
if(Get-ScheduledTask -TaskName $CompanionTaskName -ErrorAction SilentlyContinue){throw "Session Companion task already exists; remove or migrate it before a first installation: $CompanionTaskName"}
$trustedSigner=Normalize-SignerThumbprint $TrustedSignerThumbprint
$source=$null;$releaseStage=$null;$releaseManifest=$null;$releaseTrustPath='';$policy=$null;$orphanedBrokerData='';$transactionPath='';$transactionDir=''
$createdMain=$false;$createdBroker=$false;$companionInstalled=$false;$managementAccessFileCreated=$false;$brokerDataPrepared=$false;$installMutationStarted=$false
$rollbackErrors=[Collections.Generic.List[string]]::new();$sensitiveValues=[Collections.Generic.List[string]]::new()
$installDirExisted=Test-Path -LiteralPath $InstallDir;$brokerInstallDirExisted=Test-Path -LiteralPath $BrokerInstallDir
$installDirSddl=if($installDirExisted){(Get-Acl -LiteralPath $InstallDir).Sddl}else{''}
$brokerInstallDirSddl=if($brokerInstallDirExisted){(Get-Acl -LiteralPath $BrokerInstallDir).Sddl}else{''}
$dataDirExisted=Test-Path -LiteralPath $DataDir;$dataDirSddl=if($dataDirExisted){(Get-Acl -LiteralPath $DataDir).Sddl}else{''}
$storageDirExisted=Test-Path -LiteralPath (Join-Path $DataDir 'data');$logsDirExisted=Test-Path -LiteralPath (Join-Path $DataDir 'logs');$configDirExisted=Test-Path -LiteralPath (Join-Path $DataDir 'config')
$storageDirSddl=if($storageDirExisted){(Get-Acl -LiteralPath (Join-Path $DataDir 'data')).Sddl}else{''};$logsDirSddl=if($logsDirExisted){(Get-Acl -LiteralPath (Join-Path $DataDir 'logs')).Sddl}else{''};$configDirSddl=if($configDirExisted){(Get-Acl -LiteralPath (Join-Path $DataDir 'config')).Sddl}else{''}
$privateSnapshot=$null;$markerSnapshot=$null;$managementSnapshot=$null;$protectedPolicyInput=''
$installationSucceeded=$false
$managementAccessPath=$ManagementAccessTokenFile
if(-not $managementAccessPath){$managementAccessPath=Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'MongojsonSteward\management-access-token.txt'}
$managementAccessPath=Get-CanonicalPath $managementAccessPath 'ManagementAccessTokenFile' $false
$localAppData=Get-CanonicalPath ([Environment]::GetFolderPath('LocalApplicationData')) 'LocalApplicationData' $true
if(-not $managementAccessPath.StartsWith($localAppData+'\',[StringComparison]::OrdinalIgnoreCase)){throw "ManagementAccessTokenFile must be a dedicated file below $localAppData"}
Assert-NoReparseAncestors $managementAccessPath 'ManagementAccessTokenFile'
try {
  $releaseStage=New-ProtectedReleaseStage $sourceInput $ReleaseStagingRoot
  $source=$releaseStage
  $transactionRoot=Join-Path $ReleaseStagingRoot 'transactions';New-Item -ItemType Directory -Force -Path $transactionRoot|Out-Null;Protect-AdminPath $transactionRoot
  $transactionDir=Join-Path $transactionRoot ('install-'+[guid]::NewGuid().ToString('N'));New-Item -ItemType Directory -Path $transactionDir|Out-Null;Protect-AdminPath $transactionDir
  $transactionPath=Join-Path $transactionDir 'install-transaction.json'
  Write-TransactionRecord $transactionPath 'validating' @{install_dir=$InstallDir;data_dir=$DataDir;broker_install_dir=$BrokerInstallDir;broker_data_dir=$BrokerDataDir}
  $snapshotRoot=Join-Path $transactionDir 'rollback'
  New-Item -ItemType Directory -Path $snapshotRoot|Out-Null
  Protect-AdminPath $snapshotRoot
  $privateSnapshot=Get-PathSnapshot (Join-Path $DataDir 'config\service-secrets.json') $snapshotRoot 'private-environment'
  $markerSnapshot=Get-PathSnapshot $existingMarker $snapshotRoot 'installation-marker'
  $managementSnapshot=Get-PathSnapshot $managementAccessPath $snapshotRoot 'management-access-token'
  $verifier=Join-Path $source 'verify-steward-dist.ps1'
  if(-not(Test-Path -LiteralPath $verifier -PathType Leaf)){throw 'release is missing verify-steward-dist.ps1'}
  Assert-StagedVerifierTrust $verifier $trustedSigner ([bool]$AllowUnsignedPackage)
  $verifyArgs=@{DistDir=$source;RequiredTargets=@('windows/amd64');RunCurrentBinary=$true;AllowUnsignedPackage=$AllowUnsignedPackage;AllowDirtyPackage=$AllowDirtyPackage;SkipCertificateRevocationCheck=$SkipCertificateRevocationCheck;RequirePackageMode=$true}
  if($trustedSigner){$verifyArgs.TrustedSignerThumbprint=$trustedSigner}
  & $verifier @verifyArgs|Out-Host
  $releaseManifest=Get-Content -LiteralPath (Join-Path $source 'release-manifest.json') -Raw|ConvertFrom-Json
  if($AllowUnsignedPackage){Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: installation accepts an unsigned release package.'}
  if($AllowDirtyPackage){Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: installation accepts a dirty-worktree release package.'}
  if($BrokerPolicyPath){
    $policy=Get-CanonicalPath $BrokerPolicyPath 'BrokerPolicyPath' $true
    if(-not(Test-Path -LiteralPath $policy -PathType Leaf)){throw 'BrokerPolicyPath must identify a policy file'}
    Assert-NoReparseAncestors $policy 'BrokerPolicyPath'
    $policyItem=Get-Item -LiteralPath $policy -Force
    if(($policyItem.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "BrokerPolicyPath must not be a reparse point: $policy"}
    $stagedExternalPolicy=Join-Path $transactionDir 'broker-policy-input.json'
    Copy-Item -LiteralPath $policy -Destination $stagedExternalPolicy
    Protect-AdminFile $stagedExternalPolicy
    $policy=$stagedExternalPolicy
  }
  foreach($name in @('steward.exe','steward-broker.exe','steward-approval.exe','steward-companion.exe','steward-system-tool-host.exe','ui\index.html')) {
    if(-not (Test-Path -LiteralPath (Join-Path $source $name))){throw "release is missing $name"}
  }
  if(Get-Service -Name $ServiceName -ErrorAction SilentlyContinue){throw "service already exists: $ServiceName"}
  if(Get-Service -Name $BrokerServiceName -ErrorAction SilentlyContinue){throw "service already exists: $BrokerServiceName"}
  $installMutationStarted=$true
  $orphanedBrokerData=Move-OrphanedBrokerData $BrokerDataDir 'orphaned'
  $brokerDataPrepared=$true

  Write-TransactionRecord $transactionPath 'mutating' @{broker_data_quarantine=$orphanedBrokerData}
  New-Item -ItemType Directory -Force -Path $InstallDir,$DataDir,$BrokerDataDir,(Join-Path $DataDir 'data'),(Join-Path $DataDir 'logs') | Out-Null
  Protect-AdminPath $InstallDir; Protect-AdminPath $DataDir; Protect-AdminPath $BrokerDataDir
  Get-ChildItem -LiteralPath $source -Force|Copy-Item -Destination $InstallDir -Recurse -Force
  Assert-NoReparsePoints $InstallDir 'installed release'
  Protect-AdminTree $InstallDir
  $releaseTrustPath=Write-ReleaseTrust $InstallDir $trustedSigner $releaseManifest

  $brokerExe=Join-Path $InstallDir 'steward-broker.exe'
  if(-not $policy){
    $bootstrapDir=Join-Path $DataDir 'broker-bootstrap'
    $bootstrapArgs=@('bootstrap','--output-dir',$bootstrapDir,'--system-tool-host',(Join-Path $InstallDir 'steward-system-tool-host.exe'))
    if(Test-Path -LiteralPath $bootstrapDir){$bootstrapArgs+='--force'}
    $bootstrapRaw=& $brokerExe @bootstrapArgs 2>&1|Out-String
    if($LASTEXITCODE -ne 0){throw "Broker bootstrap failed: $bootstrapRaw"}
    Protect-AdminPath $bootstrapDir
    $bootstrap=$bootstrapRaw|ConvertFrom-Json
    $policy=$bootstrap.policy
    $brokerSecrets=Get-Content -LiteralPath $bootstrap.broker_secrets -Raw|ConvertFrom-Json
    $stewardClient=Get-Content -LiteralPath $bootstrap.steward_client -Raw|ConvertFrom-Json
    $keys=[pscustomobject]@{keys=[pscustomobject]@{client_key=$brokerSecrets.STEWARD_BROKER_CLIENT_KEY;control_key=$brokerSecrets.STEWARD_BROKER_CONTROL_KEY;signing_private_key=$brokerSecrets.STEWARD_BROKER_SIGNING_PRIVATE_KEY};steward_env=$stewardClient}
  }else{
    $keysRaw=& $brokerExe keygen 2>&1 | Out-String
    if($LASTEXITCODE -ne 0){throw "Broker key generation failed: $keysRaw"}
    $keys=$keysRaw|ConvertFrom-Json
  }
  $protectedPolicyInput=Join-Path $BrokerDataDir 'policy-install-input.json'
  Copy-Item -LiteralPath $policy -Destination $protectedPolicyInput -Force
  Protect-AdminFile $protectedPolicyInput
  $policy=$protectedPolicyInput
  $localKey=$LocalEncryptionKey
  if(-not $localKey){$localKey=New-Key 32}
  $syncSecretValue=$SyncSecret
  if(-not $syncSecretValue){$syncSecretValue=New-Key 32}
  $orchestrationKey=$OrchestrationSigningKey
  if(-not $orchestrationKey){$orchestrationKey=New-Key 32}
  Assert-Base64Key $orchestrationKey 32 'OrchestrationSigningKey'
  $managementToken=$ManagementAuthToken
  if(-not $managementToken){$managementToken=New-Key 32}
  if($managementToken.Length -lt 32){throw 'ManagementAuthToken must contain at least 32 characters'}
  foreach($value in @($DatabaseURL,$syncSecretValue,$localKey,$orchestrationKey,$managementToken,$keys.keys.client_key,$keys.keys.control_key,$keys.keys.signing_private_key,$keys.steward_env.STEWARD_BROKER_CLIENT_KEY,$LLMAPIKey,$DevicePrivateKey,$SyncEncryptionKey,$SyncEncryptionPreviousKeys,$LocalEncryptionPreviousKeys)){if(-not[string]::IsNullOrWhiteSpace([string]$value)){$sensitiveValues.Add([string]$value)}}

  $brokerArgs=@('service','install','--name',$BrokerServiceName,'--scope','system','--policy',$policy,
    '--install-dir',$BrokerInstallDir,'--workdir',$BrokerDataDir,
    '--private-environment-file',(Join-Path $BrokerDataDir 'service-secrets.json'),
    '--state',(Join-Path $BrokerDataDir 'state.json'),'--audit',(Join-Path $BrokerDataDir 'audit.jsonl'),
    '--checkpoint',(Join-Path $BrokerDataDir 'checkpoint.json'),
    '--device-id',$AgentID)
  if($Start){$brokerArgs+='--start'}
  $brokerSecretEnvironment=@{
    STEWARD_BROKER_CLIENT_KEY=[string]$keys.keys.client_key
    STEWARD_BROKER_CONTROL_KEY=[string]$keys.keys.control_key
    STEWARD_BROKER_SIGNING_PRIVATE_KEY=[string]$keys.keys.signing_private_key
  }
  $brokerPrevious=@{}
  try{
    foreach($entry in $brokerSecretEnvironment.GetEnumerator()){$brokerPrevious[$entry.Key]=[Environment]::GetEnvironmentVariable($entry.Key,'Process');[Environment]::SetEnvironmentVariable($entry.Key,$entry.Value,'Process')}
    $brokerOutput=& $brokerExe @brokerArgs 2>&1 | Out-String
    $brokerExitCode=$LASTEXITCODE
  }finally{foreach($entry in $brokerPrevious.GetEnumerator()){[Environment]::SetEnvironmentVariable($entry.Key,$entry.Value,'Process')}}
  if($brokerExitCode -ne 0){$createdBroker=$null -ne (Get-Service $BrokerServiceName -ErrorAction SilentlyContinue);throw "Broker installation failed: $(ConvertTo-RedactedText $brokerOutput $sensitiveValues.ToArray())"}
  $createdBroker=$true
	if(Test-Path -LiteralPath $protectedPolicyInput){Remove-Item -LiteralPath $protectedPolicyInput -Force}
	if($bootstrapDir -and (Test-Path -LiteralPath $bootstrapDir)){
	  [System.IO.Directory]::Delete($bootstrapDir,$true)
	}

  $stewardExe=Join-Path $InstallDir 'steward.exe'
  $privateEnvironmentFile=Join-Path $DataDir 'config\service-secrets.json'
  $serviceArgs=@('service','install','--name',$ServiceName,'--scope','system','--binary',$stewardExe,
    '--workdir',$DataDir,'--http-addr',$HTTPAddress,'--peer-http-addr',$PeerHTTPAddress,
    '--storage-dir',(Join-Path $DataDir 'data'),'--log-dir',(Join-Path $DataDir 'logs'),
    '--ui-dir',(Join-Path $InstallDir 'ui'),'--agent-id',$AgentID,
    '--runtime-v2','--runtime-r2','--runtime-r3',
    '--broker-url','http://127.0.0.1:18100',
    '--broker-public-key',$keys.steward_env.STEWARD_BROKER_PUBLIC_KEY,
    '--windows-hardened','--windows-install-dir',$InstallDir,'--windows-private-environment-file',$privateEnvironmentFile,
    '--windows-service-account','localservice','--windows-service-sid-type','restricted')
  if($DevicePublicKey){$serviceArgs+=@('--device-public-key',$DevicePublicKey)}
  if($SyncEncryptionKeyID){$serviceArgs+=@('--sync-encryption-key-id',$SyncEncryptionKeyID)}
  if($LocalEncryptionKeyID){$serviceArgs+=@('--local-encryption-key-id',$LocalEncryptionKeyID)}
  if($LLMBaseURL){$serviceArgs+=@('--llm-provider','openai-compatible','--llm-base-url',$LLMBaseURL)}
  if($LLMModel){$serviceArgs+=@('--llm-model',$LLMModel)}
  if($RecoverModelSettingsFromEnvironment){$serviceArgs+='--recover-model-settings-from-env'}
  if($Start){$serviceArgs+='--start'}
  $mainPrivateEnvironment=@{
    DATABASE_URL=$DatabaseURL
    STEWARD_SYNC_SECRET=$syncSecretValue
    STEWARD_LOCAL_ENCRYPTION_KEY=$localKey
    STEWARD_ORCHESTRATION_SIGNING_KEY=$orchestrationKey
    STEWARD_MANAGEMENT_AUTH_REQUIRED='true'
    STEWARD_MANAGEMENT_AUTH_TOKEN=$managementToken
    STEWARD_BROKER_CLIENT_KEY=[string]$keys.steward_env.STEWARD_BROKER_CLIENT_KEY
  }
  if($DevicePrivateKey){$mainPrivateEnvironment.STEWARD_DEVICE_PRIVATE_KEY=$DevicePrivateKey}
  if($SyncEncryptionKey){$mainPrivateEnvironment.STEWARD_SYNC_ENCRYPTION_KEY=$SyncEncryptionKey}
  if($SyncEncryptionPreviousKeys){$mainPrivateEnvironment.STEWARD_SYNC_ENCRYPTION_PREVIOUS_KEYS=$SyncEncryptionPreviousKeys}
  if($LocalEncryptionPreviousKeys){$mainPrivateEnvironment.STEWARD_LOCAL_ENCRYPTION_PREVIOUS_KEYS=$LocalEncryptionPreviousKeys}
  if($LLMAPIKey){$mainPrivateEnvironment.STEWARD_LLM_API_KEY=$LLMAPIKey}
  $mainPrevious=@{}
  try{
    foreach($entry in $mainPrivateEnvironment.GetEnumerator()){$mainPrevious[$entry.Key]=[Environment]::GetEnvironmentVariable($entry.Key,'Process');[Environment]::SetEnvironmentVariable($entry.Key,$entry.Value,'Process')}
    $mainOutput=& $stewardExe @serviceArgs 2>&1 | Out-String
    $mainExitCode=$LASTEXITCODE
  }finally{foreach($entry in $mainPrevious.GetEnumerator()){[Environment]::SetEnvironmentVariable($entry.Key,$entry.Value,'Process')}}
  if($mainExitCode -ne 0){$createdMain=$null -ne (Get-Service $ServiceName -ErrorAction SilentlyContinue);throw "main service installation failed: $(ConvertTo-RedactedText $mainOutput $sensitiveValues.ToArray())"}
  $createdMain=$true
  Protect-MainInstallTree $InstallDir $ServiceName
  Grant-RestrictedCapabilityHostReadExecute (Join-Path $InstallDir 'steward-system-tool-host.exe')

  if($InstallCompanion){
    & (Join-Path $InstallDir 'install-steward-companion.ps1') -SourceDir $InstallDir -LocalEncryptionKey $localKey -TaskName $CompanionTaskName -ServiceName $ServiceName -Start:$Start | Out-Host
    if($LASTEXITCODE -ne 0){throw "Session Companion installation failed"}
    $companionInstalled=$true
  }
  $managementAccessPath=Write-CurrentUserSecretFile $managementAccessPath $managementToken
  $managementAccessFileCreated=$true
  $markerPath=Join-Path $DataDir 'installation.json'
  $markerTemporary="$markerPath.tmp-$([guid]::NewGuid().ToString('N'))"
  $marker=[ordered]@{
    schema='mongojson.steward.windows-installation/v2';install_id=[guid]::NewGuid().ToString('D')
    service_name=$ServiceName;broker_service_name=$BrokerServiceName;companion_task_name=$CompanionTaskName
    install_dir=$InstallDir;data_dir=$DataDir;broker_install_dir=$BrokerInstallDir;broker_data_dir=$BrokerDataDir
    broker_policy_path=(Join-Path $BrokerDataDir 'policy.json');installed_at=[DateTimeOffset]::UtcNow.ToString('o')
    release_version=[string]$releaseManifest.version;release_commit=[string]$releaseManifest.commit;release_built_at=[string]$releaseManifest.built_at;quarantined_broker_data=$orphanedBrokerData
  }
  [IO.File]::WriteAllText($markerTemporary,($marker|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false))
  Move-Item -LiteralPath $markerTemporary -Destination $markerPath -Force
  Protect-AdminFile $markerPath
  Write-TransactionRecord $transactionPath 'verifying' @{service=$ServiceName;broker=$BrokerServiceName}
  if($Verify){
    if(-not $Start){throw '-Verify requires -Start'}
    $managementBase="http://$HTTPAddress"
    & (Join-Path $InstallDir 'test-steward-production.ps1') `
      -ServiceName $ServiceName `
      -BrokerServiceName $BrokerServiceName `
      -CompanionTaskName $CompanionTaskName `
      -HealthURL "$managementBase/healthz" `
      -ReadyURL "$managementBase/readyz" `
      -AgentURL "$managementBase/api/steward/agent" `
      -InstallDir $InstallDir `
      -MainDataDir $DataDir `
      -BrokerDataDir $BrokerDataDir `
      -ManagementAccessTokenFile $managementAccessPath `
      -AllowUnsignedReleaseBaseline:$AllowUnsignedPackage `
      -RequireCompanion:$InstallCompanion | Out-Host
    if($LASTEXITCODE -ne 0){throw "production verification failed"}
  }
  if($RecoverModelSettingsFromEnvironment -and $Start){
    # The running process already consumed the explicit recovery marker. Keep
    # future restarts fail-closed if the encrypted database value is damaged.
    Set-ServicePublicEnvironmentValue $ServiceName 'STEWARD_MODEL_SETTINGS_KEY_RECOVERY' 'false'
  }
  Write-TransactionRecord $transactionPath 'completed' @{service=$ServiceName;broker=$BrokerServiceName;verified=[bool]$Verify}
  $installationSucceeded=$true
  [ordered]@{ok=$true;service=$ServiceName;service_account='NT AUTHORITY\LocalService';service_sid='restricted';broker=$BrokerServiceName;broker_account='LocalSystem';companion=$companionInstalled;install_dir=$InstallDir;data_dir=$DataDir;management_access_token_file=$managementAccessPath;release_trust_file=$releaseTrustPath}|ConvertTo-Json
} catch {
  $failure=ConvertTo-RedactedText $_.Exception.Message $sensitiveValues.ToArray()
  if($transactionPath -and (Test-Path -LiteralPath (Split-Path -Parent $transactionPath))){try{Write-TransactionRecord $transactionPath 'rolling_back' @{reason='installation failed'}}catch{$rollbackErrors.Add("record rollback state: $($_.Exception.Message)")}}
  if($companionInstalled -and (Test-Path -LiteralPath (Join-Path $InstallDir 'uninstall-steward-companion.ps1'))){
    try{& (Join-Path $InstallDir 'uninstall-steward-companion.ps1') -TaskName $CompanionTaskName -Confirm:$false|Out-Null;if($LASTEXITCODE -ne 0){throw 'companion uninstaller returned failure'}}catch{$rollbackErrors.Add("remove Session Companion: $($_.Exception.Message)")}
  }
  if($createdMain){try{Remove-ServiceAndWait $ServiceName}catch{$rollbackErrors.Add($_.Exception.Message)}}
  if($createdBroker){try{Remove-ServiceAndWait $BrokerServiceName}catch{$rollbackErrors.Add($_.Exception.Message)}}
  if($installMutationStarted){
    try{Restore-PathSnapshot $privateSnapshot}catch{$rollbackErrors.Add("restore private environment: $($_.Exception.Message)")}
    try{Restore-PathSnapshot $markerSnapshot}catch{$rollbackErrors.Add("restore installation marker: $($_.Exception.Message)")}
    try{Restore-PathSnapshot $managementSnapshot}catch{$rollbackErrors.Add("restore management access token: $($_.Exception.Message)")}
    if($brokerDataPrepared){try{if(Test-Path -LiteralPath $BrokerDataDir){Remove-DirectoryWithRetry $BrokerDataDir};if($orphanedBrokerData -and (Test-Path -LiteralPath $orphanedBrokerData)){Move-Item -LiteralPath $orphanedBrokerData -Destination $BrokerDataDir}}catch{$rollbackErrors.Add("restore Broker data: $($_.Exception.Message)")}}
    try{if(Test-Path -LiteralPath $InstallDir){Remove-DirectoryWithRetry $InstallDir};if($installDirExisted){New-Item -ItemType Directory -Path $InstallDir|Out-Null;Set-PathSecurityDescriptor $InstallDir $installDirSddl $true}}catch{$rollbackErrors.Add("restore install directory: $($_.Exception.Message)")}
    try{if(Test-Path -LiteralPath $BrokerInstallDir){Remove-DirectoryWithRetry $BrokerInstallDir};if($brokerInstallDirExisted){New-Item -ItemType Directory -Path $BrokerInstallDir|Out-Null;Set-PathSecurityDescriptor $BrokerInstallDir $brokerInstallDirSddl $true}}catch{$rollbackErrors.Add("restore Broker install directory: $($_.Exception.Message)")}
    try{
      if(-not $dataDirExisted){if(Test-Path -LiteralPath $DataDir){Remove-DirectoryWithRetry $DataDir}}
      else{
        foreach($entry in @(@((Join-Path $DataDir 'config'),$configDirExisted),@((Join-Path $DataDir 'logs'),$logsDirExisted),@((Join-Path $DataDir 'data'),$storageDirExisted))){if(-not[bool]$entry[1] -and (Test-Path -LiteralPath $entry[0]) -and -not(Test-DirectoryNotEmpty $entry[0])){Remove-Item -LiteralPath $entry[0] -Force}}
        Set-PathSecurityDescriptor $DataDir $dataDirSddl $true;if($configDirExisted){Set-PathSecurityDescriptor (Join-Path $DataDir 'config') $configDirSddl $true};if($logsDirExisted){Set-PathSecurityDescriptor (Join-Path $DataDir 'logs') $logsDirSddl $true};if($storageDirExisted){Set-PathSecurityDescriptor (Join-Path $DataDir 'data') $storageDirSddl $true}
      }
    }catch{$rollbackErrors.Add("restore main data directory: $($_.Exception.Message)")}
  }
  $rollbackState=if($rollbackErrors.Count -eq 0){'rolled_back'}else{'rollback_incomplete'}
  if($transactionPath -and (Test-Path -LiteralPath (Split-Path -Parent $transactionPath))){try{Write-TransactionRecord $transactionPath $rollbackState @{rollback_errors=@($rollbackErrors)}}catch{$rollbackErrors.Add("record rollback result: $($_.Exception.Message)")}}
  $suffix=if($rollbackErrors.Count -gt 0){" Rollback also reported: $($rollbackErrors -join '; ')"}else{''}
  $prefix=if($rollbackErrors.Count -eq 0){'R5.1 installation failed; created services and owned paths were rolled back'}else{'R5.1 installation failed; rollback was incomplete and the protected transaction record was retained'}
  throw "${prefix}: $failure.$suffix"
} finally {
  foreach($entry in @($mainPrivateEnvironment,$brokerSecretEnvironment)){if($entry -is [Collections.IDictionary]){foreach($key in @($entry.Keys)){$entry[$key]=$null}}}
  if($releaseStage -and (Test-Path -LiteralPath $releaseStage)){Remove-Item -LiteralPath $releaseStage -Recurse -Force -ErrorAction SilentlyContinue}
  if($transactionDir -and (Test-Path -LiteralPath $transactionDir) -and ($installationSucceeded -or -not $installMutationStarted -or $rollbackErrors.Count -eq 0)){Remove-Item -LiteralPath $transactionDir -Recurse -Force -ErrorAction SilentlyContinue}
}
