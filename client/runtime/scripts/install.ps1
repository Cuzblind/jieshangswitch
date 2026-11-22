# 接上AI · Windows 安装脚本草稿（本地，不部署）
#
# 正式形态：irm https://jieshangai.com/i/{token} | iex
# 管道本身看不到网址。服务端接入前，助手种类和短期凭证从下面几处读取
# （先到先用，空着的才用下一处）：
#   1. 环境变量 RELAY_AGENT、RELAY_TOKEN
#   2. 用 -File 运行时的第一个参数：完整网址，或查询串 agent=claude&token=...
#   3. RELAY_INSTALL_URL
#   4. RELAY_QUERY，其次 QUERY_STRING
# 写入配置的内容默认就是这份短期凭证（可假）。若已设置 RELAY_SECRET，则改写它。
# 网关 /i/{token} 下发时注入 RELAY_CLAIM=1 与 RELAY_SITE_ORIGIN：脚本向
# POST /api/install/claim 领取一次连接凭证，向 /api/install/progress 回报进度，
# 最后用 GET /v1/models 测一次连接。
# 这些值都不会打到屏幕上。
# iex 跑在当前窗口里，所以 Codex 的连接会留在这个窗口；-File 是子进程，关了就没了。
# 设了 RELAY_HOME 时只往那个目录写文件，不动这台电脑真正的用户环境。

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$script:ClaudeInstallUrl = 'https://claude.ai/install.ps1'
$script:CodexInstallUrl = 'https://chatgpt.com/codex/install.ps1'
$script:ProviderId = 'jieshangai'
$script:CodexEnvName = 'JIESHANGAI_CODEX_KEY'
$script:DefaultCodexModel = 'gpt-5.6-sol'
$script:MarkerStart = '# jieshangai-provider-start'
$script:MarkerEnd = '# jieshangai-provider-end'
$script:ModelTag = '# jieshangai-model'
$script:ManifestHeader = '# jieshangai backup manifest v1'

$script:ReportToken = ''
$script:ReportOrigin = ''
$script:RelayPositional = @()
if ($PSCommandPath -and $args.Count -gt 0) {
  $script:RelayPositional = @($args)
}

function Say([string]$Message) {
  Write-Host $Message
}

function Get-ConfRoot {
  if (-not [string]::IsNullOrWhiteSpace($env:RELAY_HOME)) {
    return $env:RELAY_HOME
  }
  if (-not [string]::IsNullOrWhiteSpace($env:USERPROFILE)) {
    return $env:USERPROFILE
  }
  return $HOME
}

function Test-TrialHome {
  return -not [string]::IsNullOrWhiteSpace($env:RELAY_HOME)
}

function Write-EngineCode([string]$Code) {
  $dir = Join-Path (Get-ConfRoot) '.jieshangai'
  if (-not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
  }
  $path = Join-Path $dir 'install-error.log'
  $utf8 = New-Object System.Text.UTF8Encoding $false
  [IO.File]::WriteAllText($path, $Code + "`n", $utf8)
  Send-RelayProgress -Status 'failed' -ErrorCode $Code
}

function Test-ClaimMode {
  return $env:RELAY_CLAIM -eq '1'
}

# Best effort; never fails the install.
function Send-RelayProgress([int]$Beat = -1, [string]$Status = 'running', [string]$ErrorCode = '') {
  if (-not (Test-ClaimMode)) { return }
  if ([string]::IsNullOrWhiteSpace($script:ReportToken) -or [string]::IsNullOrWhiteSpace($script:ReportOrigin)) { return }
  $body = @{ token = $script:ReportToken; status = $Status; error = $ErrorCode }
  if ($Beat -ge 0) { $body.beat = $Beat }
  try {
    Invoke-RestMethod -Method Post -Uri ($script:ReportOrigin + '/api/install/progress') `
      -ContentType 'application/json' -Body ($body | ConvertTo-Json -Compress) -TimeoutSec 10 -UseBasicParsing | Out-Null
  } catch { }
}

function Get-RelayClaim([string]$Origin, [string]$Token) {
  try {
    $resp = Invoke-RestMethod -Method Post -Uri ($Origin + '/api/install/claim') `
      -ContentType 'application/json' -Body (@{ token = $Token } | ConvertTo-Json -Compress) -TimeoutSec 20 -UseBasicParsing
    $key = [string]$resp.data.key
    if ($key -match '^[A-Za-z0-9_-]+$') { return $key }
  } catch { }
  return $null
}

