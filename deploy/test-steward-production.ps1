[CmdletBinding()]
param(
  [string]$ServiceName = "MongojsonSteward",
  [string]$BrokerServiceName = "MongojsonStewardBroker",
  [string]$CompanionTaskName = "MongojsonStewardCompanion",
  [string]$CompanionPipeName = "MongojsonStewardCompanion",
  [string]$HealthURL = "http://127.0.0.1:18080/healthz",
  [string]$ReadyURL = "http://127.0.0.1:18080/readyz",
  [string]$DetailedReadyURL = "http://127.0.0.1:18080/api/system/readiness",
  [string]$AgentURL = "http://127.0.0.1:18080/api/steward/agent",
  [string]$BrokerURL = "http://127.0.0.1:18100/v1/status",
  [string]$InstallDir = "C:\Program Files\MongojsonSteward",
  [string]$MainDataDir = "C:\ProgramData\MongojsonSteward",
  [string]$BrokerDataDir = "C:\ProgramData\MongoJSON\StewardBroker",
  [string]$ManagementAccessTokenFile = (Join-Path ([Environment]::GetFolderPath('LocalApplicationData')) 'MongojsonSteward\management-access-token.txt'),
  [ValidateRange(1,600)][int]$StartupTimeoutSeconds = 60,
  [ValidateRange(0,86400)][int]$RuntimeSuccessMaxAgeSeconds = 0,
  [switch]$AllowUnsignedReleaseBaseline,
  [switch]$RequireCompanion
)

