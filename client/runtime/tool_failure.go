package clientruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// InstallFailure crosses the desktop boundary; Cause and raw process output never do.
type InstallFailure struct {
	Code       string `json:"code"`
	Stage      string `json:"stage"`
	Source     string `json:"source,omitempty"`
	NativeCode string `json:"nativeCode,omitempty"`
	Message    string `json:"message"`
	Detail     string `json:"detail,omitempty"`
	Retryable  bool   `json:"retryable"`
	Cause      error  `json:"-"`
}

func (e *InstallFailure) Error() string { return e.Message }
func (e *InstallFailure) Unwrap() error { return e.Cause }
func installFailure(code, stage, source, message string, cause error) *InstallFailure {
	return &InstallFailure{Code: code, Stage: stage, Source: source, Message: message, Retryable: code == "network" || code == "dependency" || code == "busy" || code == "npm", Cause: cause}
}

var nativeCodePattern = regexp.MustCompile(`(?i)0x[0-9a-f]{8}`)
var appxErrorMarker = regexp.MustCompile(`(?i)jieshangai_appx_error`)

// elevatedWrongAccountExit means UAC was satisfied by a different account.
const elevatedWrongAccountExit = 5

func processFailure(stage, source, output string, err error) *InstallFailure {
	native := strings.ToUpper(nativeCodePattern.FindString(output))
	if native == "" {
		var exit *exec.ExitError
		if errors.As(err, &exit) {
			native = fmt.Sprintf("0x%08X", uint32(exit.ExitCode()))
		}
	}
	// A native code after the JIESHANGAI_APPX_ERROR marker is the elevated
	// Add-AppxPackage's real result; anything before it belongs to the
	// per-user attempt that elevation already superseded. Field (Win10
	// beta.5): a stale per-user 0x80073D28 masked the elevated disk-full
	// 0x80073CF4/0x80070070, so the UI demanded admin authorization while the
	// actual problem was a full system disk.
	elevatedCode := ""
	if loc := appxErrorMarker.FindStringIndex(output); loc != nil {
		if after := nativeCodePattern.FindString(output[loc[0]:]); after != "" {
			elevatedCode = strings.ToUpper(after)
			native = elevatedCode
		}
	}
	code, message := "installation", "安装未完成，请复制安装诊断查看具体原因。"
	lower := strings.ToLower(output)
	if strings.Contains(lower, "jieshangai_elevation_failed") && elevatedCode == "" {
		// Dismissed UAC prompt (or the elevation host died before
		// Add-AppxPackage could run): the retry hint is the useful message.
		// When the elevated run did report a code, that code classifies below
		// instead — elevation already happened, so "needs authorization" would
		// be wrong.
		code, message = "permission", "桌面版需要 Windows 管理员授权；如果已拒绝授权，可以点击重试。"
	}
	if strings.Contains(lower, fmt.Sprintf("jieshangai_elevation_failed exit=%d", elevatedWrongAccountExit)) {
		code, message = "permission", "请用当前 Windows 账号本身的管理员权限确认；输入其他管理员账号时，桌面版不会装给当前账号。"
	}
	// Classify from the native code — including the elevated one, which is
	// more specific than any marker — when nothing more specific was detected.
	if code == "installation" || elevatedCode != "" {
		switch native {
		case "0X80070005", "0X80073D28", "0X8A150019":
			code, message = "permission", "桌面版需要 Windows 管理员授权。"
		case "0X800704C7", "0X8A150005", "0X8A150077":
			code, message = "cancelled", "系统授权或安装已取消，已完成的组件会保留。"
		case "0X80073CF3", "0X8A15004C":
			code, message = "dependency", "Windows 报告依赖、架构或版本冲突；请查看安装诊断中的具体要求。"
		case "0X80073CFD", "0X80073D10", "0X8A150010":
			code, message = "unsupported", "这个官方桌面包不支持当前 Windows 版本或架构。"
		case "0X80073D02":
			code, message = "busy", "请先保存任务并退出正在运行的桌面版，再继续安装。"
		case "0X80073D01", "0X80073CFF", "0X8A15001B", "0X8A15001C", "0X8A15003A":
			code, message = "policy", "Windows 安装策略阻止了此应用，请联系系统管理员；不会自动绕过策略。"
		case "0X80073CF4", "0X80070070":
			code, message = "disk", "系统磁盘空间不足，请释放空间后重试。"
		case "0X800B0109", "0X800B0100", "0X8A150011", "0X8A15002D":
			code, message = "signature", "官方安装包签名或证书验证未通过，已停止安装。"
		case "0X80072EE7", "0X80072EE2", "0X80072EFD", "0X8A150008", "0X8A15000F", "0X8A150012", "0X8A150045", "0X8A15006D":
			code, message = "network", "当前安装渠道暂不可达。"
		case "0XC000001D":
			code, message = "cpu", "启动遇到不支持的 CPU 指令，请检查虚拟机 CPU 能力。"
		case "0XC0000135", "0XC000007B":
			code, message = "runtime", "命令行启动失败，Windows 报告运行库缺失或架构不匹配。"
		}
	}
	if strings.Contains(lower, "lookup ") || strings.Contains(lower, "no such host") {
		code, message = "dns", "无法解析下载域名，请检查网络 DNS。"
	}
	if stage == "npm" && code == "installation" {
		code, message = "npm", "命令行安装失败；请复制安装诊断，查看 npm 的实际错误。"
		switch {
		case strings.Contains(lower, "enospc"):
			code, message = "disk", "命令行安装空间不足，请释放磁盘空间后重试。"
		case strings.Contains(lower, "eacces"), strings.Contains(lower, "eperm"):
			code, message = "filesystem", "命令行安装目录被占用或拒绝访问，请查看安装诊断。"
		case strings.Contains(lower, "enotfound"), strings.Contains(lower, "etimedout"), strings.Contains(lower, "econnreset"), strings.Contains(lower, "econnrefused"):
			code, message = "network", "国内 npm 镜像连接失败，可恢复下载后重试。"
		case strings.Contains(lower, "certificate"), strings.Contains(lower, "unable_to_verify"):
			code, message = "tls", "下载证书验证失败，请检查系统时间和网络证书。"
		}
	}
	f := installFailure(code, stage, source, message, err)
	f.NativeCode = native
	// Start-Process -Verb RunAs returns exit code 1 when the user dismisses
	// the elevation prompt. A later retry can legitimately succeed.
	if strings.Contains(lower, "jieshangai_elevation_failed") {
		f.Retryable = true
	}
	return f
}
func transferFailure(stage, raw string, status int, err error) error {
	if errors.Is(err, context.Canceled) {
		return context.Canceled
	}
	u, _ := url.Parse(raw)
	host := ""
	if u != nil {
		host = u.Hostname()
	}
	f := installFailure("network", stage, host, "下载源暂不可达："+host, errInstallNetwork)
	if status != 0 {
		f.NativeCode = fmt.Sprintf("HTTP %d", status)
		f.Message = fmt.Sprintf("下载源 %s 返回 HTTP %d。", host, status)
		f.Retryable = status == 408 || status == 429 || status >= 500
	}
	if err != nil {
		var dns *net.DNSError
		if errors.As(err, &dns) {
			f.Code = "dns"
			f.Message = "无法解析下载域名：" + host
		}
		if strings.Contains(strings.ToLower(err.Error()), "certificate") {
			f.Code = "tls"
			f.Message = "下载证书验证失败，请检查系统时间与网络证书。"
			f.Retryable = false
		}
	}
	return f
}

