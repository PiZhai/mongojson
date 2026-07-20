[CmdletBinding()]
param(
  [Parameter(Mandatory=$true)][string]$SourceDir,
  [string]$InstallDir="C:\Program Files\MongojsonSteward",
  [string]$ServiceName="MongojsonSteward",
  [string]$BrokerServiceName="MongojsonStewardBroker",
  [string]$BrokerInstallDir="C:\Program Files\MongoJSON\StewardBroker",
  [string]$DataDir="C:\ProgramData\MongojsonSteward",
  [string]$BrokerDataDir="C:\ProgramData\MongoJSON\StewardBroker",
  [string]$BrokerPolicyPath="C:\ProgramData\MongoJSON\StewardBroker\policy.json",
  [string]$HealthURL="",
  [string]$ReadyURL="",
  [string]$AgentURL="",
  [string]$CompanionAPIBase="",
  [string]$CompanionTaskName="MongojsonStewardCompanion",
  [string]$CompanionInstallDir=(Join-Path $env:LOCALAPPDATA "MongojsonSteward"),
  [string]$CompanionLocalEncryptionKey="",
  [string]$ManagementAccessTokenFile=(Join-Path $env:LOCALAPPDATA "MongojsonSteward\management-access-token.txt"),
  [switch]$InstallCompanion,
  [switch]$AllowUnsignedPackage,
  [switch]$AllowDirtyPackage,
  [switch]$AllowRollback,
  [string]$TrustedSignerThumbprint="",
  [switch]$SkipCertificateRevocationCheck,
  [string]$TransactionRoot=(Join-Path $env:ProgramData 'MongojsonStewardUpdateTransactions')
)
$ErrorActionPreference='Stop'