$ErrorActionPreference = "Stop"
$checks = [Collections.Generic.List[object]]::new()
$script:verificationFailed = $false
$healthURI=[Uri]$HealthURL
$brokerURI=[Uri]$BrokerURL
$mainListenPort=$healthURI.Port
$brokerListenPort=$brokerURI.Port
function Add-Check([string]$Name, [bool]$OK, [string]$Detail) {
  $checks.Add([pscustomobject]@{ name=$Name; ok=$OK; detail=$Detail })
  if (-not $OK) { $script:verificationFailed = $true }
}
function ConvertTo-SIDValue([object]$Identity) {
  if($null -eq $Identity){return ''}
  if($Identity -is [Security.Principal.SecurityIdentifier]){return $Identity.Value}
  try{
    if($Identity -is [Security.Principal.NTAccount]){
      return $Identity.Translate([Security.Principal.SecurityIdentifier]).Value
    }
    $value=[string]$Identity
    if($value -match '^S-\d(?:-\d+)+$'){return $value}
    return ([Security.Principal.NTAccount]::new($value)).Translate([Security.Principal.SecurityIdentifier]).Value
  }catch{return [string]$Identity}
}
function Get-UniqueAllowSIDs([object]$ACL) {
  return @($ACL.Access|Where-Object AccessControlType -eq 'Allow'|ForEach-Object{
    ConvertTo-SIDValue $_.IdentityReference
  }|Sort-Object -Unique)
}
function Test-ExactSIDSet([string[]]$Actual,[string[]]$Expected) {
  $actualUnique=@($Actual|Sort-Object -Unique)
  $expectedUnique=@($Expected|Sort-Object -Unique)
  return $actualUnique.Count -eq $expectedUnique.Count -and @(Compare-Object -ReferenceObject $expectedUnique -DifferenceObject $actualUnique).Count -eq 0
}
function Test-AnonymousAgentRejected([string]$URL,[string]$SensitiveValue) {
  try{
    $response=Invoke-WebRequest -Uri $URL -TimeoutSec 5 -SkipHttpErrorCheck
  }catch{
    # This probe deliberately sends no credential. Its diagnostic must also
    # stay credential-free when the endpoint cannot be reached.
    throw "anonymous management probe failed before receiving an HTTP response: $($_.Exception.Message)"
  }
  $body=[string]$response.Content
  if(-not [string]::IsNullOrEmpty($SensitiveValue) -and $body.IndexOf($SensitiveValue,[StringComparison]::Ordinal) -ge 0){
    throw 'anonymous management response leaked the protected access token'
  }
  if([int]$response.StatusCode -ne 401){
    throw "anonymous management request returned HTTP $([int]$response.StatusCode) instead of HTTP 401"
  }
  return 'HTTP 401; response did not contain the protected access token'
}
function Read-EnvironmentMap([object[]]$Entries) {
  $result=@{}
  foreach($entry in @($Entries)){
    if([string]$entry -match '^([^=]+)=(.*)$'){$result[$matches[1]]=$matches[2]}
  }
  return $result
}
function Read-PrivateEnvironmentMap([string]$Path) {
  $decoded=Get-Content -LiteralPath $Path -Raw -ErrorAction Stop|ConvertFrom-Json -ErrorAction Stop
  if($null -eq $decoded -or $decoded -is [Array]){throw 'private environment must contain one JSON object'}
  $result=@{}
  foreach($property in @($decoded.PSObject.Properties)){
    if($property.Value -isnot [string]){throw "private environment value $($property.Name) must be a string"}
    $result[[string]$property.Name]=[string]$property.Value
  }
  return $result
}
function ConvertFrom-Base64Key([string]$Value,[int]$ExpectedBytes,[string]$Name) {
  try{$bytes=[Convert]::FromBase64String(([string]$Value).Trim())}catch{throw "$Name is not valid base64"}
  if($bytes.Length -ne $ExpectedBytes){throw "$Name must decode to exactly $ExpectedBytes bytes"}
  return $bytes
}
function Test-SensitiveEnvironmentKey([string]$Key) {
  $key=$Key.Trim().ToUpperInvariant()
  if($key -eq 'STEWARD_LLM_ALLOW_NO_API_KEY'){return $false}
  if($key -like '*ENCRYPTION_KEY_ID'){return $false}
  return $key -eq 'DATABASE_URL' -or
    $key -like '*SECRET*' -or $key -like '*TOKEN*' -or $key -like '*PASSWORD*' -or
    $key -like '*API_KEY*' -or $key -like '*PRIVATE_KEY*' -or $key -like '*PREVIOUS_KEYS*' -or
    $key -like '*ENCRYPTION_KEY*' -or $key -like '*CLIENT_KEY*' -or $key -like '*CONTROL_KEY*' -or
    $key -like '*SIGNING_KEY*' -or $key -like '*CREDENTIAL*' -or $key -like '*CONNECTION_STRING*'
}
function Get-SCMEnvironment([string]$Name) {
  return @((Get-ItemProperty -LiteralPath "HKLM:\SYSTEM\CurrentControlSet\Services\$Name" -Name Environment -ErrorAction SilentlyContinue).Environment)
}
function Get-SensitiveSCMKeys([object[]]$Entries) {
  return @($Entries|ForEach-Object{
    $entry=[string]$_
    $separator=$entry.IndexOf('=')
    if($separator -gt 0){
      $key=$entry.Substring(0,$separator)
      if(Test-SensitiveEnvironmentKey $key){$key}
    }
  }|Sort-Object -Unique)
}
function Wait-JSONEndpoint([string]$URL,[string]$ExpectedStatus,[int]$TimeoutSeconds) {
  $response=$null;$lastError='';$deadline=(Get-Date).AddSeconds($TimeoutSeconds)
  do {
    try {
      $response=Invoke-RestMethod -Uri $URL -TimeoutSec 5
      if([string]$response.status -eq $ExpectedStatus){return $response}
      $lastError="unexpected response: $($response|ConvertTo-Json -Depth 8 -Compress)"
    } catch {$lastError=$_.Exception.Message}
    Start-Sleep -Milliseconds 500
  } while((Get-Date) -lt $deadline)
  throw "endpoint did not report $ExpectedStatus within ${TimeoutSeconds}s: $lastError"
}
function Find-ByteSequence([byte[]]$Bytes,[byte[]]$Needle) {
  for($index=0;$index -le $Bytes.Length-$Needle.Length;$index++){
    $matched=$true
    for($offset=0;$offset -lt $Needle.Length;$offset++){
      if($Bytes[$index+$offset] -ne $Needle[$offset]){$matched=$false;break}
    }
    if($matched){return $index}
  }
  return -1
}
function Invoke-CompanionPipeRequest([string]$Method,[string]$Path,[byte[]]$Body,[hashtable]$Headers) {
  $pipe=[IO.Pipes.NamedPipeClientStream]::new('.', $CompanionPipeName, [IO.Pipes.PipeDirection]::InOut, [IO.Pipes.PipeOptions]::Asynchronous)
  try{
    $pipe.Connect(3000)
    $requestHeaders=[Collections.Generic.List[string]]::new()
    $requestHeaders.Add("$Method $Path HTTP/1.1")
    $requestHeaders.Add('Host: steward-companion')
    $requestHeaders.Add('Connection: close')
    foreach($name in @($Headers.Keys|Sort-Object)){$requestHeaders.Add("${name}: $($Headers[$name])")}
    if($null -ne $Body){$requestHeaders.Add("Content-Length: $($Body.Length)")}
    $head=[Text.Encoding]::ASCII.GetBytes(($requestHeaders -join "`r`n")+"`r`n`r`n")
    $pipe.Write($head,0,$head.Length)
    if($null -ne $Body -and $Body.Length -gt 0){$pipe.Write($Body,0,$Body.Length)}
    $pipe.Flush()

    $buffer=New-Object byte[] 8192
    $output=[IO.MemoryStream]::new()
    $deadline=[DateTime]::UtcNow.AddSeconds(15)
    while($true){
      $remaining=[Math]::Max(1,[int]($deadline-[DateTime]::UtcNow).TotalMilliseconds)
      $pending=$pipe.BeginRead($buffer,0,$buffer.Length,$null,$null)
      if(-not $pending.AsyncWaitHandle.WaitOne($remaining)){throw 'timed out reading Companion named pipe response'}
      $read=$pipe.EndRead($pending)
      if($read -le 0){break}
      $output.Write($buffer,0,$read)
    }
    $raw=$output.ToArray()
    $separator=Find-ByteSequence $raw ([byte[]](13,10,13,10))
    if($separator -lt 0){throw 'Companion named pipe returned an invalid HTTP response'}
    $headText=[Text.Encoding]::ASCII.GetString($raw,0,$separator)
    $lines=@($headText -split "`r`n")
    if($lines.Count -eq 0 -or $lines[0] -notmatch '^HTTP/\d(?:\.\d)?\s+(\d{3})'){throw 'Companion named pipe response omitted an HTTP status'}
    $statusCode=[int]$matches[1]
    $bodyOffset=$separator+4
    $bodyLength=$raw.Length-$bodyOffset
    $responseHeaders=@{}
    foreach($line in @($lines|Select-Object -Skip 1)){
      $colon=$line.IndexOf(':')
      if($colon -gt 0){$responseHeaders[$line.Substring(0,$colon).Trim().ToLowerInvariant()]=$line.Substring($colon+1).Trim()}
    }
    if($responseHeaders.ContainsKey('transfer-encoding') -and $responseHeaders['transfer-encoding'] -match '(?i)chunked'){
      throw 'Companion named pipe returned unsupported chunked encoding'
    }
    if($responseHeaders.ContainsKey('content-length')){
      $declared=0
      if(-not [int]::TryParse([string]$responseHeaders['content-length'],[ref]$declared) -or $declared -lt 0 -or $declared -gt $bodyLength){throw 'Companion named pipe returned an invalid Content-Length'}
      $bodyLength=$declared
    }
    $bodyText=if($bodyLength -gt 0){[Text.Encoding]::UTF8.GetString($raw,$bodyOffset,$bodyLength)}else{''}
    return [pscustomobject]@{StatusCode=$statusCode;Body=$bodyText;Headers=$responseHeaders}
  }finally{$pipe.Dispose()}
}
function Invoke-ReadOnlyCompanionProbe([byte[]]$Key) {
  # fs.get_known_folders is an existing platform Session tool and has no
  # external side effects. This private manifest copy exercises the exact
  # authenticated steward-tool/1 path without creating durable Runtime rows.
  $probeScript=@'
$ErrorActionPreference='Stop'
try {
  $null=[Console]::In.ReadLine()|ConvertFrom-Json
  $userProfile=[Environment]::GetFolderPath('UserProfile')
  $output=[ordered]@{
    home=$userProfile
    desktop=[Environment]::GetFolderPath('Desktop')
    documents=[Environment]::GetFolderPath('MyDocuments')
    downloads=(Join-Path $userProfile 'Downloads')
  }
  [ordered]@{ok=$true;output=$output;evidence=@()}|ConvertTo-Json -Compress
} catch {
  [ordered]@{ok=$false;error=$_.Exception.Message;evidence=@()}|ConvertTo-Json -Compress
}
'@
  $manifest=[ordered]@{
    name='fs.get_known_folders';version='1.0.5';title='Windows known folders';description='Read the interactive user known folders.'
    origin='platform';runtime='powershell';execution_target='session';entrypoint='tool.ps1'
    input_schema=[ordered]@{type='object';properties=[ordered]@{};additionalProperties=$false}
    output_schema=[ordered]@{type='object'}
    files=@([ordered]@{path='tool.ps1';content=$probeScript})
    dependency_strategy=[ordered]@{requested='none';selected='none';selection_reason='Windows standard library only'}
    default_timeout_seconds=15;output_limit_bytes=65536;supports_cancel=$true;idempotency_mode='idempotent';side_effect='none'
  }
  $payload=[ordered]@{manifest=$manifest;package_dir='';input=[ordered]@{}}|ConvertTo-Json -Depth 20 -Compress
  $body=[Text.Encoding]::UTF8.GetBytes($payload)
  $timestamp=[DateTimeOffset]::UtcNow.ToUnixTimeSeconds().ToString([Globalization.CultureInfo]::InvariantCulture)
  $prefix=[Text.Encoding]::UTF8.GetBytes($timestamp+"`n")
  $signed=New-Object byte[] ($prefix.Length+$body.Length)
  [Array]::Copy($prefix,0,$signed,0,$prefix.Length);[Array]::Copy($body,0,$signed,$prefix.Length,$body.Length)
  $hmac=[Security.Cryptography.HMACSHA256]::new($Key)
  try{$signature=([BitConverter]::ToString($hmac.ComputeHash($signed))).Replace('-','').ToLowerInvariant()}finally{$hmac.Dispose()}
  $response=Invoke-CompanionPipeRequest 'POST' '/tools/execute' $body @{ 'Content-Type'='application/json';'X-Steward-Tool-Timestamp'=$timestamp;'X-Steward-Tool-Signature'=$signature }
  if($response.StatusCode -ne 200){throw "Companion tool endpoint returned HTTP $($response.StatusCode): $($response.Body)"}
  $decoded=$response.Body|ConvertFrom-Json
  if($decoded.ok -ne $true){throw "Companion fs.get_known_folders failed: $($decoded.error)"}
  if([string]::IsNullOrWhiteSpace([string]$decoded.output.home) -or [string]::IsNullOrWhiteSpace([string]$decoded.output.desktop)){throw 'Companion fs.get_known_folders omitted home or desktop'}
  return $decoded.output
}
function ConvertTo-ReleaseRelativePath([string]$Value,[string]$Label) {
  if([string]::IsNullOrWhiteSpace($Value)){throw "$Label must not be empty"}
  $relative=$Value -replace '\\','/'
  if([IO.Path]::IsPathRooted($relative) -or $relative.StartsWith('/') -or $relative -match '(^|/)\.\.(/|$)' -or $relative -match '(^|/)\.(/|$)'){
    throw "$Label is not a safe canonical relative path: $Value"
  }
  if($relative.EndsWith('/') -or $relative.Contains('//')){throw "$Label is not a canonical file path: $Value"}
  return $relative
}
function Get-RequiredInstalledReleaseFiles {
  return @(
    'steward.exe','steward-broker.exe','steward-approval.exe','steward-companion.exe','steward-system-tool-host.exe',
    'windows-notifier/steward-windows-notifier.exe','ui/index.html',
    'install-steward-production.ps1','update-steward-production.ps1','uninstall-steward-production.ps1',
    'test-steward-production.ps1','install-steward-companion.ps1','uninstall-steward-companion.ps1',
    'migrate-steward-production.ps1','rotate-steward-broker-keys.ps1','test-steward-broker-session0.ps1',
    'verify-steward-dist.ps1'
  )
}
function Assert-ReleaseTrustProtection([string]$TrustPath) {
  $item=Get-Item -LiteralPath $TrustPath -Force -ErrorAction Stop
  if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw 'release trust anchor must not be a reparse point'}
  $acl=Get-Acl -LiteralPath $TrustPath
  if(-not $acl.AreAccessRulesProtected){throw 'release trust anchor inherits ACLs instead of using a protected ACL'}
  $owner=ConvertTo-SIDValue $acl.Owner
  if($owner -ne 'S-1-5-32-544'){throw "release trust anchor owner must be BUILTIN\Administrators, got $owner"}
  # Do not build this mask from Modify/FullControl: both composite values carry
  # Synchronize, which is also legitimately present in ReadAndExecute and would
  # therefore misclassify a read-only service ACE as writable.
  $writeMask=[Security.AccessControl.FileSystemRights]::WriteData -bor
    [Security.AccessControl.FileSystemRights]::AppendData -bor
    [Security.AccessControl.FileSystemRights]::WriteExtendedAttributes -bor
    [Security.AccessControl.FileSystemRights]::WriteAttributes -bor
    [Security.AccessControl.FileSystemRights]::DeleteSubdirectoriesAndFiles -bor
    [Security.AccessControl.FileSystemRights]::Delete -bor
    [Security.AccessControl.FileSystemRights]::ChangePermissions -bor
    [Security.AccessControl.FileSystemRights]::TakeOwnership
  foreach($rule in @($acl.GetAccessRules($true,$false,[Security.Principal.SecurityIdentifier]))){
    $sid=[string]$rule.IdentityReference.Value
    if($rule.AccessControlType -eq [Security.AccessControl.AccessControlType]::Allow -and $sid -notin @('S-1-5-18','S-1-5-32-544') -and (($rule.FileSystemRights -band $writeMask) -ne 0)){
      throw "release trust anchor grants write-capable access to untrusted SID $sid"
    }
  }
}
function Assert-InstalledReleaseCryptographicTrust([string]$Root,[object]$Trust,[object]$Manifest) {
  $pinned=([string]$Trust.signer_thumbprint -replace '\s','').ToUpperInvariant()
  if($pinned -notmatch '^[A-F0-9]{40}$'){throw 'release trust anchor contains an invalid signer thumbprint'}
  $manifestSigner=([string]$Manifest.signing.signer_thumbprint -replace '\s','').ToUpperInvariant()
  if($manifestSigner -ne $pinned){throw 'installed release manifest signer does not match the protected signer pin'}
  $signatureRelative=ConvertTo-ReleaseRelativePath ([string]$Trust.manifest_signature_path) 'manifest signature path'
  $catalogRelative=ConvertTo-ReleaseRelativePath ([string]$Trust.package_catalog_path) 'package catalog path'
  if($signatureRelative -ne (ConvertTo-ReleaseRelativePath ([string]$Manifest.signing.manifest_signature) 'manifest-declared signature path')){throw 'manifest signature path does not match the protected baseline'}
  if($catalogRelative -ne (ConvertTo-ReleaseRelativePath ([string]$Manifest.signing.package_catalog) 'manifest-declared catalog path')){throw 'package catalog path does not match the protected baseline'}
  $manifestPath=Join-Path $Root 'release-manifest.json';$signaturePath=Join-Path $Root ($signatureRelative -replace '/','\');$catalogPath=Join-Path $Root ($catalogRelative -replace '/','\')

  Add-Type -AssemblyName System.Security.Cryptography.Pkcs
  $content=[Security.Cryptography.Pkcs.ContentInfo]::new([IO.File]::ReadAllBytes($manifestPath))
  $cms=[Security.Cryptography.Pkcs.SignedCms]::new($content,$true)
  try{$cms.Decode([IO.File]::ReadAllBytes($signaturePath));$cms.CheckSignature($true)}catch{throw "detached release manifest signature is invalid: $($_.Exception.Message)"}
  if($cms.SignerInfos.Count -ne 1 -or $null -eq $cms.SignerInfos[0].Certificate){throw 'detached release manifest signature must contain exactly one signer certificate'}
  $cmsSigner=($cms.SignerInfos[0].Certificate.Thumbprint -replace '\s','').ToUpperInvariant()
  if($cmsSigner -ne $pinned){throw 'detached release manifest signature does not match the protected signer pin'}

  $catalogSignature=Get-AuthenticodeSignature -LiteralPath $catalogPath
  $catalogSigner=if($catalogSignature.SignerCertificate){($catalogSignature.SignerCertificate.Thumbprint -replace '\s','').ToUpperInvariant()}else{''}
  if($catalogSignature.Status -ne [Management.Automation.SignatureStatus]::Valid -or $catalogSigner -ne $pinned){throw "installed package catalog is not validly signed by the protected signer pin: status=$($catalogSignature.Status)"}
  if($null -eq $catalogSignature.TimeStamperCertificate){throw 'installed package catalog does not contain a trusted timestamp'}
  $catalogStatus=Test-FileCatalog -Path $Root -CatalogFilePath $catalogPath -FilesToSkip @('release-trust.json')
  if($catalogStatus -ne [Management.Automation.CatalogValidationStatus]::Valid){throw "installed release package catalog validation failed: $catalogStatus"}
}
function Assert-InstalledReleaseBaseline([string]$Root,[bool]$AllowUnsigned=$false) {
  $rootPath=[IO.Path]::GetFullPath($Root).TrimEnd('\')
  $trustPath=Join-Path $rootPath 'release-trust.json'
  if(-not(Test-Path -LiteralPath $trustPath -PathType Leaf)){throw "protected installed release trust anchor is missing: $trustPath"}
  $trust=Get-Content -LiteralPath $trustPath -Raw|ConvertFrom-Json
  if([string]$trust.schema -ne 'mongojson.steward.release-trust/v2'){throw 'installed release trust anchor has no immutable v2 authentication baseline; perform a verified update or reinstall'}
  if([string]$trust.undeclared_files_policy -ne 'deny'){throw 'release trust anchor does not deny undeclared installation files'}
  $records=@($trust.authenticated_files)
  if($records.Count -eq 0){throw 'release trust anchor contains no authenticated files'}
  $baseline=@{};$payloadBaseline=@{}
  foreach($record in $records){
    $relative=ConvertTo-ReleaseRelativePath ([string]$record.path) 'release trust file path'
    $key=$relative.ToLowerInvariant();if($baseline.ContainsKey($key)){throw "release trust anchor contains a duplicate file path: $relative"}
    $expected=([string]$record.sha256).ToLowerInvariant();if($expected -notmatch '^[a-f0-9]{64}$'){throw "release trust anchor contains an invalid SHA-256 hash: $relative"}
    $kind=[string]$record.kind;if($kind -notin @('payload','manifest','checksums','manifest_signature','package_catalog')){throw "release trust anchor contains an unsupported file kind '$kind': $relative"}
    $full=[IO.Path]::GetFullPath((Join-Path $rootPath ($relative -replace '/','\')))
    if(-not $full.StartsWith($rootPath+'\',[StringComparison]::OrdinalIgnoreCase)){throw "release trust file path escapes the installation root: $relative"}
    if(-not(Test-Path -LiteralPath $full -PathType Leaf)){throw "authenticated installed release file is missing: $relative"}
    $item=Get-Item -LiteralPath $full -Force;if(($item.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "authenticated installed release file must not be a reparse point: $relative"}
    $actual=(Get-FileHash -Algorithm SHA256 -LiteralPath $full).Hash.ToLowerInvariant();if($actual -ne $expected){throw "installed release hash does not match the protected baseline: $relative"}
    $baseline[$key]=[pscustomobject]@{path=$relative;sha256=$expected;kind=$kind}
    if($kind -eq 'payload'){$payloadBaseline[$key]=$expected}
  }
  foreach($directory in @(Get-ChildItem -LiteralPath $rootPath -Force -Recurse -Directory)){
    if(($directory.Attributes -band [IO.FileAttributes]::ReparsePoint) -ne 0){throw "installed release directory must not be a reparse point: $($directory.FullName)"}
  }
  $actualFiles=@{}
  foreach($file in @(Get-ChildItem -LiteralPath $rootPath -Force -Recurse -File)){
    $relative=$file.FullName.Substring($rootPath.Length).TrimStart('\') -replace '\\','/';$key=$relative.ToLowerInvariant()
    if($key -ne 'release-trust.json' -and -not $baseline.ContainsKey($key)){throw "installed release contains an undeclared file: $relative"}
    $actualFiles[$key]=$true
  }
  if($actualFiles.Count -ne $baseline.Count+1 -or -not $actualFiles.ContainsKey('release-trust.json')){throw 'installed release file set does not exactly match the protected baseline'}
  foreach($required in Get-RequiredInstalledReleaseFiles){if(-not $baseline.ContainsKey($required.ToLowerInvariant())){throw "protected release baseline is missing required file: $required"}}
  foreach($metadata in @(@('release-manifest.json','manifest'),@('sha256sums.txt','checksums'))){
    if(-not $baseline.ContainsKey($metadata[0]) -or $baseline[$metadata[0]].kind -ne $metadata[1]){throw "protected release baseline is missing required $($metadata[1]) metadata"}
  }

  $manifest=Get-Content -LiteralPath (Join-Path $rootPath 'release-manifest.json') -Raw|ConvertFrom-Json
  if([string]$manifest.schema -ne 'mongojson.steward.release/v1' -or [string]$manifest.target -ne 'windows/amd64'){throw 'installed release manifest has an unsupported schema or target'}
  if([string]$manifest.version -ne [string]$trust.release_version -or [string]$manifest.commit -ne [string]$trust.release_commit){throw 'installed release identity does not match the protected baseline'}
  $manifestTicks=0L;$baselineTicks=0L
  if($manifest.built_at -is [DateTime]){$manifestTicks=([DateTime]$manifest.built_at).ToUniversalTime().Ticks}else{$manifestBuiltAt=[DateTimeOffset]::MinValue;if(-not[DateTimeOffset]::TryParse([string]$manifest.built_at,[ref]$manifestBuiltAt)){$manifestTicks=-1}else{$manifestTicks=$manifestBuiltAt.UtcTicks}}
  if(-not[Int64]::TryParse([string]$trust.release_built_at_utc_ticks,[Globalization.NumberStyles]::None,[Globalization.CultureInfo]::InvariantCulture,[ref]$baselineTicks) -or $manifestTicks -ne $baselineTicks){throw "installed release built_at does not match the protected baseline: manifest=$($manifest.built_at); baseline_ticks=$($trust.release_built_at_utc_ticks)"}
  $manifestPayload=@{}
  foreach($record in @($manifest.files)){
    $relative=ConvertTo-ReleaseRelativePath ([string]$record.path) 'manifest payload path';$key=$relative.ToLowerInvariant()
    $hash=([string]$record.sha256).ToLowerInvariant();if($manifestPayload.ContainsKey($key) -or $hash -notmatch '^[a-f0-9]{64}$'){throw "installed release manifest contains an invalid or duplicate payload record: $relative"}
    $manifestPayload[$key]=$hash
  }
  if($manifestPayload.Count -ne $payloadBaseline.Count){throw 'installed release manifest payload set does not match the protected baseline'}
  foreach($key in $payloadBaseline.Keys){if(-not $manifestPayload.ContainsKey($key) -or $manifestPayload[$key] -ne $payloadBaseline[$key]){throw "installed release manifest payload does not match the protected baseline: $($baseline[$key].path)"}}

  $signatureRequired=[bool]$trust.signature_required
  if([bool]$manifest.signing.required -ne $signatureRequired){throw 'installed release signature requirement does not match the protected baseline'}
  if($signatureRequired){
    foreach($metadata in @(@((ConvertTo-ReleaseRelativePath ([string]$trust.manifest_signature_path) 'manifest signature path').ToLowerInvariant(),'manifest_signature'),@((ConvertTo-ReleaseRelativePath ([string]$trust.package_catalog_path) 'package catalog path').ToLowerInvariant(),'package_catalog'))){
      if(-not $baseline.ContainsKey($metadata[0]) -or $baseline[$metadata[0]].kind -ne $metadata[1]){throw "protected release baseline is missing required $($metadata[1]) metadata"}
    }
    Assert-InstalledReleaseCryptographicTrust $rootPath $trust $manifest
  }elseif(-not $AllowUnsigned){throw 'installed release is an unsigned development override; production verification requires a signed and timestamped release'}
  return [pscustomobject]@{file_count=$baseline.Count;signed=$signatureRequired;signer=([string]$trust.signer_thumbprint -replace '\s','').ToUpperInvariant()}
}

$main = Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
Add-Check "main.service" ($null -ne $main -and $main.State -eq "Running") $(if($main){"state=$($main.State)"}else{"missing"})
Add-Check "main.account" ($null -ne $main -and $main.StartName -eq "NT AUTHORITY\LocalService") $(if($main){"account=$($main.StartName)"}else{"missing"})
$sidOutput = (& sc.exe qsidtype $ServiceName 2>&1 | Out-String)
Add-Check "main.restricted_sid" ($LASTEXITCODE -eq 0 -and $sidOutput -match "RESTRICTED") $sidOutput.Trim()

$broker = Get-CimInstance Win32_Service -Filter "Name='$BrokerServiceName'" -ErrorAction SilentlyContinue
Add-Check "broker.service" ($null -ne $broker -and $broker.State -eq "Running") $(if($broker){"state=$($broker.State)"}else{"missing"})
Add-Check "broker.account" ($null -ne $broker -and $broker.StartName -eq "LocalSystem") $(if($broker){"account=$($broker.StartName)"}else{"missing"})

try{
  $trustPath=Join-Path $InstallDir 'release-trust.json';Assert-ReleaseTrustProtection $trustPath
  $integrity=Assert-InstalledReleaseBaseline $InstallDir ([bool]$AllowUnsignedReleaseBaseline)
  $trustDescription=if($integrity.signed){"signed by pinned signer $($integrity.signer)"}else{'explicit unsigned development baseline'}
  Add-Check 'main.installed_release_integrity' $true "verified exact $($integrity.file_count)-file protected authentication baseline; $trustDescription"
}catch{Add-Check 'main.installed_release_integrity' $false $_.Exception.Message}

$health=$null
try{$health=Wait-JSONEndpoint $HealthURL 'ok' $StartupTimeoutSeconds;Add-Check 'main.health' $true ($health|ConvertTo-Json -Compress)}catch{
  $healthDetail=$_.Exception.Message
  $serviceLog=Join-Path (Join-Path $MainDataDir 'logs') ($ServiceName+'.log')
  if(Test-Path -LiteralPath $serviceLog){$healthDetail+="; inspect the protected service log at $serviceLog"}
  Add-Check 'main.health' $false $healthDetail
}
$ready=$null
try{
  $ready=Wait-JSONEndpoint $ReadyURL 'ready' $StartupTimeoutSeconds
  if($null -ne $ready.PSObject.Properties['checks'] -or $null -ne $ready.PSObject.Properties['error']){throw 'anonymous readiness response exposed internal diagnostics'}
  Add-Check 'main.readiness_public' $true ($ready|ConvertTo-Json -Compress)
}catch{Add-Check 'main.readiness_public' $false $_.Exception.Message}
$mainAfterStartup=Get-CimInstance Win32_Service -Filter "Name='$ServiceName'" -ErrorAction SilentlyContinue
Add-Check 'main.running_after_startup' ($null -ne $mainAfterStartup -and $mainAfterStartup.State -eq 'Running') $(if($mainAfterStartup){"state=$($mainAfterStartup.State); exit_code=$($mainAfterStartup.ExitCode)"}else{'missing'})
$mainListener = Get-NetTCPConnection -LocalPort $mainListenPort -State Listen -ErrorAction SilentlyContinue
Add-Check "main.loopback" ($null -ne $mainListener -and @($mainListener | Where-Object LocalAddress -notin @("127.0.0.1","::1")).Count -eq 0) (($mainListener | Select-Object LocalAddress,LocalPort | ConvertTo-Json -Compress))
$brokerListener = Get-NetTCPConnection -LocalPort $brokerListenPort -State Listen -ErrorAction SilentlyContinue
Add-Check "broker.loopback" ($null -ne $brokerListener -and @($brokerListener | Where-Object LocalAddress -notin @("127.0.0.1","::1")).Count -eq 0) (($brokerListener | Select-Object LocalAddress,LocalPort | ConvertTo-Json -Compress))

$mainPrivate=Join-Path $MainDataDir 'config\service-secrets.json';$brokerPrivate=Join-Path $BrokerDataDir 'service-secrets.json';$policy=Join-Path $BrokerDataDir 'policy.json'
Add-Check 'main.private_environment' (Test-Path $mainPrivate) $mainPrivate
Add-Check 'broker.private_environment' (Test-Path $brokerPrivate) $brokerPrivate
$mainSecrets=@{};$mainSecretsError=''
if(Test-Path $mainPrivate){
  try{
    $mainSID=([Security.Principal.NTAccount]::new("NT SERVICE\$ServiceName")).Translate([Security.Principal.SecurityIdentifier]).Value
    $mainACL=Get-Acl -LiteralPath $mainPrivate
    $mainOwnerSID=ConvertTo-SIDValue $mainACL.Owner
    $mainAllowSIDs=@(Get-UniqueAllowSIDs $mainACL)
    $expectedMainAllowSIDs=@('S-1-5-18','S-1-5-32-544',$mainSID)
    $mainIsolated=$mainACL.AreAccessRulesProtected -and $mainOwnerSID -eq 'S-1-5-32-544' -and (Test-ExactSIDSet $mainAllowSIDs $expectedMainAllowSIDs)
    Add-Check 'main.service_sid_secret_acl' $mainIsolated "protected=$($mainACL.AreAccessRulesProtected); owner_sid=$mainOwnerSID; allow_sids=$($mainAllowSIDs -join ',')"
  }catch{Add-Check 'main.service_sid_secret_acl' $false $_.Exception.Message}
  try{$mainSecrets=Read-PrivateEnvironmentMap $mainPrivate;Add-Check 'main.private_environment_format' $true "valid JSON object with $($mainSecrets.Count) protected values"}catch{$mainSecretsError=$_.Exception.Message;Add-Check 'main.private_environment_format' $false $mainSecretsError}
}
if(Test-Path $brokerPrivate){
  try{
    $brokerSID=([Security.Principal.NTAccount]::new("NT SERVICE\$BrokerServiceName")).Translate([Security.Principal.SecurityIdentifier]).Value
    $acl=Get-Acl -LiteralPath $brokerPrivate
    $brokerOwnerSID=ConvertTo-SIDValue $acl.Owner
    $allowSIDs=@(Get-UniqueAllowSIDs $acl)
    # Broker secrets deliberately omit LocalSystem. The Broker's restricted
    # service token must satisfy the dedicated Service SID ACE; capability
    # children retain SYSTEM but cannot use the Broker Service SID.
    $expectedBrokerAllowSIDs=@('S-1-5-32-544',$brokerSID)
    $isolated=$acl.AreAccessRulesProtected -and $brokerOwnerSID -eq 'S-1-5-32-544' -and (Test-ExactSIDSet $allowSIDs $expectedBrokerAllowSIDs)
    Add-Check 'broker.service_sid_secret_acl' $isolated "protected=$($acl.AreAccessRulesProtected); owner_sid=$brokerOwnerSID; allow_sids=$($allowSIDs -join ',')"
  }catch{Add-Check 'broker.service_sid_secret_acl' $false $_.Exception.Message}
}

$registryEnvironment=Get-SCMEnvironment $ServiceName
$publicEnvironment=Read-EnvironmentMap $registryEnvironment
$mainSCMLeaks=Get-SensitiveSCMKeys $registryEnvironment
Add-Check 'main.scm_secrets_absent' ($mainSCMLeaks.Count -eq 0) $(if($mainSCMLeaks.Count){"sensitive key names remain in SCM Environment: $($mainSCMLeaks -join ',')"}else{'SCM Environment contains no private keys, tokens, credentials, passwords, or connection strings'})
$brokerRegistryEnvironment=Get-SCMEnvironment $BrokerServiceName
$brokerSCMLeaks=Get-SensitiveSCMKeys $brokerRegistryEnvironment
Add-Check 'broker.scm_secrets_absent' ($brokerSCMLeaks.Count -eq 0) $(if($brokerSCMLeaks.Count){"sensitive key names remain in SCM Environment: $($brokerSCMLeaks -join ',')"}else{'SCM Environment contains no private keys, tokens, credentials, passwords, or connection strings'})

$orchestrationKeyValid=$false;$orchestrationKeyDetail='STEWARD_ORCHESTRATION_SIGNING_KEY is missing from the protected main environment'
if($mainSecrets.ContainsKey('STEWARD_ORCHESTRATION_SIGNING_KEY')){
  try{[void](ConvertFrom-Base64Key $mainSecrets['STEWARD_ORCHESTRATION_SIGNING_KEY'] 32 'STEWARD_ORCHESTRATION_SIGNING_KEY');$orchestrationKeyValid=$true;$orchestrationKeyDetail='valid 32-byte base64 Ed25519 seed; absent from SCM Environment'}catch{$orchestrationKeyDetail=$_.Exception.Message}
}
if($mainSCMLeaks -contains 'STEWARD_ORCHESTRATION_SIGNING_KEY'){$orchestrationKeyValid=$false;$orchestrationKeyDetail='STEWARD_ORCHESTRATION_SIGNING_KEY leaked into SCM Environment'}
Add-Check 'main.orchestration_signing_key' $orchestrationKeyValid $orchestrationKeyDetail

$managementToken=if($mainSecrets.ContainsKey('STEWARD_MANAGEMENT_AUTH_TOKEN')){[string]$mainSecrets['STEWARD_MANAGEMENT_AUTH_TOKEN']}else{''}
if($managementToken.Length -lt 32){
  Add-Check 'main.agent_runtime' $false 'STEWARD_MANAGEMENT_AUTH_TOKEN is missing or shorter than 32 characters in the protected main environment'
  Add-Check 'main.management_access_token_file' $false 'management access token file cannot be validated because the protected service token is missing or invalid'
}else{
  try{
    $detailed=Invoke-RestMethod -Uri $DetailedReadyURL -Headers @{Authorization="Bearer $managementToken"} -TimeoutSec 5
    if([string]$detailed.status -ne 'ready' -or [string]$detailed.checks.steward_runtime -ne 'ok'){throw 'authenticated readiness did not prove steward_runtime=ok'}
    Add-Check 'main.readiness_authenticated' $true 'authenticated readiness returned detailed successful checks'
  }catch{Add-Check 'main.readiness_authenticated' $false $_.Exception.Message}
  try{
    $origin=$healthURI.GetLeftPart([UriPartial]::Authority)
    $page=Invoke-WebRequest -Uri ($origin + '/') -Headers @{Authorization="Bearer $managementToken"} -TimeoutSec 10 -SkipHttpErrorCheck
    if([int]$page.StatusCode -ne 200 -or [string]$page.Content -notmatch '<div\s+id=["'']root["'']'){throw "workspace root returned HTTP $([int]$page.StatusCode) or did not contain the application root"}
    $assetMatch=[regex]::Match([string]$page.Content,'(?:src|href)=["''](?<path>/assets/[^"'']+)["'']')
    if(-not $assetMatch.Success){throw 'workspace HTML did not reference a bundled asset'}
    $asset=Invoke-WebRequest -Uri ($origin + $assetMatch.Groups['path'].Value) -Headers @{Authorization="Bearer $managementToken"} -TimeoutSec 10 -SkipHttpErrorCheck
    if([int]$asset.StatusCode -ne 200 -or $asset.RawContentLength -le 0){throw "bundled UI asset returned HTTP $([int]$asset.StatusCode)"}
    Add-Check 'main.ui' $true "root and bundled asset are readable through the installed service"
  }catch{Add-Check 'main.ui' $false $_.Exception.Message}
  try{
    $anonymousBefore=Test-AnonymousAgentRejected $AgentURL $managementToken
    Add-Check 'main.management_auth_anonymous_before' $true $anonymousBefore
  }catch{Add-Check 'main.management_auth_anonymous_before' $false $_.Exception.Message}
  $agent=$null;$agentError='';$agentDeadline=(Get-Date).AddSeconds($StartupTimeoutSeconds)
  do{
    try{
      # The token is deliberately used only in this Authorization header and is
      # never placed in output, process environment, URLs, or temporary files.
      $response=Invoke-RestMethod -Uri $AgentURL -Headers @{Authorization="Bearer $managementToken"} -TimeoutSec 5
      $agent=$response.agent
      $runtimeLoop=@($agent.background_loops|Where-Object name -eq 'runtime-v2'|Select-Object -First 1)
      $successAt=$null
      if($runtimeLoop.Count -eq 1 -and $runtimeLoop[0].last_success_at){$successAt=[DateTimeOffset]::Parse([string]$runtimeLoop[0].last_success_at,[Globalization.CultureInfo]::InvariantCulture)}
      $age=if($successAt){([DateTimeOffset]::UtcNow-$successAt).TotalSeconds}else{[double]::PositiveInfinity}
      $freshEnough=$RuntimeSuccessMaxAgeSeconds -eq 0 -or $age -le $RuntimeSuccessMaxAgeSeconds
      if($agent.status -eq 'running' -and $runtimeLoop.Count -eq 1 -and $runtimeLoop[0].enabled -eq $true -and $runtimeLoop[0].running -eq $true -and [int]$runtimeLoop[0].consecutive_failures -lt 3 -and $age -ge -60 -and $freshEnough){
        $agentError='';break
      }
      $agentError="agent=$($agent.status); runtime-v2 enabled=$($runtimeLoop[0].enabled) running=$($runtimeLoop[0].running) consecutive_failures=$($runtimeLoop[0].consecutive_failures) last_success_at=$($runtimeLoop[0].last_success_at)"
    }catch{$agentError=$_.Exception.Message}
    Start-Sleep -Milliseconds 500
  }while((Get-Date)-lt $agentDeadline)
  if($agentError){Add-Check 'main.agent_runtime' $false "runtime-v2 did not become healthy within ${StartupTimeoutSeconds}s: $agentError"}else{
    $runtimeLoop=@($agent.background_loops|Where-Object name -eq 'runtime-v2'|Select-Object -First 1)[0]
    $age=[Math]::Max(0,[int]([DateTimeOffset]::UtcNow-[DateTimeOffset]::Parse([string]$runtimeLoop.last_success_at,[Globalization.CultureInfo]::InvariantCulture)).TotalSeconds)
    Add-Check 'main.agent_runtime' $true "agent=running; runtime-v2 enabled=true running=true consecutive_failures=$($runtimeLoop.consecutive_failures) last_success_age_seconds=$age"
  }
  try{
    $anonymousAfter=Test-AnonymousAgentRejected $AgentURL $managementToken
    Add-Check 'main.management_auth_anonymous_after' $true $anonymousAfter
  }catch{Add-Check 'main.management_auth_anonymous_after' $false $_.Exception.Message}
  try{
    if(-not(Test-Path -LiteralPath $ManagementAccessTokenFile -PathType Leaf)){throw "management access token file is missing: $ManagementAccessTokenFile"}
    $accessToken=[IO.File]::ReadAllText($ManagementAccessTokenFile).Trim()
    if(-not($accessToken -ceq $managementToken)){throw 'management access token file does not match the protected service token'}
    $currentUserSID=[Security.Principal.WindowsIdentity]::GetCurrent().User.Value
    $accessACL=Get-Acl -LiteralPath $ManagementAccessTokenFile
    $accessOwnerSID=ConvertTo-SIDValue $accessACL.Owner
    $accessAllowSIDs=@(Get-UniqueAllowSIDs $accessACL)
    $expectedAccessSIDs=@($currentUserSID,'S-1-5-18','S-1-5-32-544')
    $accessFileIsolated=$accessACL.AreAccessRulesProtected -and $accessOwnerSID -eq $currentUserSID -and (Test-ExactSIDSet $accessAllowSIDs $expectedAccessSIDs)
    if(-not $accessFileIsolated){throw "management access token file ACL is not isolated; protected=$($accessACL.AreAccessRulesProtected); owner_sid=$accessOwnerSID; allow_sids=$($accessAllowSIDs -join ',')"}
    Add-Check 'main.management_access_token_file' $true "path=$ManagementAccessTokenFile; protected=true; owner_sid=$accessOwnerSID; allow_sids=$($accessAllowSIDs -join ',')"
    $accessToken=$null
  }catch{Add-Check 'main.management_access_token_file' $false $_.Exception.Message}
}
$managementToken=$null

$systemHost=Join-Path $InstallDir 'steward-system-tool-host.exe';$brokerCLI=Join-Path $InstallDir 'steward-broker.exe'
Add-Check 'broker.system_tool_host' (Test-Path $systemHost) $systemHost
if((Test-Path $systemHost) -and (Test-Path $policy)){
  try{$catalog=& $systemHost catalog|ConvertFrom-Json;Add-Check 'broker.system_tool_catalog' ($catalog.protocol -eq 'steward-system-tool-catalog/1' -and $catalog.tools.Count -gt 0) "tools=$($catalog.tools.Count)"}catch{Add-Check 'broker.system_tool_catalog' $false $_.Exception.Message}
  try{$validated=& $brokerCLI validate-policy --policy $policy|ConvertFrom-Json;$parameterized=@($validated.capabilities|Where-Object accepts_input);Add-Check 'broker.parameterized_policy' ($validated.valid -and $parameterized.Count -gt 0) "parameterized_capabilities=$($parameterized.Count)"}catch{Add-Check 'broker.parameterized_policy' $false $_.Exception.Message}
}
if((Test-Path $brokerPrivate) -and (Test-Path $mainPrivate) -and (Test-Path $brokerCLI)){
  $oldClient=$env:STEWARD_BROKER_CLIENT_KEY;$oldPublic=$env:STEWARD_BROKER_PUBLIC_KEY;$oldURL=$env:STEWARD_BROKER_URL
  try{
    $bs=Get-Content $brokerPrivate -Raw|ConvertFrom-Json
    $brokerPublicKey=[string]$publicEnvironment['STEWARD_BROKER_PUBLIC_KEY']
    if($brokerPublicKey -notmatch '^[A-Za-z0-9+/]{43}=$'){throw 'STEWARD_BROKER_PUBLIC_KEY is missing or invalid in the main service SCM Environment'}
    $env:STEWARD_BROKER_CLIENT_KEY=$bs.STEWARD_BROKER_CLIENT_KEY;$env:STEWARD_BROKER_PUBLIC_KEY=$brokerPublicKey;$env:STEWARD_BROKER_URL='http://127.0.0.1:18100'
    $smoke=& $brokerCLI tool-execute --capability tool:system.uptime --arguments-json '{}' 2>&1|Out-String
    $smokeExit=$LASTEXITCODE
    if($smokeExit -ne 0){
      Add-Check 'broker.system_tool_execution' $false "Broker CLI failed with exit code ${smokeExit}: $($smoke.Trim())"
    }else{
      $brokerResult=$smoke|ConvertFrom-Json
      $toolResult=([string]$brokerResult.stdout)|ConvertFrom-Json
      $receipt=$brokerResult.receipt.payload
      $hasUptime=$null -ne $toolResult.output -and $null -ne $toolResult.output.PSObject.Properties['uptime_seconds']
      $receiptOK=$null -ne $receipt -and $receipt.succeeded -eq $true -and $receipt.audit_persisted -eq $true -and $receipt.capability -eq 'tool:system.uptime' -and -not [string]::IsNullOrWhiteSpace([string]$brokerResult.receipt.key_id) -and -not [string]::IsNullOrWhiteSpace([string]$brokerResult.receipt.signature)
      $toolOK=$toolResult.ok -eq $true -and $hasUptime
      if($receiptOK -and $toolOK){
        Add-Check 'broker.system_tool_execution' $true "system.uptime returned a verified signed Broker receipt and uptime_seconds=$($toolResult.output.uptime_seconds)"
      }else{
        $reason=if($toolResult.ok -ne $true){"tool host error: $($toolResult.error)"}elseif(-not $hasUptime){'tool host response omitted output.uptime_seconds'}else{'signed receipt fields did not match the requested successful audited capability'}
        Add-Check 'broker.system_tool_execution' $false $reason
      }
    }
  }catch{Add-Check 'broker.system_tool_execution' $false $_.Exception.Message}
  finally{$env:STEWARD_BROKER_CLIENT_KEY=$oldClient;$env:STEWARD_BROKER_PUBLIC_KEY=$oldPublic;$env:STEWARD_BROKER_URL=$oldURL}
}

$task = Get-ScheduledTask -TaskName $CompanionTaskName -ErrorAction SilentlyContinue
if($RequireCompanion){
  $taskOK=$null -ne $task -and $task.Principal.RunLevel -eq 'Limited' -and $task.State -eq 'Running'
  Add-Check 'companion.task' $taskOK $(if($task){"state=$($task.State); run_level=$($task.Principal.RunLevel)"}else{'missing'})
  $expectedCompanion=if($task -and $task.Actions.Count -gt 0){[Environment]::ExpandEnvironmentVariables([string]$task.Actions[0].Execute)}else{''}
  $companionProcesses=@(Get-CimInstance Win32_Process -Filter "Name='steward-companion.exe'" -ErrorAction SilentlyContinue|Where-Object{
    $path=[string]$_.ExecutablePath
    -not [string]::IsNullOrWhiteSpace($path) -and -not [string]::IsNullOrWhiteSpace($expectedCompanion) -and
      [IO.Path]::GetFullPath($path).Equals([IO.Path]::GetFullPath($expectedCompanion),[StringComparison]::OrdinalIgnoreCase)
  })
  $interactiveProcesses=@($companionProcesses|Where-Object{[int]$_.SessionId -gt 0})
  Add-Check 'companion.interactive_process' ($interactiveProcesses.Count -gt 0) $(if($interactiveProcesses.Count){"pid=$($interactiveProcesses[0].ProcessId); session_id=$($interactiveProcesses[0].SessionId); executable=$expectedCompanion"}else{"no matching Companion process in an interactive session; expected=$expectedCompanion"})

  $pipeReady=$false
  try{
    $statusResponse=Invoke-CompanionPipeRequest 'GET' '/status' ([byte[]]::new(0)) @{}
    $status=if($statusResponse.Body){$statusResponse.Body|ConvertFrom-Json}else{$null}
    $pipeReady=$statusResponse.StatusCode -eq 200 -and $status.status -eq 'ready'
    Add-Check 'companion.named_pipe' $pipeReady $(if($pipeReady){"\\.\pipe\$CompanionPipeName returned status=ready"}else{"\\.\pipe\$CompanionPipeName returned HTTP $($statusResponse.StatusCode): $($statusResponse.Body)"})
  }catch{Add-Check 'companion.named_pipe' $false $_.Exception.Message}

  if($pipeReady -and $mainSecrets.ContainsKey('STEWARD_LOCAL_ENCRYPTION_KEY')){
    try{
      $localKey=ConvertFrom-Base64Key $mainSecrets['STEWARD_LOCAL_ENCRYPTION_KEY'] 32 'STEWARD_LOCAL_ENCRYPTION_KEY'
      $knownFolders=Invoke-ReadOnlyCompanionProbe $localKey
      Add-Check 'companion.read_only_session_tool' $true "fs.get_known_folders succeeded for interactive home=$($knownFolders.home)"
    }catch{Add-Check 'companion.read_only_session_tool' $false $_.Exception.Message}
  }else{
    $reason=if(-not $pipeReady){'named pipe is unavailable; fs.get_known_folders was not executed'}else{'STEWARD_LOCAL_ENCRYPTION_KEY is unavailable; authenticated fs.get_known_folders was not executed'}
    Add-Check 'companion.read_only_session_tool' $false $reason
  }
}else{
  Add-Check 'companion.task' $true $(if($task){"optional; state=$($task.State); run_level=$($task.Principal.RunLevel)"}else{'optional; not installed'})
}

$result = [ordered]@{ ok=(-not $script:verificationFailed); checked_at=(Get-Date).ToUniversalTime().ToString("o"); checks=@($checks) }
$result | ConvertTo-Json -Depth 8
if ($script:verificationFailed) { exit 1 }
