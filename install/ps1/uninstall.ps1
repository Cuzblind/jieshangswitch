# 接上AI · 清掉本站写进这台电脑的连接（不卸载官方 CLI）
# 有备份且指纹对得上：换回装之前的原样；否则只去掉我方条目。
# 与 client/core（Restore）、uninstall.sh 同一逻辑、同一份 backup-manifest.tsv。
# 写入后用户又改过：打印一行并要求确认（RELAY_CONFIRM=1 或在窗口输入 y）。
#
# 正式形态：irm https://jieshangai.com/u | iex
# 网关 /u 可注入 RELAY_SITE_ORIGIN。本脚本不调用撤销接口（本机通常没有登录态）。
# 设了 RELAY_HOME 时只动那个目录，不清用户环境变量。屏幕上不打印连接内容。

$ErrorActionPreference = 'Stop'
$ProgressPreference = 'SilentlyContinue'

$script:CodexEnvName = 'JIESHANGAI_CODEX_KEY'
$script:MarkerStart = '# jieshangai-provider-start'
$script:MarkerEnd = '# jieshangai-provider-end'
$script:RcStart = '# jieshangai-connection-start'
$script:RcEnd = '# jieshangai-connection-end'
$script:NoBackupMsg = '已去掉我们加的设置。如果你以前自己选过模型，需要重新选一次。'
$script:RestoredMsg = '已经恢复好了。'
$script:EditedPrompt = '你后来自己改过的地方，也会一起恢复。要恢复请输入 y 后按回车：'
$script:DeclinedMsg = '没有动任何文件。'

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

# -split on a newline pattern is a no-op in this PowerShell, so use String.Split.
function Split-TextLines([string]$Text) {
  if ([string]::IsNullOrEmpty($Text)) { return @() }
  $norm = $Text.Replace("`r`n", "`n").Replace("`r", "`n")
  return @($norm.Split([char]10))
}

function Remove-MarkedBlock([string]$Text, [string]$Start, [string]$End) {
  $lines = @(Split-TextLines $Text)
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

function Remove-ProviderLine([string]$Text) {
  $lines = @(Split-TextLines $Text)
  $out = New-Object System.Collections.Generic.List[string]
  foreach ($line in $lines) {
    $t = $line.TrimEnd("`r")
    if ($t -match '^\s*model_provider\s*=\s*"jieshangai"\s*$') { continue }
    if ($t -match '^model[ \t]*=.*# jieshangai-model$') { continue }
    $out.Add($t) | Out-Null
  }
  return ($out -join "`n")
}

function Update-MarkedFile([string]$Path, [string]$Start, [string]$End, [bool]$DropProvider) {
  if (-not (Test-Path -LiteralPath $Path)) { return }
  $text = [IO.File]::ReadAllText($Path)
  # only rewrite files that carry our marks
  $marked = $false
  foreach ($l in @(Split-TextLines $Text)) {
    $t = $l.TrimEnd("`r")
    if ($t -eq $Start) { $marked = $true; break }
    if ($DropProvider -and ($t -match '^\s*model_provider\s*=\s*"jieshangai"\s*$' -or $t -match '^model[ \t]*=.*# jieshangai-model$')) { $marked = $true; break }
  }
  if (-not $marked) { return }
  $text = Remove-MarkedBlock $text $Start $End
  if ($DropProvider) { $text = Remove-ProviderLine $text }
  $text = $text.TrimEnd("`n") + "`n"  # same as uninstall.sh: trailing blank lines dropped
  Write-Utf8File $Path $text
}

function Remove-ClaudeKeys([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path)) { return }
  $raw = [IO.File]::ReadAllText($Path)
  if ([string]::IsNullOrWhiteSpace($raw)) { return }
  $data = $raw | ConvertFrom-Json
  if ($null -eq $data -or $data -is [System.Array]) { return }
  $drop = @('ANTHROPIC_BASE_URL', 'ANTHROPIC_AUTH_TOKEN', 'ANTHROPIC_API_KEY')
  if ($data.PSObject.Properties['env'] -and $null -ne $data.env -and -not ($data.env -is [System.Array])) {
    foreach ($name in $drop) {
      $prop = $data.env.PSObject.Properties[$name]
      if ($null -ne $prop) { $data.env.PSObject.Properties.Remove($name) }
    }
    if (@($data.env.PSObject.Properties).Count -eq 0) {
      $data.PSObject.Properties.Remove('env')
    }
  }
  $json = ConvertTo-Json -InputObject $data -Depth 100
  Write-Utf8File $Path ($json.TrimEnd() + "`n")
}