type ComponentStatus struct {
	Status    string          `json:"status"`
	Source    string          `json:"source,omitempty"`
	Phase     string          `json:"phase"`
	Message   string          `json:"message"`
	Failure   *InstallFailure `json:"failure,omitempty"`
	UpdatedAt string          `json:"updatedAt"`
}

func (m *toolManager) component(agent, kind, status, line string, failure *InstallFailure) {
	m.mu.Lock()
	s := m.states[agent]
	old := s.CLIInstall
	if kind == "gui" {
		old = s.GUIInstall
	}
	c := ComponentStatus{Status: status, Message: line, Phase: "prepare", UpdatedAt: time.Now().UTC().Format(time.RFC3339)}
	if old != nil && old.Status == "running" {
		c.Source = old.Source
		c.Phase = old.Phase
	}
	if kind == "cli" {
		c.Source = "npmmirror"
	}
	switch {
	case strings.Contains(line, "离线"):
		c.Source = "offline"
	case strings.Contains(line, "微软商店"):
		c.Source = "msstore"
	case strings.Contains(line, "官方"):
		c.Source = "official"
	}
	switch {
	case strings.Contains(line, "正在准备"):
		c.Phase = "prepare"
	case strings.Contains(line, "下载"):
		c.Phase = "download"
	case strings.Contains(line, "验证"), strings.Contains(line, "校验"):
		c.Phase = "verify"
	case strings.Contains(line, "授权"):
		c.Phase = "permission"
	case strings.Contains(line, "安装"):
		c.Phase = "install"
	}
	c.Failure = failure
	if failure != nil {
		c.Phase = failure.Stage
		// Trust the structured record over keyword inference: the generic
		// fallback message contains "离线安装包", which used to mislabel the
		// source as "offline" for unrelated CLI failures.
		c.Source = failure.Source
		if failure.Code == "waiting-system" {
			c.Status = "waiting-system"
		}
	}
	if kind == "cli" {
		s.CLIInstall = &c
	} else {
		s.GUIInstall = &c
	}
	m.states[agent] = s
	m.mu.Unlock()
	if status != "running" {
		_ = os.MkdirAll(filepath.Join(m.root, agent), 0700)
		data, _ := json.Marshal(c)
		_ = writePrivateFile(filepath.Join(m.root, agent, kind+"-status.json"), data)
	}
}
func (m *toolManager) componentError(agent, kind string, err error) {
	if err == nil {
		m.component(agent, kind, "ready", "安装与检测完成", nil)
		return
	}
	if errors.Is(err, context.Canceled) {
		m.component(agent, kind, "cancelled", "已取消，已安装的部分保留", nil)
		return
	}
	var f *InstallFailure
	if !errors.As(err, &f) {
		// The friendly text stays generic for the UI, but the persisted status
		// keeps the underlying error (redacted) so support diagnostics show the
		// actual cause instead of a dead-end wrapper message.
		f = installFailure("installation", "install", "", installErrorMessage(err), err)
		f.Detail = redactInstallText(err.Error())
	}
	m.component(agent, kind, "failed", f.Message, f)
}

