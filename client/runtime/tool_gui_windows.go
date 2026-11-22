//go:build windows

package clientruntime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"

	"golang.org/x/sys/windows"
)

type winInstallResult struct {
	Output string
	Err    error
	Code   string
}

func (m *toolManager) installAppxCurrentUser(ctx context.Context, packagePath string) (string, error) {
	dir, err := os.MkdirTemp(m.root, "appx-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	scriptPath := filepath.Join(dir, "install-appx.ps1")
	ps := "$ErrorActionPreference='Stop'; Add-AppxPackage -Path $env:JIESHANGAI_MSIX -ErrorAction Stop\n"
	if err = writePrivateFile(scriptPath, []byte(ps)); err != nil {
		return "", err
	}
	env := append(m.environment(), "JIESHANGAI_MSIX="+packagePath, "JIESHANGAI_INSTALLER_PS1="+scriptPath)
	return runToolCommand(ctx, powershell(), []string{"-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", scriptPath}, env, true)
}

// Long enough for a healthy Store download; the mirror then takes over within
// minutes where the Store CDN stalls.
const storeInstallTimeout = 6 * time.Minute

func currentUserSID() (string, error) {
	user, err := windows.GetCurrentProcessToken().GetTokenUser()
	if err != nil {
		return "", err
	}
	return user.User.Sid.String(), nil
}

func psLiteral(s string) string { return "'" + strings.ReplaceAll(s, "'", "''") + "'" }

// elevatedAppxScript installs an MSIX under the admin token. Everything travels
// inside the script text: process environment does not cross the ShellExecute
// "runas" boundary, and a script file in a user-writable directory could be
// swapped before the prompt is approved. The start marker distinguishes "never
// ran" from "install failed"; result writes are guarded so a broken diagnostic
// channel can never mask the real error.
func elevatedAppxScript(msix, result, sid string) string {
	return "$ErrorActionPreference='Stop'\n" +
		"$Msix=" + psLiteral(msix) + "\n" +
		"$Result=" + psLiteral(result) + "\n" +
		"if ([Security.Principal.WindowsIdentity]::GetCurrent().User.Value -ne " + psLiteral(sid) + ") { exit " + strconv.Itoa(elevatedWrongAccountExit) + " }\n" +
		"try { Add-Content -LiteralPath $Result -Value 'JIESHANGAI_APPX_START' } catch {}\n" +
		"try {\n" +
		"  Add-AppxPackage -Path $Msix -ErrorAction Stop\n" +
		"} catch {\n" +
		"  $msg = $_.Exception.Message\n" +
		"  if ($_.Exception.HResult) { $msg += (' (0x{0:X8})' -f $_.Exception.HResult) }\n" +
		"  try { Add-Content -LiteralPath $Result -Value ('JIESHANGAI_APPX_ERROR: ' + $msg) } catch {}\n" +
		"  exit 3\n" +
		"}\n"
}

// elevatedAppxPayload encodes the script for powershell -EncodedCommand.
func elevatedAppxPayload(msix, result, sid string) string {
	units := utf16.Encode([]rune(elevatedAppxScript(msix, result, sid)))
	raw := make([]byte, 0, len(units)*2)
	for _, u := range units {
		raw = append(raw, byte(u), byte(u>>8))
	}
	return base64.StdEncoding.EncodeToString(raw)
}

func elevationLaunchCommand() string {
	return "$a=@('-NoProfile','-NonInteractive','-WindowStyle','Hidden','-ExecutionPolicy','Bypass','-EncodedCommand',$env:JIESHANGAI_APPX_PAYLOAD);" +
		"$p=Start-Process -FilePath $env:SystemRoot\\System32\\WindowsPowerShell\\v1.0\\powershell.exe -Verb RunAs -WindowStyle Hidden -Wait -PassThru -ArgumentList $a;" +
		"if(Test-Path $env:JIESHANGAI_APPX_RESULT){Get-Content $env:JIESHANGAI_APPX_RESULT | Write-Output};" +
		"if(-not $p){Write-Output 'JIESHANGAI_ELEVATION_FAILED uac-dismissed'; exit 1};" +
		"if($p.ExitCode -ne 0){Write-Output ('JIESHANGAI_ELEVATION_FAILED exit='+$p.ExitCode); exit $p.ExitCode}"
}

func (m *toolManager) installAppxElevated(ctx context.Context, packagePath, agent string, progress func(string)) (string, error) {
	// The elevated step is a fixed Add-AppxPackage operation. It receives no
	// user command, shell fragment, token, or provider configuration.
	dir, err := os.MkdirTemp(m.root, "elevated-")
	if err != nil {
		return "", err
	}
	defer os.RemoveAll(dir)
	resultPath := filepath.Join(dir, "appx-result.log")
	// Start-Process -Verb RunAs goes through ShellExecute, which cannot
	// redirect the child's streams; the elevated script therefore reports
	// through a result file so the real Add-AppxPackage error (certificate
	// trust, policy, …) reaches the diagnostic stream instead of a bare
	// exit code.
	sid, err := currentUserSID()
	if err != nil {
		return "", err
	}
	// 0x80073D28 is by design: both vendor MSIX packages carry a packaged
	// background service (Claude Cowork VM / ChatGPT desktop service) that
	// Windows registers only under an admin token — per-user Add-AppxPackage
	// can never succeed. The UAC prompt names PowerShell, our elevation host.
	progress("桌面版包含需要管理员注册的系统服务；Windows 将弹出确认框（显示为 Windows PowerShell），请点击“是”。")
	started := time.Now()
	env := append(m.environment(), "JIESHANGAI_APPX_PAYLOAD="+elevatedAppxPayload(packagePath, resultPath, sid), "JIESHANGAI_APPX_RESULT="+resultPath)
	command := elevationLaunchCommand()
	out, err := runToolCommand(ctx, powershell(), []string{"-NoProfile", "-NonInteractive", "-Command", command}, env, true)
	if detail, e := os.ReadFile(resultPath); e == nil && len(detail) > 0 {
		out += "\n" + string(detail)
	}
	// Keep the exit code, elapsed time, and timeout state in the existing private
	// diagnostic stream so a dismissed or blocked UAC prompt is actionable.
	timedOut := errors.Is(ctx.Err(), context.DeadlineExceeded)
	m.diagnostic(agent, "gui", "elevation", "appx", fmt.Sprintf("elapsed_ms=%d timeout=%t\n%s", time.Since(started).Milliseconds(), timedOut, out), err)
	return out, err
}

func (m *toolManager) storeID(agent string) string {
	if agent == "codex" {
		return "9PLM9XGG6VKS"
	}
	return "9P6K58THS811"
}

func (m *toolManager) rgAdguardGUI(ctx context.Context, agent string) (guiRelease, error) {
	form := url.Values{
		"type": {"ProductId"},
		"url":  {m.storeID(agent)},
		"ring": {"Retail"},
		"lang": {""},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://store.rg-adguard.net/api/GetFiles", strings.NewReader(form.Encode()))
	if err != nil {
		return guiRelease{}, err
	}
	// Match the browser request shape expected by the public converter. The
	// returned package URL is still restricted to Microsoft's CDN below.
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 Chrome/131.0.0.0 Safari/537.36")
	req.Header.Set("Accept", "text/html, */*; q=0.01")
	req.Header.Set("Origin", "https://store.rg-adguard.net")
	req.Header.Set("Referer", "https://store.rg-adguard.net/")
	req.Header.Set("X-Requested-With", "XMLHttpRequest")
	res, err := m.download.client.Do(req)
	if err != nil {
		return guiRelease{}, fmt.Errorf("微软商店直连源暂不可达：%w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return guiRelease{}, fmt.Errorf("微软商店直连源返回 HTTP %d", res.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	if err != nil {
		return guiRelease{}, err
	}
	return parseStoreMirrorMSIX(body, m.arch)
}

func (m *toolManager) installStore(ctx context.Context, agent string, progress func(string)) winInstallResult {
	winget, err := exec.LookPath("winget.exe")
	if err != nil {
		return winInstallResult{Code: "unavailable", Err: err}
	}
	progress("正在检查微软商店安装通道……")
	probeCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	probe, probeErr := runToolCommand(probeCtx, winget, wingetStoreProbeArgs(), m.environment(), true)
	cancel()
	if probeErr != nil || !strings.Contains(strings.ToLower(probe), "msstore") {
		return winInstallResult{Output: probe, Code: "source-unavailable", Err: probeErr}
	}
	progress("正在通过微软商店安装官方桌面版……")
	installCtx, cancel := context.WithTimeout(ctx, storeInstallTimeout)
	out, err := runToolCommand(installCtx, winget, []string{"install", "--id", m.storeID(agent), "--source", "msstore", "--exact", "--silent", "--disable-interactivity", "--accept-source-agreements", "--accept-package-agreements", "--no-upgrade"}, m.environment(), true)
	cancel()
	return winInstallResult{Output: out, Err: err}
}

func inspectGUIScript(names []string) string {
	quoted := "'" + strings.Join(names, "','") + "'"
	return "$ErrorActionPreference='Stop';$p=@(foreach($n in @(" + quoted + ")){Get-AppxPackage -Name $n -ErrorAction SilentlyContinue}) | Sort-Object Version -Descending | Select-Object -First 1;if($p){$m=Get-AppxPackageManifest $p;$a=@($m.Package.Applications.Application | Where-Object {$_.Executable -like '*.exe'})[0];@{path=(Join-Path $p.InstallLocation $a.Executable);version=$p.Version.ToString();family=$p.PackageFamilyName;appId=$a.Id}|ConvertTo-Json -Compress}"
}

func inspectGUI(ctx context.Context, agent string) (guiInfo, error) {
	names := []string{"AnthropicPBC.Claude", "Claude"}
	if agent == "codex" {
		names = []string{"OpenAI.Codex"}
	}
	script := inspectGUIScript(names)
	out, e := runToolCommand(ctx, powershell(), []string{"-NoProfile", "-NonInteractive", "-Command", script}, cmdEnv(), false)
	if e != nil {
		return guiInfo{}, e
	}
	var info guiInfo
	if strings.TrimSpace(out) != "" {
		if e = json.Unmarshal([]byte(strings.TrimSpace(out)), &info); e != nil {
			return info, e
		}
	}
	if info.Path == "" && agent == "claude" {
		for _, p := range []string{filepath.Join(os.Getenv("LOCALAPPDATA"), "AnthropicClaude", "claude.exe"), filepath.Join(os.Getenv("LOCALAPPDATA"), "Claude", "claude.exe")} {
			if st, e := os.Stat(p); e == nil && !st.IsDir() {
				info.Path = p
				break
			}
		}
	}
	return info, nil
}
func (m *toolManager) latestGUI(ctx context.Context, agent string) (guiRelease, error) {
	if agent == "claude" {
		// Official Claude Windows MSIX; release metadata is returned by the
		// vendor endpoint and does not require GitHub or winget catalogs.
		arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[m.arch]
		if arch == "" {
			return guiRelease{}, errInstallDependency
		}
		return guiRelease{URL: "https://claude.ai/api/desktop/win32/" + arch + "/msix/latest/redirect", Format: "msix"}, nil
	}
	if agent == "codex" {
		arch := map[string]string{"amd64": "x64", "arm64": "arm64"}[m.arch]
		if arch == "" {
			return guiRelease{}, errInstallDependency
		}
		return guiRelease{URL: "https://persistent.oaistatic.com/codex-app-prod/ChatGPT-" + arch + ".msix", Format: "msix"}, nil
	}
	return guiRelease{}, errors.New("暂时无法获取官方桌面安装清单")
}

// latestGUIVersion reads the version from the package itself: neither the mirror
// nor the vendor URLs carry one, and an empty result silently disabled updates.
func (m *toolManager) latestGUIVersion(ctx context.Context, agent string) (string, error) {
	var sources []string
	if r, err := mirrorGUIRelease(m.goos, m.arch, agent); err == nil {
		sources = append(sources, r.URL)
	}
	if r, err := m.latestGUI(ctx, agent); err == nil {
		sources = append(sources, r.URL)
	}
	return m.download.firstRemoteMSIXVersion(ctx, sources, agent, m.arch)
}
func (m *toolManager) installGUIPlatform(ctx context.Context, agent string, update bool, progress func(string)) error {
	file, r, offlineErr := offlineGUI(agent)
	if offlineErr != nil && !os.IsNotExist(offlineErr) {
		return offlineErr
	}
	// The Store channel only performs fresh installs (--no-upgrade); during an
	// update its "already installed" result would be misread as a Store outage
	// and poison the 24h fallback marker, so updates start at the package
	// sources below.
	if file == "" && !update && !m.storeUnavailableFor(agent) {
		store := m.installStore(ctx, agent, progress)
		m.diagnostic(agent, "gui", "msstore", "msstore", store.Output, store.Err)
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if store.Err == nil {
			if info, err := inspectGUI(ctx, agent); err == nil && info.Path != "" {
				m.clearStoreUnavailable(agent)
				return nil
			}
		}
		if store.Code == "unavailable" || store.Code == "source-unavailable" || store.Err != nil {
			if ctx.Err() == nil {
				m.markStoreUnavailable(agent)
			}
			progress("微软商店暂不可用，切换官方完整安装包……")
		}
	} else if file == "" {
		if update {
			progress("正在通过完整安装包更新桌面版……")
		} else {
			progress("微软商店上次未完成，直接尝试官方完整安装包……")
		}
	}
	var e error
	var lastErr error
	sourceLabel := "offline"
	if file == "" {
		// Mainland-China first: claude.ai geo-blocks CN addresses and the vendor
		// CDN crawls from CN links, while the mirror serves the official package from a domestic node at multiples of the speed.
		progress("正在从国内镜像获取官方桌面版……")
		if rel, mirrorErr := mirrorGUIRelease(m.goos, m.arch, agent); mirrorErr == nil {
			r = rel
			sourceLabel = "mirror"
			file, lastErr = m.downloadGUIRelease(ctx, agent, r, sourceLabel, progress)
			if lastErr != nil {
				m.diagnostic(agent, "gui", "download", "agentsmirror", "", lastErr)
				file = ""
			}
		} else {
			lastErr = transferFailure("metadata", "agentsmirror", 0, mirrorErr)
			m.diagnostic(agent, "gui", "metadata", "agentsmirror", "", mirrorErr)
		}
	}
	if file == "" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		progress("正在获取官方桌面版完整安装包……")
		r, e = m.latestGUI(ctx, agent)
		if e == nil {
			sourceLabel = "official"
			file, lastErr = m.downloadGUIRelease(ctx, agent, r, sourceLabel, progress)
			if lastErr != nil {
				file = ""
			}
		} else {
			lastErr = transferFailure("metadata", r.URL, 0, e)
		}
	}
	if file == "" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		progress("官方地址不可用，正在从微软商店直连源获取 " + agentName(agent) + "……")
		mirror, mirrorErr := m.rgAdguardGUI(ctx, agent)
		m.diagnostic(agent, "gui", "metadata", "rg-adguard", "", mirrorErr)
		if mirrorErr == nil {
			r = mirror
			sourceLabel = "rg-adguard"
			file, lastErr = m.downloadGUIRelease(ctx, agent, r, sourceLabel, progress)
			if lastErr != nil {
				file = ""
			}
		} else {
			lastErr = transferFailure("metadata", "https://store.rg-adguard.net/api/GetFiles", 0, mirrorErr)
		}
	}
	if file == "" {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if lastErr != nil {
			return lastErr
		}
		return errors.New("没有找到可用的官方桌面安装包")
	}
	// Add-AppxPackage validates the package's vendor signature and installs only
	// for this user. Try that path first; elevation is requested only when
	// Windows explicitly reports an access/elevation requirement.
	dir, e := os.MkdirTemp(m.root, "msix-")
	if e != nil {
		return e
	}
	defer os.RemoveAll(dir)
	p := filepath.Join(dir, agent+".msix")
	if e = os.Link(file, p); e != nil {
		if e = copyToolFile(file, p, 0600); e != nil {
			return e
		}
	}
	// A Store job abandoned at its timeout may have finished during the download.
	if !update {
		if info, err := inspectGUI(ctx, agent); err == nil && info.Path != "" {
			return nil
		}
	}
	progress("正在为当前用户安装桌面版……")
	out, e := m.installAppxCurrentUser(ctx, p)
	if e != nil && appxNeedsElevation(out) {
		out += "\n"
		elevatedOut, elevatedErr := m.installAppxElevated(ctx, p, agent, progress)
		out += elevatedOut
		e = elevatedErr
	}
	if e != nil {
		logOut, _ := runToolCommand(context.Background(), powershell(), []string{"-NoProfile", "-NonInteractive", "-Command", "Get-AppxLog | Select-Object -First 15 | Format-List | Out-String"}, m.environment(), false)
		out += "\n" + logOut
		return processFailure("install", sourceLabel, out, e)
	}
	return nil
}

func (m *toolManager) downloadGUIRelease(ctx context.Context, agent string, r guiRelease, source string, progress func(string)) (string, error) {
	file, err := m.download.file(ctx, r.URL, r.SHA256, progress)
	if err != nil && r.FallbackURL != "" {
		progress("HTTPS 下载暂不可达，切换微软 CDN 兼容通道……")
		fallback := &toolDownloader{
			client: m.download.client,
			cache:  m.download.cache,
			allow:  allowedMicrosoftCDNHTTP,
			slots:  m.download.slots,
		}
		file, err = fallback.file(ctx, r.FallbackURL, r.SHA256, progress)
	}
	if err != nil {
		raw := r.URL
		if r.FallbackURL != "" {
			raw = r.FallbackURL
		}
		return "", transferFailure("download", raw, 0, err)
	}
	if err = verifyFileSHA1(file, r.SHA1); err != nil {
		return "", processFailure("verify", source, "", err)
	}
	if err = validateMSIX(file, agent, m.arch); err != nil {
		return "", processFailure("verify", source, "", err)
	}
	return file, nil
}

func appxNeedsElevation(output string) bool {
	lower := strings.ToLower(output)
	return strings.Contains(lower, "requires elevation") ||
		strings.Contains(lower, "requires administrator privileges") ||
		strings.Contains(lower, "0x80073d28")
}