function Clear-CodexEnv {
  if (Test-TrialHome) { return }
  $name = $script:CodexEnvName
  try {
    [Environment]::SetEnvironmentVariable($name, $null, 'User')
  } catch { }
  $item = 'Env:' + $name
  if (Test-Path -LiteralPath $item) {
    Remove-Item -LiteralPath $item -ErrorAction SilentlyContinue
  }
}

function Remove-PersistentToolPath {
  if (Test-TrialHome) { return }
  $dir = Join-Path (Join-Path (Get-ConfRoot) '.jieshangai') 'tools\bin'
  try {
    $key = [Microsoft.Win32.Registry]::CurrentUser.OpenSubKey('Environment', $true)
    if ($null -eq $key) { return }
    $raw = [string]$key.GetValue('Path', '', [Microsoft.Win32.RegistryValueOptions]::DoNotExpandEnvironmentNames)
    $parts = @($raw -split ';' | Where-Object { $_.Trim().TrimEnd('\\') -ine $dir.TrimEnd('\\') -and -not [string]::IsNullOrWhiteSpace($_) })
    if ($parts.Count -eq 0 -and $raw -ne '') { $key.SetValue('Path', '', [Microsoft.Win32.RegistryValueKind]::ExpandString) }
    elseif ($parts.Count -lt @($raw -split ';').Count) { $key.SetValue('Path', ($parts -join ';'), [Microsoft.Win32.RegistryValueKind]::ExpandString) }
    $key.Close()
  } catch { }
}

function Remove-SiteFiles([string]$Root) {
  $dir = Join-Path $Root '.jieshangai'
  foreach ($name in @('codex.env', 'install-stamp.json', 'install-error.log')) {
    $path = Join-Path $dir $name
    if (Test-Path -LiteralPath $path) {
      Remove-Item -LiteralPath $path -Force
    }
  }
}

function Remove-DirIfEmpty([string]$Dir) {
  if (Test-Path -LiteralPath $Dir) {
    $left = Get-ChildItem -LiteralPath $Dir -Force -ErrorAction SilentlyContinue | Select-Object -First 1
    if ($null -eq $left) {
      Remove-Item -LiteralPath $Dir -Force
    }
  }
}

function Get-FileSha([string]$Path) {
  return (Get-FileHash -LiteralPath $Path -Algorithm SHA256).Hash.ToLowerInvariant()
}