function Test-Administrator {
  $principal=[Security.Principal.WindowsPrincipal]::new([Security.Principal.WindowsIdentity]::GetCurrent())
  return $principal.IsInRole([Security.Principal.WindowsBuiltInRole]::Administrator)
}
function Get-CanonicalPath([string]$Path,[string]$Name,[bool]$MustExist=$false) {
  if([string]::IsNullOrWhiteSpace($Path)){throw "$Name must not be empty"}
  if([WildcardPattern]::ContainsWildcardCharacters($Path)){throw "$Name must not contain wildcard characters"}
  if(-not[IO.Path]::IsPathRooted($Path)){throw "$Name must be absolute"}
  try{$full=[IO.Path]::GetFullPath($Path);if($full.Length -gt [IO.Path]::GetPathRoot($full).Length){$full=$full.TrimEnd('\')}}catch{throw "$Name is not a valid absolute path"}
  if($MustExist -and -not(Test-Path -LiteralPath $full)){throw "$Name does not exist: $full"}
  return $full
}
function Normalize-CompanionAPIBase([string]$Value) {
  if([string]::IsNullOrWhiteSpace($Value)){throw 'CompanionAPIBase must not be empty'}
  try{$uri=[Uri]$Value.Trim()}catch{throw "CompanionAPIBase must be an absolute HTTP URL: $Value"}
  if(-not $uri.IsAbsoluteUri -or @('http','https') -notcontains $uri.Scheme){throw "CompanionAPIBase must be an absolute HTTP URL: $Value"}
  if(-not [string]::IsNullOrEmpty($uri.UserInfo) -or -not [string]::IsNullOrEmpty($uri.Query) -or -not [string]::IsNullOrEmpty($uri.Fragment)){throw 'CompanionAPIBase must not contain credentials, a query, or a fragment'}
  $loopback=$uri.Host.Equals('localhost',[StringComparison]::OrdinalIgnoreCase)
  $address=$null
  if(-not $loopback -and [Net.IPAddress]::TryParse($uri.Host,[ref]$address)){$loopback=[Net.IPAddress]::IsLoopback($address)}
  if(-not $loopback){throw "CompanionAPIBase must target loopback: $Value"}
  if(-not $uri.AbsolutePath.TrimEnd('/').Equals('/api',[StringComparison]::OrdinalIgnoreCase)){throw "CompanionAPIBase must end with /api: $Value"}
  return $uri.GetLeftPart([UriPartial]::Authority)+'/api'
}
function Assert-SafeSystemName([string]$Value,[string]$Name) {if($Value -notmatch '^[A-Za-z0-9][A-Za-z0-9_.-]{0,127}$'){throw "$Name contains unsupported characters"}}
function Assert-DedicatedChildPath([string]$Path,[string]$Root,[string]$Name) {
  $full=Get-CanonicalPath $Path $Name $false;$rootFull=Get-CanonicalPath $Root "$Name root" $true
  if($full.Equals($rootFull,[StringComparison]::OrdinalIgnoreCase) -or -not $full.StartsWith($rootFull+'\',[StringComparison]::OrdinalIgnoreCase)){throw "$Name must be a dedicated child below $rootFull"}
  return $full
}
function Assert-NoReparseAncestors([string]$Path,[string]$Name) {
  $current=Get-CanonicalPath $Path $Name $false
  while($true){
    if(Test-Path -LiteralPath $current){$item=Get-Item -LiteralPath $current -Force;if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Name contains a reparse point at $current"}}
    $parent=Split-Path -Parent $current;if([string]::IsNullOrWhiteSpace($parent) -or $parent -eq $current){break};$current=$parent
  }
}
function Assert-DisjointPaths([hashtable]$Paths) {
  $entries=@($Paths.GetEnumerator())
  for($i=0;$i -lt $entries.Count;$i++){for($j=$i+1;$j -lt $entries.Count;$j++){
    $left=[string]$entries[$i].Value;$right=[string]$entries[$j].Value
    if($left.Equals($right,[StringComparison]::OrdinalIgnoreCase) -or $left.StartsWith($right+'\',[StringComparison]::OrdinalIgnoreCase) -or $right.StartsWith($left+'\',[StringComparison]::OrdinalIgnoreCase)){throw "$($entries[$i].Key) and $($entries[$j].Key) must not overlap"}
  }}
}
function Get-ServiceExecutablePath([string]$ImagePath,[string]$Name) {
  $match=[regex]::Match($ImagePath,'^\s*(?:"([^"]+)"|(\S+))')
  if(-not $match.Success){throw "$Name has an invalid ImagePath"}
  $value=if($match.Groups[1].Success){$match.Groups[1].Value}else{$match.Groups[2].Value}
  return Get-CanonicalPath $value "$Name executable" $true
}
function Assert-ServiceOwnership([object]$Service,[string]$ExpectedExecutable,[string]$ExpectedAccount,[string]$Name) {
  if($null -eq $Service){throw "required Windows service is missing: $Name"}
  if(-not([string]$Service.StartName).Equals($ExpectedAccount,[StringComparison]::OrdinalIgnoreCase)){throw "refusing update: $Name account is '$($Service.StartName)', expected '$ExpectedAccount'"}
  $actual=Get-ServiceExecutablePath ([string]$Service.PathName) $Name
  if(-not $actual.Equals($ExpectedExecutable,[StringComparison]::OrdinalIgnoreCase)){throw "refusing update: $Name ImagePath is outside the owned installation: $actual"}
  Assert-NoReparseAncestors $actual "$Name ImagePath"
}
function Set-StewardServiceRecoveryPolicy([string]$Name,[bool]$DelayedAutoStart,[string]$Actions) {
  $startMode=if($DelayedAutoStart){'delayed-auto'}else{'auto'}
  $output=& sc.exe config $Name start= $startMode 2>&1|Out-String
  if($LASTEXITCODE -ne 0){throw "failed to configure $Name start mode: $($output.Trim())"}
  $output=& sc.exe failure $Name reset= 86400 actions= $Actions 2>&1|Out-String
  if($LASTEXITCODE -ne 0){throw "failed to configure $Name recovery actions: $($output.Trim())"}
  $output=& sc.exe failureflag $Name 1 2>&1|Out-String
  if($LASTEXITCODE -ne 0){throw "failed to enable $Name recovery after non-crash failures: $($output.Trim())"}
}
function Read-InstallationMarker([string]$Path) {
  if(-not(Test-Path -LiteralPath $Path -PathType Leaf)){throw "installation marker is missing: $Path"}
  Assert-NoReparseAncestors $Path 'installation marker'
  $marker=Get-Content -LiteralPath $Path -Raw|ConvertFrom-Json
  if([string]$marker.schema -and [string]$marker.schema -ne 'mongojson.steward.windows-installation/v2'){throw "unsupported installation marker schema: $($marker.schema)"}
  return $marker
}
function Assert-MarkerValue([object]$Marker,[string]$Property,[string]$Expected,[bool]$PathValue=$false) {
  $actual=[string]$Marker.$Property
  if([string]::IsNullOrWhiteSpace($actual)){return}
  if($PathValue){$actual=Get-CanonicalPath $actual "installation marker $Property" $false}
  if(-not $actual.Equals($Expected,[StringComparison]::OrdinalIgnoreCase)){throw "installation marker $Property does not match the requested owned path or name"}
}
function Get-PathSnapshot([string]$Path,[string]$SnapshotRoot,[string]$Name) {
  $exists=Test-Path -LiteralPath $Path;$snapshot=[ordered]@{path=$Path;exists=$exists;is_directory=$false;sddl='';copy=''}
  if(-not $exists){return [pscustomobject]$snapshot}
  $item=Get-Item -LiteralPath $Path -Force;if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "$Name must not be a reparse point"}
  $snapshot.is_directory=$item.PSIsContainer;$snapshot.sddl=(Get-Acl -LiteralPath $Path).Sddl
  if(-not $item.PSIsContainer){$copy=Join-Path $SnapshotRoot ($Name+'-'+[guid]::NewGuid().ToString('N')+'.bin');Copy-Item -LiteralPath $Path -Destination $copy;Protect-AdminFile $copy;$snapshot.copy=$copy}
  return [pscustomobject]$snapshot
}
function Set-PathSecurityDescriptor([string]$Path,[string]$Sddl,[bool]$Directory) {
  if([string]::IsNullOrWhiteSpace($Sddl)){return};$acl=if($Directory){[Security.AccessControl.DirectorySecurity]::new()}else{[Security.AccessControl.FileSecurity]::new()};$acl.SetSecurityDescriptorSddlForm($Sddl);Set-Acl -LiteralPath $Path -AclObject $acl
}
function Restore-PathSnapshot([object]$Snapshot) {
  if($null -eq $Snapshot){return};if(-not[bool]$Snapshot.exists){if(Test-Path -LiteralPath ([string]$Snapshot.path) -PathType Leaf){Remove-Item -LiteralPath ([string]$Snapshot.path) -Force};return}
  if(-not[bool]$Snapshot.is_directory){Copy-Item -LiteralPath ([string]$Snapshot.copy) -Destination ([string]$Snapshot.path) -Force};Set-PathSecurityDescriptor ([string]$Snapshot.path) ([string]$Snapshot.sddl) ([bool]$Snapshot.is_directory)
}
function Write-TransactionRecord([string]$Path,[string]$State,[hashtable]$Details=@{}) {
  $payload=[ordered]@{schema='mongojson.steward.update-transaction/v1';state=$State;updated_at=[DateTimeOffset]::UtcNow.ToString('o');details=$Details};[IO.File]::WriteAllText($Path,($payload|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false));Protect-AdminFile $Path
}
function ConvertTo-RedactedText([string]$Text,[string[]]$Secrets) {
  $safe=[string]$Text;foreach($secret in $Secrets){if(-not[string]::IsNullOrWhiteSpace($secret)){$safe=$safe.Replace($secret,'[REDACTED]')}};return [regex]::Replace($safe,'(?i)(postgres(?:ql)?://[^:/\s]+:)[^@\s]+@','$1[REDACTED]@')
}
function Assert-RollbackHealth([string]$URL) {
  $last=''
  for($i=0;$i -lt 60;$i++){
    try{$response=Invoke-RestMethod -Uri $URL -TimeoutSec 3;if([string]$response.status -eq 'ok'){return}}catch{$last=$_.Exception.Message}
    Start-Sleep -Seconds 1
  }
  throw "restored release did not become healthy: $last"
}
function Remove-DirectoryWithRetry([string]$Path) {
  if(-not(Test-Path -LiteralPath $Path)){return}
  $lastError=''
  for($attempt=0;$attempt -lt 10;$attempt++){
    try{Remove-Item -LiteralPath $Path -Recurse -Force -ErrorAction Stop;return}catch{$lastError=$_.Exception.Message;Start-Sleep -Milliseconds (200*($attempt+1))}
  }
  throw "failed to remove '$Path': $lastError"
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
function Protect-AdminStage([string]$Path) {
  $item=Get-Item -LiteralPath $Path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "release staging directory must not be a reparse point: $Path"}
  & icacls.exe $Path /inheritance:r /grant:r '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to protect release staging directory: $Path"}
}
function Protect-AdminFile([string]$Path) {
  $item=Get-Item -LiteralPath $Path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "release trust file must not be a reparse point: $Path"}
  & icacls.exe $Path /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to protect release trust file: $Path"}
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
function Assert-StagedVerifierTrust([string]$Verifier,[string]$TrustedThumbprint,[bool]$UnsignedOverride) {
  if($TrustedThumbprint){
    $signature=Get-AuthenticodeSignature -LiteralPath $Verifier
    $actual=if($signature.SignerCertificate){($signature.SignerCertificate.Thumbprint -replace '\s','').ToUpperInvariant()}else{''}
    if($signature.Status -eq [Management.Automation.SignatureStatus]::Valid -and $actual -eq $TrustedThumbprint){return}
    if(-not $UnsignedOverride){throw "staged verifier is not validly signed by trusted signer '$TrustedThumbprint': status=$($signature.Status); signer=$actual"}
    Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: staged verifier signature does not match the installed trust pin.'
    return
  }
  if(-not $UnsignedOverride){throw 'TrustedSignerThumbprint is required because the existing installation has no release trust pin'}
  Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: the staged verifier is unsigned and is being executed from an administrator-protected staging directory.'
}
function Read-InstalledReleaseTrust([string]$Root) {
  $path=Join-Path $Root 'release-trust.json'
  if(-not(Test-Path -LiteralPath $path -PathType Leaf)){return [pscustomobject]@{path=$path;thumbprint='';document=$null}}
  $item=Get-Item -LiteralPath $path -Force
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "installed release trust file must not be a reparse point: $path"}
  $document=Get-Content -LiteralPath $path -Raw|ConvertFrom-Json
  if([string]$document.schema -notin @('mongojson.steward.release-trust/v1','mongojson.steward.release-trust/v2')){throw "installed release trust file has an unsupported schema: $path"}
  $thumbprint=Normalize-SignerThumbprint ([string]$document.signer_thumbprint)
  if(-not $thumbprint -and ([string]$document.schema -ne 'mongojson.steward.release-trust/v2' -or [bool]$document.signature_required)){throw "installed release trust file has no signer thumbprint: $path"}
  return [pscustomobject]@{path=$path;thumbprint=$thumbprint;document=$document}
}
function Write-ReleaseTrust([string]$Root,[string]$TrustedThumbprint,[object]$Manifest) {
  $path=Join-Path $Root 'release-trust.json'
  $builtAt=[DateTimeOffset]::MinValue
  if($Manifest.built_at -is [DateTime]){$builtAt=[DateTimeOffset]([DateTime]$Manifest.built_at)}elseif(-not[DateTimeOffset]::TryParse([string]$Manifest.built_at,[ref]$builtAt)){throw 'verified release manifest has an invalid built_at timestamp'}
  $manifestPath=Join-Path $Root 'release-manifest.json'
  if(-not(Test-Path -LiteralPath $manifestPath -PathType Leaf)){throw 'verified release is missing release-manifest.json'}
  $authenticatedFiles=[Collections.Generic.List[object]]::new();$seen=@{}
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
function Assert-ReleaseProgression([object]$CurrentTrust,[object]$CurrentMarker,[object]$Candidate,[bool]$RollbackOverride) {
  $candidateBuiltAt=[DateTimeOffset]::MinValue
  if($Candidate.built_at -is [DateTime]){$candidateBuiltAt=[DateTimeOffset]([DateTime]$Candidate.built_at)}elseif(-not[DateTimeOffset]::TryParse([string]$Candidate.built_at,[ref]$candidateBuiltAt)){throw 'verified release manifest has an invalid built_at timestamp'}
  $currentVersion=if([string]$CurrentTrust.release_version){[string]$CurrentTrust.release_version}else{[string]$CurrentMarker.release_version}
  $currentCommit=if([string]$CurrentTrust.release_commit){[string]$CurrentTrust.release_commit}else{[string]$CurrentMarker.release_commit}
  if($currentVersion -and $currentCommit -and $currentVersion -eq [string]$Candidate.version -and $currentCommit -eq [string]$Candidate.commit){throw 'candidate release has the same version and commit as the installed release; refusing a no-op update'}
  $currentBuiltAt=[DateTimeOffset]::MinValue
  $hasCurrentBuiltAt=if($CurrentTrust.release_built_at -is [DateTime]){$currentBuiltAt=[DateTimeOffset]([DateTime]$CurrentTrust.release_built_at);$true}else{[DateTimeOffset]::TryParse([string]$CurrentTrust.release_built_at,[ref]$currentBuiltAt)}
  if($hasCurrentBuiltAt -and $candidateBuiltAt -lt $currentBuiltAt){
    if(-not $RollbackOverride){throw "candidate release was built before the installed release; use -AllowRollback only for an intentional signed rollback"}
    Write-Warning 'SIGNED ROLLBACK OVERRIDE ACTIVE: candidate built_at precedes the installed release. The existing signer pin remains mandatory.'
  }
}
function New-RandomBase64Key([int]$Length=32) {
  $bytes=New-Object byte[] $Length
  $rng=[Security.Cryptography.RandomNumberGenerator]::Create()
  try{$rng.GetBytes($bytes)}finally{$rng.Dispose()}
  return [Convert]::ToBase64String($bytes)
}
function Assert-Base64Key([string]$Value,[int]$Length,[string]$Name) {
  try{$bytes=[Convert]::FromBase64String($Value)}catch{throw "$Name in the private environment file is not valid base64"}
  if($bytes.Length -ne $Length){throw "$Name in the private environment file must contain exactly $Length bytes"}
}
function Ensure-PrivateServiceCredentials([string]$PrivateEnvironmentPath,[string]$Name) {
  if(-not(Test-Path -LiteralPath $PrivateEnvironmentPath -PathType Leaf)){throw "private service environment is missing: $PrivateEnvironmentPath"}
  $environment=Get-Content -LiteralPath $PrivateEnvironmentPath -Raw|ConvertFrom-Json
  $orchestrationKey=[string]$environment.STEWARD_ORCHESTRATION_SIGNING_KEY
  $managementToken=[string]$environment.STEWARD_MANAGEMENT_AUTH_TOKEN
  $orchestrationGenerated=$false
  $managementGenerated=$false
  if(-not [string]::IsNullOrWhiteSpace($orchestrationKey)){
    Assert-Base64Key $orchestrationKey 32 'STEWARD_ORCHESTRATION_SIGNING_KEY'
  }else{
    $orchestrationKey=New-RandomBase64Key 32
    $environment|Add-Member -NotePropertyName STEWARD_ORCHESTRATION_SIGNING_KEY -NotePropertyValue $orchestrationKey -Force
    $orchestrationGenerated=$true
  }
  if(-not [string]::IsNullOrWhiteSpace($managementToken)){
    if($managementToken.Length -lt 32){throw 'STEWARD_MANAGEMENT_AUTH_TOKEN in the private environment file must contain at least 32 characters; refusing to rotate or replace it automatically'}
  }else{
    $managementToken=New-RandomBase64Key 32
    $environment|Add-Member -NotePropertyName STEWARD_MANAGEMENT_AUTH_TOKEN -NotePropertyValue $managementToken -Force
    $managementGenerated=$true
  }
  if($orchestrationGenerated -or $managementGenerated){
    $temporary="$PrivateEnvironmentPath.tmp-$([guid]::NewGuid().ToString('N'))"
    [IO.File]::WriteAllText($temporary,($environment|ConvertTo-Json -Depth 20),[Text.UTF8Encoding]::new($false))
    try{
      # File.Replace rejects a null backup path on the Windows/.NET runtime
      # used by the production installer. File.Move(..., overwrite: true)
      # performs the required same-volume atomic replacement without a backup;
      # the transaction already owns a separate protected snapshot.
      [IO.File]::Move($temporary,$PrivateEnvironmentPath,$true)
      $sid=(New-Object Security.Principal.NTAccount("NT SERVICE\$Name")).Translate([Security.Principal.SecurityIdentifier]).Value
      & icacls.exe $PrivateEnvironmentPath /inheritance:r /grant:r '*S-1-5-18:F' '*S-1-5-32-544:F' "*${sid}:R"|Out-Null
      if($LASTEXITCODE -ne 0){throw "failed to restore private environment ACL after adding missing service credentials"}
    }finally{
      if(Test-Path -LiteralPath $temporary){Remove-Item -LiteralPath $temporary -Force}
    }
  }
  if($orchestrationGenerated){Write-Warning 'The existing installation had no orchestration signing key. A key was generated once and stored in the protected private environment file.'}
  if($managementGenerated){Write-Warning 'The existing installation had no management authentication token. A high-entropy token was generated once and stored in the protected private environment file.'}
  return [pscustomobject]@{orchestration_key_generated=$orchestrationGenerated;management_token_generated=$managementGenerated;management_token=$managementToken}
}
function Write-CurrentUserManagementToken([string]$Path,[string]$Token) {
  if([string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)){throw 'LOCALAPPDATA is unavailable for the management access token file'}
  $localRoot=[IO.Path]::GetFullPath($env:LOCALAPPDATA).TrimEnd('\')+'\'
  $full=[IO.Path]::GetFullPath($Path)
  if(-not $full.StartsWith($localRoot,[StringComparison]::OrdinalIgnoreCase)){throw "ManagementAccessTokenFile must remain under the current user's LocalAppData: $full"}
  Assert-NoReparseAncestors $full 'ManagementAccessTokenFile'
  $directory=Split-Path -Parent $full
  New-Item -ItemType Directory -Force -Path $directory|Out-Null
  Assert-NoReparseAncestors $full 'ManagementAccessTokenFile'
  $sid=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  & icacls.exe $directory /inheritance:r /grant:r "*${sid}:(OI)(CI)F" '*S-1-5-18:(OI)(CI)F' '*S-1-5-32-544:(OI)(CI)F'|Out-Null
  if($LASTEXITCODE -ne 0){throw "failed to protect current-user management secret directory: $directory"}
  $temporary="$full.tmp-$([guid]::NewGuid().ToString('N'))"
  try{
    [IO.File]::WriteAllText($temporary,$Token+"`r`n",[Text.UTF8Encoding]::new($false))
    if(Test-Path -LiteralPath $full){[IO.File]::Move($temporary,$full,$true)}else{Move-Item -LiteralPath $temporary -Destination $full}
    & icacls.exe $full /inheritance:r /grant:r "*${sid}:F" '*S-1-5-18:F' '*S-1-5-32-544:F'|Out-Null
    if($LASTEXITCODE -ne 0){throw "failed to protect current-user management access token file: $full"}
    & icacls.exe $full /setowner "*${sid}"|Out-Null
    if($LASTEXITCODE -ne 0){throw "failed to set current-user ownership on the management access token file: $full"}
  }finally{if(Test-Path -LiteralPath $temporary){Remove-Item -LiteralPath $temporary -Force}}
  return $full
}
function Protect-MainInstallPath([string]$Path,[string]$Name) {
  Assert-NoReparsePoints $Path 'main installation tree'
  $system=[Security.Principal.SecurityIdentifier]::new('S-1-5-18');$administrators=[Security.Principal.SecurityIdentifier]::new('S-1-5-32-544');$localService=[Security.Principal.SecurityIdentifier]::new('S-1-5-19')
  $serviceSID=(New-Object Security.Principal.NTAccount("NT SERVICE\$Name")).Translate([Security.Principal.SecurityIdentifier]);$allow=[Security.AccessControl.AccessControlType]::Allow
  $full=[Security.AccessControl.FileSystemRights]::FullControl;$readExecute=[Security.AccessControl.FileSystemRights]::ReadAndExecute;$inheritance=[Security.AccessControl.InheritanceFlags]'ContainerInherit, ObjectInherit';$noneInheritance=[Security.AccessControl.InheritanceFlags]::None;$nonePropagation=[Security.AccessControl.PropagationFlags]::None
  foreach($directory in @((Get-Item -LiteralPath $Path -Force))+@(Get-ChildItem -LiteralPath $Path -Force -Recurse -Directory)){
    $acl=[Security.AccessControl.DirectorySecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    foreach($entry in @(@($system,$full),@($administrators,$full),@($localService,$readExecute),@($serviceSID,$readExecute))){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($entry[0],$entry[1],$inheritance,$nonePropagation,$allow))};Set-Acl -LiteralPath $directory.FullName -AclObject $acl
  }
  foreach($file in @(Get-ChildItem -LiteralPath $Path -Force -Recurse -File)){
    $acl=[Security.AccessControl.FileSecurity]::new();$acl.SetOwner($administrators);$acl.SetAccessRuleProtection($true,$false)
    foreach($entry in @(@($system,$full),@($administrators,$full),@($localService,$readExecute),@($serviceSID,$readExecute))){$acl.AddAccessRule([Security.AccessControl.FileSystemAccessRule]::new($entry[0],$entry[1],$noneInheritance,$nonePropagation,$allow))};Set-Acl -LiteralPath $file.FullName -AclObject $acl
  }
  $hostPath=Join-Path $Path 'steward-system-tool-host.exe';& icacls.exe $hostPath /grant '*S-1-5-18:(RX)' '*S-1-5-12:(RX)'|Out-Null;if($LASTEXITCODE -ne 0){throw 'failed to grant the production capability token access to the System Tool Host'}
  foreach($required in @((Join-Path $Path 'steward.exe'),(Join-Path $Path 'ui\index.html'))){
    if(-not(Test-Path -LiteralPath $required -PathType Leaf)){throw "required service-readable release file is missing: $required"};$rules=(Get-Acl -LiteralPath $required).GetAccessRules($true,$false,[Security.Principal.SecurityIdentifier])
    foreach($sid in @($localService,$serviceSID)){if(@($rules|Where-Object{$_.IdentityReference -eq $sid -and $_.AccessControlType -eq $allow -and (($_.FileSystemRights -band $readExecute) -eq $readExecute)}).Count -eq 0){throw "service read/execute ACL verification failed for $required and SID $sid"}}
  }
}
function Restore-Companion([object]$State) {
  if($null -eq $State){return}
  Stop-ScheduledTask -TaskName $CompanionTaskName -ErrorAction SilentlyContinue
  Unregister-ScheduledTask -TaskName $CompanionTaskName -Confirm:$false -ErrorAction SilentlyContinue
  if(Test-Path -LiteralPath $CompanionInstallDir){Remove-DirectoryWithRetry $CompanionInstallDir}
  if(-not [string]::IsNullOrWhiteSpace([string]$State.rollback_dir) -and (Test-Path -LiteralPath $State.rollback_dir)){
    Move-Item -LiteralPath $State.rollback_dir -Destination $CompanionInstallDir -ErrorAction Stop
  }
  if(-not [string]::IsNullOrWhiteSpace([string]$State.rollback_task_xml) -and (Test-Path -LiteralPath $State.rollback_task_xml)){
    Register-ScheduledTask -TaskName $CompanionTaskName -Xml (Get-Content -LiteralPath $State.rollback_task_xml -Raw) -Force|Out-Null
    if([bool]$State.previous_task_running){Start-ScheduledTask -TaskName $CompanionTaskName}
  }
}
function Remove-CompanionRollbackData([object]$State) {
  if($null -eq $State){return}
  if(-not [string]::IsNullOrWhiteSpace([string]$State.rollback_dir)){Remove-DirectoryWithRetry ([string]$State.rollback_dir)}
  if(-not [string]::IsNullOrWhiteSpace([string]$State.rollback_task_xml) -and (Test-Path -LiteralPath $State.rollback_task_xml)){Remove-Item -LiteralPath $State.rollback_task_xml -Force}
}

if(-not(Test-Administrator)){throw 'Run this updater from an elevated PowerShell session.'}
Assert-SafeSystemName $ServiceName 'ServiceName';Assert-SafeSystemName $BrokerServiceName 'BrokerServiceName';Assert-SafeSystemName $CompanionTaskName 'CompanionTaskName'
$source=Get-CanonicalPath $SourceDir 'SourceDir' $true
if(-not(Test-Path -LiteralPath $source -PathType Container)){throw 'SourceDir must be a directory'}
$InstallDir=Assert-DedicatedChildPath $InstallDir $env:ProgramFiles 'InstallDir';$BrokerInstallDir=Assert-DedicatedChildPath $BrokerInstallDir $env:ProgramFiles 'BrokerInstallDir'
$DataDir=Assert-DedicatedChildPath $DataDir $env:ProgramData 'DataDir';$BrokerDataDir=Assert-DedicatedChildPath $BrokerDataDir $env:ProgramData 'BrokerDataDir';$TransactionRoot=Assert-DedicatedChildPath $TransactionRoot $env:ProgramData 'TransactionRoot'
$BrokerPolicyPath=Get-CanonicalPath $BrokerPolicyPath 'BrokerPolicyPath' $true
if(-not $BrokerPolicyPath.Equals((Join-Path $BrokerDataDir 'policy.json'),[StringComparison]::OrdinalIgnoreCase)){throw 'BrokerPolicyPath must be the protected policy.json inside BrokerDataDir'}
$installFull=$InstallDir
foreach($entry in @(@($source,'SourceDir'),@($InstallDir,'InstallDir'),@($BrokerInstallDir,'BrokerInstallDir'),@($DataDir,'DataDir'),@($BrokerDataDir,'BrokerDataDir'),@($TransactionRoot,'TransactionRoot'),@($BrokerPolicyPath,'BrokerPolicyPath'))){Assert-NoReparseAncestors $entry[0] $entry[1]}
Assert-NoReparseAncestors (Join-Path $DataDir 'config\service-secrets.json') 'private environment path';Assert-NoReparseAncestors (Join-Path $DataDir 'installation.json') 'installation marker path'
Assert-DisjointPaths @{InstallDir=$InstallDir;BrokerInstallDir=$BrokerInstallDir;DataDir=$DataDir;BrokerDataDir=$BrokerDataDir;TransactionRoot=$TransactionRoot}
Assert-DisjointPaths @{InstallDir=$InstallDir;BrokerInstallDir=$BrokerInstallDir;DataDir=$DataDir;BrokerDataDir=$BrokerDataDir;SourceDir=$source}
Assert-NoReparsePoints $InstallDir 'installed release';Assert-NoReparsePoints $BrokerInstallDir 'Broker installation';Assert-NoReparsePoints $BrokerDataDir 'Broker data'
$localAppData=Get-CanonicalPath $env:LOCALAPPDATA 'LocalApplicationData' $true
$ManagementAccessTokenFile=Get-CanonicalPath $ManagementAccessTokenFile 'ManagementAccessTokenFile' $false
if(-not $ManagementAccessTokenFile.StartsWith($localAppData+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'ManagementAccessTokenFile must be below the current user LocalAppData'}
Assert-NoReparseAncestors $ManagementAccessTokenFile 'ManagementAccessTokenFile'
$installedTrust=Read-InstalledReleaseTrust $InstallDir
$requestedSigner=Normalize-SignerThumbprint $TrustedSignerThumbprint
if($installedTrust.thumbprint -and $requestedSigner -and $installedTrust.thumbprint -ne $requestedSigner){throw "signer rotation is not supported by this updater; requested signer '$requestedSigner' does not match the installed pin '$($installedTrust.thumbprint)'. Use a separately audited dual-sign transition release."}
$trustedSigner=if($installedTrust.thumbprint){$installedTrust.thumbprint}else{$requestedSigner}
$unsignedOverrideForVerification=[bool]$AllowUnsignedPackage -and -not[bool]$installedTrust.thumbprint
if($AllowUnsignedPackage -and $installedTrust.thumbprint){Write-Warning 'AllowUnsignedPackage cannot bypass the installed signer pin; the candidate must still validate under the pinned signer.'}
if($trustedSigner){
  $installedUpdater=Join-Path $installFull 'update-steward-production.ps1'
  $runningUpdater=[IO.Path]::GetFullPath($PSCommandPath)
  if(-not $runningUpdater.Equals([IO.Path]::GetFullPath($installedUpdater),[StringComparison]::OrdinalIgnoreCase)){throw "production updates must be launched with the installed updater: $installedUpdater"}
  $runningSignature=Get-AuthenticodeSignature -LiteralPath $runningUpdater
  $runningSigner=if($runningSignature.SignerCertificate){($runningSignature.SignerCertificate.Thumbprint -replace '\s','').ToUpperInvariant()}else{''}
  if($runningSignature.Status -ne [Management.Automation.SignatureStatus]::Valid -or $runningSigner -ne $trustedSigner){throw "installed updater is not validly signed by release trust '$trustedSigner'"}
}elseif($AllowUnsignedPackage){
  Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: update accepts an unsigned release package and may run outside the installed updater.'
}else{throw 'TrustedSignerThumbprint is required because the installed release has no release-trust.json'}
if($AllowDirtyPackage){Write-Warning 'DEVELOPMENT OVERRIDE ACTIVE: update accepts a dirty-worktree release package.'}

$main=Get-CimInstance Win32_Service|Where-Object Name -eq $ServiceName|Select-Object -First 1
$broker=Get-CimInstance Win32_Service|Where-Object Name -eq $BrokerServiceName|Select-Object -First 1
Assert-ServiceOwnership $main (Join-Path $InstallDir 'steward.exe') 'NT AUTHORITY\LocalService' $ServiceName
Assert-ServiceOwnership $broker (Join-Path $BrokerInstallDir 'steward-broker.exe') 'LocalSystem' $BrokerServiceName
$markerPath=Join-Path $DataDir 'installation.json';$installationMarker=Read-InstallationMarker $markerPath
if([string]::IsNullOrWhiteSpace([string]$installationMarker.service_name) -or [string]::IsNullOrWhiteSpace([string]$installationMarker.broker_service_name)){throw 'installation marker does not identify both owned services'}
if([string]$installationMarker.schema -eq 'mongojson.steward.windows-installation/v2'){foreach($property in @('install_dir','data_dir','broker_install_dir','broker_data_dir')){if([string]::IsNullOrWhiteSpace([string]$installationMarker.$property)){throw "v2 installation marker is missing $property"}}}
Assert-MarkerValue $installationMarker 'service_name' $ServiceName;Assert-MarkerValue $installationMarker 'broker_service_name' $BrokerServiceName
Assert-MarkerValue $installationMarker 'install_dir' $InstallDir $true;Assert-MarkerValue $installationMarker 'data_dir' $DataDir $true;Assert-MarkerValue $installationMarker 'broker_install_dir' $BrokerInstallDir $true;Assert-MarkerValue $installationMarker 'broker_data_dir' $BrokerDataDir $true

$existingCompanionTask=Get-ScheduledTask -TaskName $CompanionTaskName -ErrorAction SilentlyContinue
if($null -ne $existingCompanionTask){
  $taskUser=[string]$existingCompanionTask.Principal.UserId
  $taskSID=if($taskUser -match '^S-1-'){ $taskUser } else { (New-Object Security.Principal.NTAccount($taskUser)).Translate([Security.Principal.SecurityIdentifier]).Value }
  $currentSID=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
  if($taskSID -ne $currentSID){throw "Session Companion belongs to '$taskUser'. Run the updater as that interactive user so its task and user-private key can be updated safely."}
  if(-not $PSBoundParameters.ContainsKey('CompanionInstallDir')){
    $taskExecutable=[string](@($existingCompanionTask.Actions)[0].Execute)
    if(-not [string]::IsNullOrWhiteSpace($taskExecutable)){$CompanionInstallDir=Split-Path -Parent $taskExecutable}
  }
}
$CompanionInstallDir=Get-CanonicalPath $CompanionInstallDir 'CompanionInstallDir' $false
if($CompanionInstallDir.Equals($localAppData,[StringComparison]::OrdinalIgnoreCase) -or -not $CompanionInstallDir.StartsWith($localAppData+'\',[StringComparison]::OrdinalIgnoreCase)){throw 'CompanionInstallDir must be a dedicated directory below the current user LocalAppData'}
Assert-NoReparseAncestors $CompanionInstallDir 'CompanionInstallDir'
if($null -ne $existingCompanionTask){
  $taskAction=[string](@($existingCompanionTask.Actions)[0].Execute)
  $taskExecutable=Get-CanonicalPath $taskAction 'Companion task executable' $true
  if(-not $taskExecutable.Equals((Join-Path $CompanionInstallDir 'steward-companion.exe'),[StringComparison]::OrdinalIgnoreCase)){throw 'Session Companion task executable is outside CompanionInstallDir'}
}
$manageCompanion=$InstallCompanion -or $null -ne $existingCompanionTask
$taskAPIBase=''
if($null -ne $existingCompanionTask -and @($existingCompanionTask.Actions).Count -gt 0){
  $taskArguments=[string](@($existingCompanionTask.Actions)[0].Arguments)
  $taskAPIMatch=[regex]::Match($taskArguments,'(?:^|\s)--api\s+(?:"(?<quoted>[^"]+)"|(?<plain>\S+))',[Text.RegularExpressions.RegexOptions]::IgnoreCase)
  if($taskAPIMatch.Success){$taskAPIBase=if($taskAPIMatch.Groups['quoted'].Success){$taskAPIMatch.Groups['quoted'].Value}else{$taskAPIMatch.Groups['plain'].Value}}
}
if([string]::IsNullOrWhiteSpace($CompanionAPIBase)){
  if(-not [string]::IsNullOrWhiteSpace([string]$installationMarker.companion_api_base)){$CompanionAPIBase=[string]$installationMarker.companion_api_base}
  elseif(-not [string]::IsNullOrWhiteSpace($taskAPIBase)){$CompanionAPIBase=$taskAPIBase}
  elseif(-not [string]::IsNullOrWhiteSpace($AgentURL)){$CompanionAPIBase=([Uri]$AgentURL).GetLeftPart([UriPartial]::Authority)+'/api'}
  elseif(-not [string]::IsNullOrWhiteSpace($HealthURL)){$CompanionAPIBase=([Uri]$HealthURL).GetLeftPart([UriPartial]::Authority)+'/api'}
  else{$CompanionAPIBase='http://127.0.0.1:18080/api'}
}
$CompanionAPIBase=Normalize-CompanionAPIBase $CompanionAPIBase
$managementBase=([Uri]$CompanionAPIBase).GetLeftPart([UriPartial]::Authority)
if([string]::IsNullOrWhiteSpace($HealthURL)){$HealthURL="$managementBase/healthz"}
if([string]::IsNullOrWhiteSpace($ReadyURL)){$ReadyURL="$managementBase/readyz"}
if([string]::IsNullOrWhiteSpace($AgentURL)){$AgentURL="$CompanionAPIBase/steward/agent"}
$DetailedReadyURL="$CompanionAPIBase/system/readiness"
if($manageCompanion -and [string]::IsNullOrWhiteSpace($CompanionLocalEncryptionKey)){
  $companionSecrets=Join-Path $CompanionInstallDir 'companion-secrets.json'
  if(Test-Path -LiteralPath $companionSecrets -PathType Leaf){$CompanionLocalEncryptionKey=[string](Get-Content -LiteralPath $companionSecrets -Raw|ConvertFrom-Json).STEWARD_LOCAL_ENCRYPTION_KEY}
}
if($manageCompanion -and $CompanionLocalEncryptionKey -notmatch '^[A-Za-z0-9+/]{43}=$'){
  throw 'A valid CompanionLocalEncryptionKey is required to install or update Session Companion'
}

$stamp=Get-Date -Format yyyyMMdd-HHmmssfff
$backup="$InstallDir.backup-$stamp"
$failed="$InstallDir.failed-$stamp"
$stage="$InstallDir.stage-$stamp"
$brokerBinary=Join-Path $BrokerInstallDir 'steward-broker.exe'
$brokerBackup="$brokerBinary.backup-$stamp"
$brokerStage="$brokerBinary.stage-$stamp"
$policyBackup=$null
$companionState=$null
$managementAccessPath=$ManagementAccessTokenFile
$serviceCredentials=$null
$mainSwapped=$false
$brokerSwapped=$false
$updateMutationStarted=$false
$releaseManifest=$null
$releaseTrustPath=''
$transactionDir=Join-Path $TransactionRoot ('update-'+$stamp+'-'+[guid]::NewGuid().ToString('N'))
$transactionPath=Join-Path $transactionDir 'transaction.json'
$privateSnapshot=$null;$markerSnapshot=$null;$managementSnapshot=$null;$policySnapshot=$null
$rollbackErrors=[Collections.Generic.List[string]]::new();$sensitiveValues=[Collections.Generic.List[string]]::new()

if(-not(Test-Path -LiteralPath $brokerBinary -PathType Leaf)){throw "installed Broker binary is missing: $brokerBinary"}
foreach($generated in @($backup,$failed,$stage,$brokerBackup,$brokerStage)){if(Test-Path -LiteralPath $generated){throw "update-owned staging or backup path already exists: $generated"};Assert-NoReparseAncestors $generated 'update-owned staging path'}

try{
  New-Item -ItemType Directory -Force -Path $TransactionRoot|Out-Null;Protect-AdminStage $TransactionRoot
  New-Item -ItemType Directory -Path $transactionDir|Out-Null;Protect-AdminStage $transactionDir
  $privateEnvironmentPath=Join-Path $DataDir 'config\service-secrets.json'
  $privateSnapshot=Get-PathSnapshot $privateEnvironmentPath $transactionDir 'private-environment'
  $markerSnapshot=Get-PathSnapshot $markerPath $transactionDir 'installation-marker'
  $managementSnapshot=Get-PathSnapshot $managementAccessPath $transactionDir 'management-access-token'
  $policySnapshot=Get-PathSnapshot $BrokerPolicyPath $transactionDir 'broker-policy'
  if(Test-Path -LiteralPath $privateEnvironmentPath -PathType Leaf){$privateDocument=Get-Content -LiteralPath $privateEnvironmentPath -Raw|ConvertFrom-Json;foreach($property in $privateDocument.PSObject.Properties){if(-not[string]::IsNullOrWhiteSpace([string]$property.Value)){$sensitiveValues.Add([string]$property.Value)}}}
  Write-TransactionRecord $transactionPath 'validating' @{install_dir=$InstallDir;backup=$backup;broker_binary=$brokerBinary}
  Assert-NoReparsePoints $source 'release source'
  New-Item -ItemType Directory -Path $stage|Out-Null
  Protect-AdminStage $stage
  Get-ChildItem -LiteralPath $source -Force|Copy-Item -Destination $stage -Recurse -Force
  Assert-NoReparsePoints $stage 'staged release'
  Protect-AdminTree $stage
  $verifier=Join-Path $stage 'verify-steward-dist.ps1'
  if(-not(Test-Path -LiteralPath $verifier -PathType Leaf)){throw 'release is missing verify-steward-dist.ps1'}
  Assert-StagedVerifierTrust $verifier $trustedSigner $unsignedOverrideForVerification
  $verifyArgs=@{DistDir=$stage;RequiredTargets=@('windows/amd64');RunCurrentBinary=$true;AllowUnsignedPackage=$unsignedOverrideForVerification;AllowDirtyPackage=$AllowDirtyPackage;SkipCertificateRevocationCheck=$SkipCertificateRevocationCheck;RequirePackageMode=$true}
  if($trustedSigner){$verifyArgs.TrustedSignerThumbprint=$trustedSigner}
  & $verifier @verifyArgs|Out-Host
  $releaseManifest=Get-Content -LiteralPath (Join-Path $stage 'release-manifest.json') -Raw|ConvertFrom-Json
  Assert-ReleaseProgression $installedTrust.document $installationMarker $releaseManifest ([bool]$AllowRollback)
  $releaseTrustPath=Write-ReleaseTrust $stage $trustedSigner $releaseManifest
  Protect-MainInstallPath $stage $ServiceName
  Copy-Item -LiteralPath (Join-Path $stage 'steward-broker.exe') -Destination $brokerStage -Force

  $updateMutationStarted=$true
  Write-TransactionRecord $transactionPath 'mutating' @{install_backup=$backup;broker_backup=$brokerBackup}
  Stop-Service $ServiceName -Force -ErrorAction Stop
  Stop-Service $BrokerServiceName -Force -ErrorAction Stop
  $serviceCredentials=Ensure-PrivateServiceCredentials (Join-Path $DataDir 'config\service-secrets.json') $ServiceName
  Copy-Item -LiteralPath $brokerBinary -Destination $brokerBackup -Force
  Move-Item -LiteralPath $InstallDir -Destination $backup -ErrorAction Stop
  Move-Item -LiteralPath $stage -Destination $InstallDir -ErrorAction Stop
  $mainSwapped=$true
  Move-Item -LiteralPath $brokerStage -Destination $brokerBinary -Force
  $brokerSwapped=$true

  $refreshRaw=& $brokerBinary refresh-system-policy --policy $BrokerPolicyPath --system-tool-host (Join-Path $InstallDir 'steward-system-tool-host.exe') 2>&1|Out-String
  if($LASTEXITCODE -ne 0){throw "Broker System Tool policy refresh failed: $(ConvertTo-RedactedText $refreshRaw $sensitiveValues.ToArray())"}
  $policyBackup=($refreshRaw|ConvertFrom-Json).backup
  Set-StewardServiceRecoveryPolicy $ServiceName $true 'restart/15000/restart/30000/restart/60000'
  Set-StewardServiceRecoveryPolicy $BrokerServiceName $false 'restart/5000/restart/15000/restart/30000'
  Start-Service $BrokerServiceName -ErrorAction Stop
  Start-Service $ServiceName -ErrorAction Stop

  $managementAccessPath=Write-CurrentUserManagementToken $ManagementAccessTokenFile $serviceCredentials.management_token
  if($manageCompanion){
    $companionRaw=& (Join-Path $InstallDir 'install-steward-companion.ps1') -SourceDir $InstallDir -InstallDir $CompanionInstallDir -TaskName $CompanionTaskName -LocalEncryptionKey $CompanionLocalEncryptionKey -ManagementAccessTokenFile $managementAccessPath -APIBase $CompanionAPIBase -ServiceName $ServiceName -Start -KeepRollbackData -RollbackRoot (Split-Path -Parent $CompanionInstallDir) | Out-String
    if($LASTEXITCODE -ne 0){throw "Session Companion update failed: $companionRaw"}
    $companionState=$companionRaw|ConvertFrom-Json
  }
  $newMarker=[ordered]@{
    schema='mongojson.steward.windows-installation/v2';install_id=if([string]$installationMarker.install_id){[string]$installationMarker.install_id}else{[guid]::NewGuid().ToString('D')}
    service_name=$ServiceName;broker_service_name=$BrokerServiceName;companion_task_name=$CompanionTaskName
    install_dir=$InstallDir;data_dir=$DataDir;broker_install_dir=$BrokerInstallDir;broker_data_dir=$BrokerDataDir;broker_policy_path=$BrokerPolicyPath
    http_address=([Uri]$CompanionAPIBase).Authority;companion_api_base=$CompanionAPIBase;detailed_ready_url=$DetailedReadyURL
    installed_at=if([string]$installationMarker.installed_at){[string]$installationMarker.installed_at}else{[DateTimeOffset]::UtcNow.ToString('o')};updated_at=[DateTimeOffset]::UtcNow.ToString('o')
    release_version=[string]$releaseManifest.version;release_commit=[string]$releaseManifest.commit;release_built_at=[string]$releaseManifest.built_at
  }
  $markerTemporary="$markerPath.tmp-$([guid]::NewGuid().ToString('N'))";[IO.File]::WriteAllText($markerTemporary,($newMarker|ConvertTo-Json -Depth 10),[Text.UTF8Encoding]::new($false));Move-Item -LiteralPath $markerTemporary -Destination $markerPath -Force;Protect-AdminFile $markerPath
  Write-TransactionRecord $transactionPath 'verifying' @{service=$ServiceName;broker=$BrokerServiceName}

  & (Join-Path $InstallDir 'test-steward-production.ps1') `
    -ServiceName $ServiceName `
    -BrokerServiceName $BrokerServiceName `
    -CompanionTaskName $CompanionTaskName `
    -HealthURL $HealthURL `
    -ReadyURL $ReadyURL `
    -AgentURL $AgentURL `
    -DetailedReadyURL $DetailedReadyURL `
    -InstallDir $InstallDir `
    -MainDataDir $DataDir `
    -BrokerDataDir $BrokerDataDir `
    -ManagementAccessTokenFile $managementAccessPath `
    -AllowUnsignedReleaseBaseline:$unsignedOverrideForVerification `
    -RequireCompanion:$manageCompanion|Out-Host
  if($LASTEXITCODE -ne 0){throw 'post-update verification failed'}
  Write-TransactionRecord $transactionPath 'completed' @{service=$ServiceName;broker=$BrokerServiceName;backup=$backup}
  Remove-CompanionRollbackData $companionState
  Remove-Item -LiteralPath $brokerBackup -Force -ErrorAction SilentlyContinue
  Remove-DirectoryWithRetry $transactionDir
  [ordered]@{ok=$true;backup=$backup;install_dir=$InstallDir;companion_updated=$manageCompanion;release_verified=$true;release_trust_file=(Join-Path $InstallDir 'release-trust.json');orchestration_key_generated=[bool]$serviceCredentials.orchestration_key_generated;management_token_generated=[bool]$serviceCredentials.management_token_generated;management_access_token_file=$managementAccessPath}|ConvertTo-Json
}catch{
  $reason=ConvertTo-RedactedText $_.Exception.Message $sensitiveValues.ToArray()
  if($updateMutationStarted){
    try{Write-TransactionRecord $transactionPath 'rolling_back' @{reason='update failed'}}catch{$rollbackErrors.Add("record rollback state: $($_.Exception.Message)")}
    try{Stop-Service $ServiceName,$BrokerServiceName -Force -ErrorAction SilentlyContinue}catch{$rollbackErrors.Add("stop new services: $($_.Exception.Message)")}
    try{Restore-Companion $companionState}catch{$rollbackErrors.Add("restore Session Companion: $($_.Exception.Message)")}
    if($brokerSwapped -and (Test-Path -LiteralPath $brokerBackup)){try{Copy-Item -LiteralPath $brokerBackup -Destination $brokerBinary -Force}catch{$rollbackErrors.Add("restore Broker binary: $($_.Exception.Message)")}}
    try{Restore-PathSnapshot $policySnapshot}catch{$rollbackErrors.Add("restore Broker policy: $($_.Exception.Message)")}
    if($mainSwapped){
      try{if(Test-Path -LiteralPath $InstallDir){Move-Item -LiteralPath $InstallDir -Destination $failed -ErrorAction Stop}}catch{$rollbackErrors.Add("quarantine failed release: $($_.Exception.Message)")}
      try{if(Test-Path -LiteralPath $backup){Move-Item -LiteralPath $backup -Destination $InstallDir -ErrorAction Stop}else{throw 'old install backup is missing'}}catch{$rollbackErrors.Add("restore old install tree: $($_.Exception.Message)")}
    }
    try{Restore-PathSnapshot $privateSnapshot}catch{$rollbackErrors.Add("restore private service environment: $($_.Exception.Message)")}
    try{Restore-PathSnapshot $markerSnapshot}catch{$rollbackErrors.Add("restore installation marker: $($_.Exception.Message)")}
    try{Restore-PathSnapshot $managementSnapshot}catch{$rollbackErrors.Add("restore management access token: $($_.Exception.Message)")}
    try{Start-Service $BrokerServiceName -ErrorAction Stop}catch{$rollbackErrors.Add("restart restored Broker: $($_.Exception.Message)")}
    try{Start-Service $ServiceName -ErrorAction Stop}catch{$rollbackErrors.Add("restart restored main service: $($_.Exception.Message)")}
    try{Assert-RollbackHealth $HealthURL}catch{$rollbackErrors.Add($_.Exception.Message)}
    try{Remove-DirectoryWithRetry $failed}catch{$rollbackErrors.Add("remove failed release quarantine: $($_.Exception.Message)")}
    try{Remove-Item -LiteralPath $brokerBackup -Force -ErrorAction SilentlyContinue}catch{$rollbackErrors.Add("remove Broker backup: $($_.Exception.Message)")}
    $rollbackState=if($rollbackErrors.Count -eq 0){'rolled_back'}else{'rollback_incomplete'}
    try{Write-TransactionRecord $transactionPath $rollbackState @{rollback_errors=@($rollbackErrors)}}catch{$rollbackErrors.Add("record rollback result: $($_.Exception.Message)")}
    $prefix=if($rollbackErrors.Count -eq 0){'update failed; previous release was restored and passed its health check'}else{'update failed; restoration of the previous release was incomplete'}
  }else{$prefix='update package was rejected before installed services were changed'}
  try{Remove-DirectoryWithRetry $stage}catch{$rollbackErrors.Add("remove release stage: $($_.Exception.Message)")}
  try{Remove-Item -LiteralPath $brokerStage -Force -ErrorAction SilentlyContinue}catch{$rollbackErrors.Add("remove Broker stage: $($_.Exception.Message)")}
  if(-not $updateMutationStarted){try{Remove-DirectoryWithRetry $transactionDir}catch{$rollbackErrors.Add("remove rejected transaction data: $($_.Exception.Message)")}}
  $suffix=if($rollbackErrors.Count -gt 0){" Rollback also reported: $($rollbackErrors -join '; ')"}else{''}
  throw "${prefix}: $reason.$suffix"
}