function Test-RelayConnection([string]$Origin, [string]$Secret) {
  try {
    Invoke-RestMethod -Method Get -Uri ($Origin + '/v1/models') `
      -Headers @{ Authorization = ('Bearer ' + $Secret) } -TimeoutSec 20 -UseBasicParsing | Out-Null
    return $true
  } catch {
    return $false
  }
}

function Clear-EngineCode {
  $path = Join-Path (Join-Path (Get-ConfRoot) '.jieshangai') 'install-error.log'
  if (Test-Path -LiteralPath $path) {
    Remove-Item -LiteralPath $path -Force
  }
}

function ConvertFrom-RelayQuery([string]$Query) {
  $map = @{}
  if ([string]::IsNullOrWhiteSpace($Query)) { return $map }
  $q = $Query.Trim().TrimStart('?')
  $hash = $q.IndexOf('#')
  if ($hash -ge 0) { $q = $q.Substring(0, $hash) }
  foreach ($part in ($q -split '&')) {
    if ([string]::IsNullOrEmpty($part)) { continue }
    $i = $part.IndexOf('=')
    if ($i -lt 0) {
      $k = $part
      $v = ''
    } else {
      $k = $part.Substring(0, $i)
      $v = $part.Substring($i + 1)
    }
    $k = [Uri]::UnescapeDataString($k.Replace('+', ' ')).ToLowerInvariant()
    $v = [Uri]::UnescapeDataString($v.Replace('+', ' '))
    $map[$k] = $v
  }
  return $map
}

function Get-PathToken([string]$Raw) {
  if ($Raw -notmatch '^[a-zA-Z][a-zA-Z0-9+.-]*://') { return '' }
  $noHash = $Raw.Split('#')[0]
  $noQuery = $noHash.Split('?')[0]
  if ($noQuery -match '/i/([^/]+)/?$') {
    return [Uri]::UnescapeDataString($Matches[1])
  }
  return ''
}

function Get-QueryFromRaw([string]$Raw) {
  if ([string]::IsNullOrWhiteSpace($Raw)) { return '' }
  if ($Raw -match '^[a-zA-Z][a-zA-Z0-9+.-]*://') {
    $noHash = $Raw.Split('#')[0]
    $i = $noHash.IndexOf('?')
    if ($i -lt 0) { return '' }
    return $noHash.Substring($i + 1)
  }
  if ($Raw.Contains('=')) { return $Raw }
  return ''
}

function Resolve-RelayInput {
  $agent = ''
  $token = ''
  if (-not [string]::IsNullOrWhiteSpace($env:RELAY_AGENT)) {
    $agent = $env:RELAY_AGENT.Trim().ToLowerInvariant()
  }
  if (-not [string]::IsNullOrWhiteSpace($env:RELAY_TOKEN)) {
    $token = $env:RELAY_TOKEN.Trim()
  }
  $sources = New-Object System.Collections.Generic.List[string]
  if ($script:RelayPositional.Count -gt 0 -and -not [string]::IsNullOrWhiteSpace([string]$script:RelayPositional[0])) {
    $sources.Add([string]$script:RelayPositional[0])
  }
  foreach ($name in @('RELAY_INSTALL_URL', 'RELAY_QUERY', 'QUERY_STRING')) {
    $value = [Environment]::GetEnvironmentVariable($name)
    if (-not [string]::IsNullOrWhiteSpace($value)) { $sources.Add($value.Trim()) }
  }
  foreach ($raw in $sources) {
    $q = ConvertFrom-RelayQuery (Get-QueryFromRaw $raw)
    if ([string]::IsNullOrWhiteSpace($agent) -and $q.ContainsKey('agent')) {
      $agent = ([string]$q['agent']).Trim().ToLowerInvariant()
    }
    if ([string]::IsNullOrWhiteSpace($token)) {
      if ($q.ContainsKey('token') -and -not [string]::IsNullOrWhiteSpace([string]$q['token'])) {
        $token = ([string]$q['token']).Trim()
      } else {
        $pathToken = Get-PathToken $raw
        if (-not [string]::IsNullOrWhiteSpace($pathToken)) { $token = $pathToken.Trim() }
      }
    }
  }
  $script:RelayAgent = $agent
  $script:RelayToken = $token
}

function Test-RelayToken([string]$Value) {
  if ([string]::IsNullOrWhiteSpace($Value)) { return $false }
  if ($Value.Length -gt 200) { return $false }
  return $Value -match '^[A-Za-z0-9._~+/=-]+$'
}

function Test-SkipCli {
  $v = $env:RELAY_SKIP_CLI
  if ([string]::IsNullOrWhiteSpace($v)) { return $false }
  return @('1', 'true', 'yes', 'y') -contains $v.Trim().ToLowerInvariant()
}

function Get-SiteRoot {
  $origin = $env:RELAY_SITE_ORIGIN
  if ([string]::IsNullOrWhiteSpace($origin)) { $origin = 'https://jieshangai.com' }
  $origin = $origin.Trim().TrimEnd('/')
  # An HTTP desktop test build opts in only to its exact built-in origin.
  # Other HTTP origins still require loopback.
  $testOrigin = ($origin.StartsWith('http://') -and $origin -ceq $env:RELAY_TEST_ORIGIN)
  if (-not $testOrigin -and $origin -notmatch '^(https://|http://(127\.0\.0\.1|localhost|\[::1\])(:|/|$))') { return $null }
  if ($origin -match '[\s''"\\@]') { return $null }
  if ($origin.EndsWith('/v1')) { $origin = $origin.Substring(0, $origin.Length - 3) }
  return $origin
}

# --- backup manifest (same format/logic as client/core/backup.go and install.sh) ---
# ~/.jieshangai/backup-manifest.tsv: header, then ordinal-sorted
# <relpath with />`t<had_original 0|1>`t<sha256 of .bak|->`t<sha256 of our last write|->
function Get-ManifestPath { return (Join-Path (Join-Path (Get-ConfRoot) '.jieshangai') 'backup-manifest.tsv') }

function Get-FileSha([string]$Path) {
  return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# Compare normalized paths: a root spelled with forward slashes or another case
# must still produce a relative key, or restore cannot match the entry later.
function Get-RelKey([string]$Path) {
  $full = [IO.Path]::GetFullPath($Path)
  $root = [IO.Path]::GetFullPath((Get-ConfRoot)).TrimEnd('\', '/')
  if ($full.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    $full = $full.Substring($root.Length + 1)
  }
  return $full.Replace('\', '/')
}

function Read-Manifest {
  $map = @{}
  $m = Get-ManifestPath
  if (Test-Path -LiteralPath $m) {
    foreach ($line in [IO.File]::ReadAllText($m).Split([char]10)) {
      if ($line -eq '' -or $line.StartsWith('#')) { continue }
      $f = $line.Split([char]9)
      if ($f.Count -eq 4) { $map[$f[0]] = $f }
    }
  }
  return $map
}

function Save-Manifest($Map) {
  $m = Get-ManifestPath
  if ($Map.Count -eq 0) {
    if (Test-Path -LiteralPath $m) { Remove-Item -LiteralPath $m -Force }
    return
  }
  $keys = [string[]]@($Map.Keys)
  [Array]::Sort($keys, [StringComparer]::Ordinal)
  $sb = New-Object System.Text.StringBuilder
  [void]$sb.Append($script:ManifestHeader + "`n")
  foreach ($k in $keys) { [void]$sb.Append((($Map[$k]) -join "`t") + "`n") }
  Write-Utf8File $m $sb.ToString()
}

function Backup-Once([string]$Path) {
  $map = Read-Manifest
  $key = Get-RelKey $Path
  if ($map.ContainsKey($key)) { return }
  $bak = $Path + '.jieshangai.bak'
  $had = '0'; $orig = '-'
  if (Test-Path -LiteralPath $bak -PathType Leaf) {
    $had = '1'; $orig = Get-FileSha $bak
  } elseif (Test-Path -LiteralPath $Path -PathType Leaf) {
    Copy-Item -LiteralPath $Path -Destination $bak
    $had = '1'; $orig = Get-FileSha $bak
  }
  $map[$key] = @($key, $had, $orig, '-')
  Save-Manifest $map
}

function Write-RecordedWrite([string]$Path) {
  $map = Read-Manifest
  $key = Get-RelKey $Path
  if (-not $map.ContainsKey($key)) { return }
  $e = $map[$key]
  $map[$key] = @($key, $e[1], $e[2], (Get-FileSha $Path))
  Save-Manifest $map
}

function Set-NoteProperty($Object, [string]$Name, $Value) {
  if ($Object.PSObject.Properties[$Name]) {
    $Object.$Name = $Value
  } else {
    $Object | Add-Member -NotePropertyName $Name -NotePropertyValue $Value
  }
}

function Write-Utf8File([string]$Path, [string]$Text) {
  $dir = Split-Path -Parent $Path
  if (-not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
  }
  $tmp = $Path + '.tmp'
  $utf8 = New-Object System.Text.UTF8Encoding $false
  [IO.File]::WriteAllText($tmp, $Text, $utf8)
  if (Test-Path -LiteralPath $Path) { Remove-Item -LiteralPath $Path -Force }
  [IO.File]::Move($tmp, $Path)
}

function Write-ClaudeSettings([string]$Path, [string]$Base, [string]$Secret) {
  $dir = Split-Path -Parent $Path
  if (-not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
  }
  $data = New-Object PSObject
  Backup-Once $Path
  if (Test-Path -LiteralPath $Path) {
    $raw = [IO.File]::ReadAllText($Path)
    if (-not [string]::IsNullOrWhiteSpace($raw)) {
      $parsed = $raw | ConvertFrom-Json
      if ($parsed -is [System.Array] -or $null -eq $parsed) {
        throw 'config-write-failed'
      }
      $data = $parsed
    }
  }
  if (-not $data.PSObject.Properties['env'] -or $null -eq $data.env -or $data.env -is [System.Array]) {
    Set-NoteProperty $data 'env' (New-Object PSObject)
  }
  Set-NoteProperty $data.env 'ANTHROPIC_BASE_URL' $Base
  Set-NoteProperty $data.env 'ANTHROPIC_AUTH_TOKEN' $Secret
  if ($null -ne $data.env.PSObject.Properties['ANTHROPIC_API_KEY']) {
    $data.env.PSObject.Properties.Remove('ANTHROPIC_API_KEY')
  }
  Set-NoteProperty $data.env 'CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC' '1'
  Set-NoteProperty $data.env 'DISABLE_TELEMETRY' '1'
  Set-NoteProperty $data.env 'DISABLE_ERROR_REPORTING' '1'
  $json = ConvertTo-Json -InputObject $data -Depth 100
  Write-Utf8File $Path ($json.TrimEnd() + "`n")
  Write-RecordedWrite $Path
}

function Remove-MarkedBlock([string]$Text, [string]$Start, [string]$End) {
  $lines = @()
  if (-not [string]::IsNullOrEmpty($Text)) {
    $lines = $Text -split "`n", -1
  }
  $out = New-Object System.Collections.Generic.List[string]
  $skip = $false
  foreach ($line in $lines) {
    $t = $line.TrimEnd("`r")
    if ($t -eq $Start) { $skip = $true; continue }
    if ($t -eq $End) { $skip = $false; continue }
    if (-not $skip) { $out.Add($t) | Out-Null }
  }
  return ($out -join "`n")
}

function Set-TopLevelToml([string]$Text, [string]$Key, [string]$ValueLiteral) {
  $pattern = '(?m)^' + [regex]::Escape($Key) + '[ \t]*=.*$'
  if ([regex]::IsMatch($Text, $pattern)) {
    return [regex]::Replace($Text, $pattern, ($Key + ' = ' + $ValueLiteral))
  }
  if ([string]::IsNullOrWhiteSpace($Text)) {
    return ($Key + ' = ' + $ValueLiteral)
  }
  return ($Key + ' = ' + $ValueLiteral + "`n" + $Text.TrimEnd("`r"))
}

function Write-CodexConfig([string]$Path, [string]$Base, [string]$Model) {
  $text = ''
  Backup-Once $Path
  if (Test-Path -LiteralPath $Path) {
    $text = [IO.File]::ReadAllText($Path)
  }
  $text = Remove-MarkedBlock $text $script:MarkerStart $script:MarkerEnd
  $text = Set-TopLevelToml $text 'model_provider' '"jieshangai"'
  if (-not [string]::IsNullOrWhiteSpace($Model)) {
    $text = Set-TopLevelToml $text 'model' ('"' + $Model + '" ' + $script:ModelTag)
  } elseif (-not [regex]::IsMatch($text, '(?m)^model[ \t]*=')) {
    $text = Set-TopLevelToml $text 'model' ('"' + $script:DefaultCodexModel + '" ' + $script:ModelTag)
  }
  $text = $text.TrimEnd("`r", "`n")
  $block = @(
    ''
    $script:MarkerStart
    ('[model_providers.' + $script:ProviderId + ']')
    'name = "接上AI"'
    ('base_url = "' + $Base + '"')
    ('env_key = "' + $script:CodexEnvName + '"')
    'wire_api = "responses"'
    $script:MarkerEnd
    ''
  ) -join "`n"
  Write-Utf8File $Path ($text + "`n" + $block)
  Write-RecordedWrite $Path
}

function Write-CodexEnvFile([string]$Path, [string]$Secret) {
  $escaped = $Secret.Replace("'", "'\''")
  $line = 'export ' + $script:CodexEnvName + "='" + $escaped + "'"
  Write-Utf8File $Path ($line + "`n")
}

function Set-CodexProcessEnv([string]$Secret) {
  if (Test-TrialHome) { return }
  try {
    [Environment]::SetEnvironmentVariable($script:CodexEnvName, $Secret, 'User')
  } catch {
    Write-EngineCode 'user-env-failed'
  }
  Set-Item -Path ('Env:' + $script:CodexEnvName) -Value $Secret
}

function Write-Stamp([string]$Assistant, [string]$Cli) {
  $dir = Join-Path (Get-ConfRoot) '.jieshangai'
  if (-not (Test-Path -LiteralPath $dir)) {
    New-Item -ItemType Directory -Path $dir -Force | Out-Null
  }
  $path = Join-Path $dir 'install-stamp.json'
  $json = '{"assistant":"' + $Assistant + '","cli":"' + $Cli + '","connection":"ok"}' + "`n"
  Write-Utf8File $path $json
}

function Update-ToolPath {
  $dirs = @()
  if (-not [string]::IsNullOrWhiteSpace($env:USERPROFILE)) {
    $dirs += (Join-Path $env:USERPROFILE '.local\bin')
  }
  if (-not [string]::IsNullOrWhiteSpace($env:LOCALAPPDATA)) {
    $dirs += (Join-Path $env:LOCALAPPDATA 'Programs\OpenAI\Codex\bin')
  }
  foreach ($dir in $dirs) {
    if ([string]::IsNullOrWhiteSpace($dir)) { continue }
    if (-not (Test-Path -LiteralPath $dir)) { continue }
    $parts = @($env:PATH -split ';')
    if ($parts -notcontains $dir) {
      $env:PATH = $dir + ';' + $env:PATH
    }
  }
}

function Ensure-PersistentToolPath {
  if (Test-TrialHome) { return }
  $dir = Join-Path (Join-Path (Get-ConfRoot) '.jieshangai') 'tools\bin'
  try {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    if ($null -eq $key) { $key = [Microsoft.Win32.Registry]::CurrentUser.CreateSubKey('Environment') }
    $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $parts = @($raw -split ';' | Where-Object { -not [string]::IsNullOrWhiteSpace($_) })
    if (-not ($parts | Where-Object { $_.Trim().TrimEnd('\\') -ieq $dir.TrimEnd('\\') })) {
      $parts += $dir
      $key.SetValue('Path', ($parts -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString)
    }
    $key.Close()
    $type = @"
using System;
using System.Runtime.InteropServices;
public static class JieshangaiEnvBroadcast {
  [DllImport("user32.dll", CharSet=CharSet.Unicode, SetLastError=true)]
  public static extern IntPtr SendMessageTimeout(IntPtr hWnd, uint Msg, UIntPtr wParam, string lParam, uint flags, uint timeout, out UIntPtr result);
}
"@
    Add-Type -TypeDefinition $type -ErrorAction SilentlyContinue | Out-Null
    $result = [UIntPtr]::Zero
    [JieshangaiEnvBroadcast]::SendMessageTimeout([IntPtr]0xffff, 0x1a, [UIntPtr]::Zero, 'Environment', 2, 5000, [ref]$result) | Out-Null
  } catch { Write-EngineCode 'user-path-failed' }
}

function Test-Tool([string]$Name) {
  return [bool](Get-Command $Name -ErrorAction SilentlyContinue)
}

function Install-OfficialCli([string]$Url) {
  $tmp = Join-Path $env:TEMP ('jieshangai-install-' + [guid]::NewGuid().ToString('n') + '.ps1')
  try {
    Invoke-WebRequest -Uri $Url -OutFile $tmp -UseBasicParsing
    $env:CODEX_NON_INTERACTIVE = '1'
    $hostExe = Join-Path $env:SystemRoot 'System32\WindowsPowerShell\v1.0\powershell.exe'
    if (-not (Test-Path -LiteralPath $hostExe)) { $hostExe = 'powershell.exe' }
    & $hostExe -NoProfile -ExecutionPolicy Bypass -File $tmp | Out-Host
    if ($null -ne $LASTEXITCODE -and $LASTEXITCODE -ne 0) { return $false }
    return $true
  } catch {
    return $false
  } finally {
    if (Test-Path -LiteralPath $tmp) { Remove-Item -LiteralPath $tmp -Force }
  }
}

function Show-ClaudeNext([bool]$InstalledNow, [bool]$Found) {
  Say '好了。'
  if ($Found -and -not $InstalledNow) {
    Say '在命令窗口输入 claude ，按回车就能开始。'
  } else {
    Say '如果这个窗口提示找不到 claude，关掉再开一次，然后输入 claude ，按回车。'
  }
}

function Show-CodexNext([bool]$Found) {
  Say '好了。'
  if ($Found) {
    Say '在这个窗口输入 codex ，按回车。'
  } else {
    Say '如果这个窗口提示找不到 codex，关掉再开一次，然后输入 codex ，按回车。'
  }
  Say '如果它问要不要信任这个文件夹，选信任当前项目。'
}

function Invoke-RelayInstall {
  Resolve-RelayInput
  $agent = [string]$script:RelayAgent
  $token = [string]$script:RelayToken

  if ([string]::IsNullOrWhiteSpace($agent) -and [string]::IsNullOrWhiteSpace($token)) {
    Write-EngineCode 'missing-input'
    Say '这行命令不完整，回到网页重新复制一次。'
    return 1
  }
  if ($agent -ne 'claude' -and $agent -ne 'codex') {
    Write-EngineCode 'bad-agent'
    Say '不知道要装哪一个助手，回到网页重新复制一次命令。'
    return 1
  }
  if (-not (Test-RelayToken $token)) {
    Write-EngineCode 'missing-input'
    Say '这行命令不完整，回到网页重新复制一次。'
    return 1
  }

  $secret = $token
  if (-not [string]::IsNullOrWhiteSpace($env:RELAY_SECRET)) {
    $secret = $env:RELAY_SECRET
  }
  if ($secret.IndexOfAny([char[]]@("`n", "`r")) -ge 0) {
    Write-EngineCode 'config-write-failed'
    Say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
    return 1
  }

  $origin = Get-SiteRoot
  if ([string]::IsNullOrWhiteSpace($origin)) {
    Write-EngineCode 'config-write-failed'
    Say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
    return 1
  }
  $script:ReportToken = $token
  $script:ReportOrigin = $origin
  Send-RelayProgress -Beat 1
  if ((Test-ClaimMode) -and [string]::IsNullOrWhiteSpace($env:RELAY_SECRET)) {
    $claimed = Get-RelayClaim $origin $token
    if (-not $claimed) {
      Write-EngineCode 'claim-failed'
      Say '连不上服务器，请检查网络后回到网页重新复制一次命令。'
      return 1
    }
    $secret = $claimed
  }

  $root = Get-ConfRoot
  Send-RelayProgress -Beat 2
  $found = $false
  $installedNow = $false
  $cliState = 'present'
  if ($agent -eq 'claude') {
    Say '正在看看这台电脑上有没有 Claude Code……'
  } else {
    Say '正在看看这台电脑上有没有 Codex……'
  }
  Update-ToolPath
  if (Test-Tool $agent) {
    $found = $true
    Say '已经有了，不用再装。'
    $cliState = 'present'
  } elseif (Test-SkipCli) {
    Say '这台电脑上还没有这个助手，这次没有去官网下载。'
    $cliState = 'skipped'
  } else {
    Say '没找到，我去官网帮你装，可能要等一会儿。'
    $url = $script:ClaudeInstallUrl
    if ($agent -eq 'codex') { $url = $script:CodexInstallUrl }
    $installerOk = Install-OfficialCli $url
    Update-ToolPath
    if (Test-Tool $agent) {
      $found = $true
      $installedNow = $true
      $cliState = 'installed'
      Say '装好了。'
    } elseif ($installerOk) {
      $installedNow = $true
      $cliState = 'installed'
      Say '装好了。'
    } else {
      $cliState = 'failed'
    }
  }

  if ($cliState -ne 'failed' -and $cliState -ne 'skipped') { Ensure-PersistentToolPath }

  Send-RelayProgress -Beat 3
  Say '正在把连接写好……'
  try {
    if ($agent -eq 'claude') {
      Write-ClaudeSettings (Join-Path (Join-Path $root '.claude') 'settings.json') $origin $secret
    } else {
      $model = ''
      if (-not [string]::IsNullOrWhiteSpace($env:RELAY_CODEX_MODEL)) {
        $model = $env:RELAY_CODEX_MODEL.Trim()
        if ($model -notmatch '^[A-Za-z0-9._:-]+$') {
          throw 'config-write-failed'
        }
      }
      Write-CodexConfig (Join-Path (Join-Path $root '.codex') 'config.toml') ($origin + '/v1') $model
      Write-CodexEnvFile (Join-Path (Join-Path $root '.jieshangai') 'codex.env') $secret
      Set-CodexProcessEnv $secret
    }
  } catch {
    Write-EngineCode 'config-write-failed'
    Say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
    return 1
  }

  if ($cliState -eq 'failed') {
    Write-EngineCode 'cli-install-failed'
    Say '连接已经写好了，但助手没装上。回到网页看看下一步，或打开帮助页联系我们。'
    return 1
  }

  if (Test-ClaimMode) {
    Send-RelayProgress -Beat 4
    Say '正在测试连接……'
    if (-not (Test-RelayConnection $origin $secret)) {
      Write-EngineCode 'connection-test-failed'
      Say '连接没测通。检查网络后回到网页重新复制一次命令，或打开帮助页联系我们。'
      return 1
    }
  }

  Write-Stamp $agent $cliState
  Clear-EngineCode
  Send-RelayProgress -Beat 5 -Status 'done'
  Say '写好了。'
  if ($cliState -eq 'skipped') {
    if ($agent -eq 'claude') {
      Say '等助手装上之后，在命令窗口输入 claude ，按回车。'
    } else {
      Say '等助手装上之后，在这个窗口输入 codex ，按回车。'
      Say '如果它问要不要信任这个文件夹，选信任当前项目。'
    }
    return 0
  }
  if ($agent -eq 'claude') {
    Show-ClaudeNext $installedNow $found
  } else {
    Show-CodexNext $found
  }
  return 0
}

$relayExit = 1
try {
  $relayExit = Invoke-RelayInstall
} catch {
  Write-EngineCode 'unexpected'
  Say '写连接的时候出错了。回到网页看看下一步，或打开帮助页联系我们。'
  $relayExit = 1
}

if ($PSCommandPath) {
  exit $relayExit
}