# Older installs wrote absolute keys when the root was spelled unusually; read
# them as relative so those connections can still be restored.
function ConvertTo-RelKey([string]$Key) {
  if (-not [IO.Path]::IsPathRooted($Key)) { return $Key }
  $full = [IO.Path]::GetFullPath($Key)
  $root = [IO.Path]::GetFullPath((Get-ConfRoot)).TrimEnd('\', '/')
  if ($full.StartsWith($root + [IO.Path]::DirectorySeparatorChar, [StringComparison]::OrdinalIgnoreCase)) {
    return $full.Substring($root.Length + 1).Replace('\', '/')
  }
  return $null
}

# Manifest entries (string[4]) sorted ordinally by path.
function Read-ManifestEntries([string]$Path) {
  $list = New-Object System.Collections.Generic.List[object]
  if (-not (Test-Path -LiteralPath $Path)) { return ,$list }
  foreach ($line in [IO.File]::ReadAllText($Path).Split([char]10)) {
    if ($line -eq '' -or $line.StartsWith('#')) { continue }
    $f = $line.Split([char]9)
    if ($f.Count -eq 4) {
      $f[0] = ConvertTo-RelKey $f[0]
      if ($null -ne $f[0]) { $list.Add($f) | Out-Null }
    }
  }
  # Sort in place: piping string[] rows through Sort-Object makes Windows
  # PowerShell 5.1 unroll each row into its four fields, so restore used to
  # iterate single characters instead of entries.
  $arr = $list.ToArray()
  [Array]::Sort($arr, [Comparison[object]]{ param($a, $b) [string]::Compare($a[0], $b[0], $false) })
  $sorted = New-Object System.Collections.Generic.List[object]
  foreach ($e in $arr) { $sorted.Add($e) | Out-Null }
  return ,$sorted
}

function Join-Rel([string]$Root, [string]$Rel) {
  return (Join-Path $Root ($Rel.Replace('/', [IO.Path]::DirectorySeparatorChar)))
}

function Test-Confirmed {
  if (@('1', 'true', 'yes', 'y') -contains "$($env:RELAY_CONFIRM)".Trim().ToLowerInvariant()) { return $true }
  Write-Host -NoNewline $script:EditedPrompt
  try {
    $ans = [Console]::ReadLine()
  } catch {
    Write-Host ''
    return $false
  }
  if ($null -eq $ans) { Write-Host ''; return $false }
  return @('y', 'yes') -contains $ans.Trim().ToLowerInvariant()
}

function Remove-IfLeftEmpty([string]$Path) {
  if (-not (Test-Path -LiteralPath $Path -PathType Leaf)) { return }
  $t = [IO.File]::ReadAllText($Path)
  # Windows PowerShell 5.1 renders an empty object as a multi-line "{ }";
  # fold whitespace so both layouts count as empty.
  if ($t -eq '' -or $t -eq "`n" -or (($t -replace '\s', '') -eq '{}')) { Remove-Item -LiteralPath $Path -Force }
}

function Invoke-RelayUninstall {
  $root = (Get-ConfRoot).TrimEnd('\', '/')
  $site = Join-Path $root '.jieshangai'
  $manifest = Join-Path $site 'backup-manifest.tsv'
  $entries = Read-ManifestEntries $manifest
  $edited = $false
  foreach ($e in $entries) {
    if ($e[3] -eq '-') { continue }
    $p = Join-Rel $root $e[0]
    if (-not (Test-Path -LiteralPath $p -PathType Leaf) -or (Get-FileSha $p) -ne $e[3]) { $edited = $true }
  }
  if ($edited -and -not (Test-Confirmed)) {
    Say $script:DeclinedMsg
    return 1
  }
  Say '正在清掉本站写上的连接……'
  $claude = Join-Path (Join-Path $root '.claude') 'settings.json'
  $claudeTracked = $entries.Count -eq 0
  foreach ($e in $entries) { if ($e[0] -eq '.claude/settings.json') { $claudeTracked = $true } }
  if ($claudeTracked) {
    try {
      Remove-ClaudeKeys $claude
    } catch {
      Say '配置文件读不了，请稍后再试。'
      return 1
    }
  }
  Update-MarkedFile (Join-Path (Join-Path $root '.codex') 'config.toml') $script:MarkerStart $script:MarkerEnd $true
  Update-MarkedFile (Join-Path $root '.zshrc') $script:RcStart $script:RcEnd $false
  Update-MarkedFile (Join-Path $root '.bashrc') $script:RcStart $script:RcEnd $false
  Remove-SiteFiles $root
  Clear-CodexEnv
  Remove-PersistentToolPath
  $used = $false
  foreach ($e in $entries) {
    $p = Join-Rel $root $e[0]
    $bak = $p + '.jieshangai.bak'
    if ($e[1] -eq '1' -and (Test-Path -LiteralPath $bak -PathType Leaf) -and (Get-FileSha $bak) -eq $e[2]) {
      Copy-Item -LiteralPath $bak -Destination $p -Force
      Remove-Item -LiteralPath $bak -Force
      $used = $true
    } elseif ($e[1] -ne '1') {
      Remove-IfLeftEmpty $p
    }
  }
  if (Test-Path -LiteralPath $manifest) { Remove-Item -LiteralPath $manifest -Force }
  Remove-DirIfEmpty $site
  if ($used) { Say $script:RestoredMsg } else { Say $script:NoBackupMsg }
  return 0
}

$relayExit = 1
try {
  $relayExit = Invoke-RelayUninstall
} catch {
  Say '清连接的时候出错了，请稍后再试。'
  $relayExit = 1
}

if ($PSCommandPath) {
  exit $relayExit
}