// Output is bounded and scrubbed before being retained locally. Never persist env.
var installSecretPattern = regexp.MustCompile(`(?i)(bearer\s+|(?:api[_-]?key|token|secret|password)[=: ]+)[^\s,;"']+`)
var installURLPattern = regexp.MustCompile(`https?://[^\s"'<>]+`)

func redactInstallText(s string) string {
	for _, prefix := range []string{homeDir(), os.Getenv("USERPROFILE")} {
		if prefix != "" {
			s = strings.ReplaceAll(s, prefix, "<USER>")
		}
	}
	s = installSecretPattern.ReplaceAllString(s, "${1}<REDACTED>")
	s = installURLPattern.ReplaceAllStringFunc(s, func(raw string) string {
		u, e := url.Parse(raw)
		if e != nil {
			return "<URL>"
		}
		u.User = nil
		u.RawQuery = ""
		u.Fragment = ""
		return u.String()
	})
	if len(s) > 12000 {
		s = s[len(s)-12000:]
	}
	return s
}

var diagnosticMu = make(chan struct{}, 1)

func (m *toolManager) diagnostic(agent, component, stage, source, output string, err error) {
	diagnosticMu <- struct{}{}
	defer func() { <-diagnosticMu }()
	dir := filepath.Join(m.root, "diagnostics")
	_ = os.MkdirAll(dir, 0700)
	path := filepath.Join(dir, agent+".jsonl")
	if st, e := os.Stat(path); e == nil && st.Size() > 1<<20 {
		_ = os.Remove(path + ".1")
		_ = os.Rename(path, path+".1")
	}
	result := "ok"
	if err != nil {
		result = redactInstallText(err.Error())
	}
	data, _ := json.Marshal(map[string]string{"at": time.Now().UTC().Format(time.RFC3339), "agent": agent, "component": component, "stage": stage, "source": source, "result": result, "output": redactInstallText(output)})
	f, e := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600)
	if e == nil {
		_, _ = f.Write(append(data, '\n'))
		_ = f.Close()
	}
}

// Read only the tail we actually publish. Desktop snapshots request this five
// times a second, so reading a full rotating log wastes MB/s while idle.
func readDiagnosticTail(r io.ReaderAt, size int64) []byte {
	start := max(int64(0), size-16000)
	data, _ := io.ReadAll(io.NewSectionReader(r, start, size-start))
	if start > 0 {
		// The first retained byte may be in the middle of a JSON record or
		// UTF-8 character. Keep complete records, as with the previous tail.
		if i := bytes.IndexByte(data, '\n'); i >= 0 {
			return data[i+1:]
		}
		return nil
	}
	return data
}

func (m *toolManager) diagnostics(agent string) string {
	var data []byte
	if f, err := os.Open(filepath.Join(m.root, "diagnostics", agent+".jsonl")); err == nil {
		if st, err := f.Stat(); err == nil {
			data = readDiagnosticTail(f, st.Size())
		}
		_ = f.Close()
	}
	statuses := m.snapshot()
	report, _ := json.MarshalIndent(map[string]any{"agent": agent, "os": m.goos, "arch": m.arch, "components": statuses, "log": string(data)}, "", "  ")
	return redactInstallText(string(report))
}
