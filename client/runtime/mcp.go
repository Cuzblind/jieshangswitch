package clientruntime

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"unicode/utf8"
)

// MCP management is local only. Servers are kept in ~/.jieshangai/mcp and an
// enabled server is projected into the assistant's own config:
//   Claude Code: the "mcpServers" field of ~/.claude.json, every other field kept;
//   Codex:       a marked block of [mcp_servers.*] tables in ~/.codex/config.toml.
// Whether a server is enabled is always read back from that file, so a restore
// or a hand edit can never leave the list out of date. Servers the user added
// themselves are listed read-only and never overwritten.

const (
	mcpBlockStart = "# jieshangai-mcp-start"
	mcpBlockEnd   = "# jieshangai-mcp-end"
	maxMCPServers = 50
)

var (
	reMCPName = regexp.MustCompile(`^[A-Za-z0-9_-]{1,40}$`)
	reEnvName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]{0,63}$`)
	// Header names double as bare TOML keys in Codex's http_headers table.
	reHeaderName = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)
)

type MCPServer struct {
	ID      string            `json:"id"`
	Name    string            `json:"name"`
	Type    string            `json:"type"` // "stdio", "http" or "sse"
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	URL     string            `json:"url,omitempty"`
	// Headers go with http and sse; values are secrets like env values.
	Headers map[string]string `json:"headers,omitempty"`
	// Cwd is the optional working directory of a stdio server.
	Cwd string `json:"cwd,omitempty"`
	// Derived on every read, never stored.
	Enabled  bool `json:"enabled"`
	Conflict bool `json:"conflict,omitempty"`
	External bool `json:"external,omitempty"`
}

var mcpMu sync.Mutex

func mcpStore(agent string) string {
	return filepath.Join(homeDir(), ".jieshangai", "mcp", agent+".json")
}

func claudeJSONPath() string { return filepath.Join(homeDir(), ".claude.json") }
func codexConfigPath() string {
	return filepath.Join(homeDir(), ".codex", "config.toml")
}

func loadMCP(agent string) ([]MCPServer, error) {
	data, err := os.ReadFile(mcpStore(agent))
	if os.IsNotExist(err) {
		return []MCPServer{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []MCPServer
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New("MCP 记录已损坏，原文件未改动")
	}
	return list, nil
}

func saveMCP(agent string, list []MCPServer) error {
	clean := make([]MCPServer, 0, len(list))
	for _, s := range list {
		if s.External {
			continue
		}
		s.Enabled, s.Conflict = false, false
		clean = append(clean, s)
	}
	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(mcpStore(agent)), 0o700); err != nil {
		return err
	}
	return writePrivateFile(mcpStore(agent), data)
}

func validateMCP(s MCPServer) error {
	if !reMCPName.MatchString(s.Name) {
		return errors.New("名称只能用英文字母、数字、- 和 _，不超过 40 个字符")
	}
	ok := func(v string, n int) bool { return utf8.ValidString(v) && len(v) <= n && !strings.ContainsRune(v, 0) }
	switch s.Type {
	case "stdio":
		if strings.TrimSpace(s.Command) == "" || !ok(s.Command, 1024) {
			return errors.New("请填写要运行的命令")
		}
		if len(s.Args) > 64 {
			return errors.New("参数太多")
		}
		for _, a := range s.Args {
			if !ok(a, 2048) {
				return errors.New("参数无效")
			}
		}
		if !ok(s.Cwd, 1024) {
			return errors.New("工作目录无效")
		}
	case "http", "sse":
		if !(strings.HasPrefix(s.URL, "https://") || strings.HasPrefix(s.URL, "http://")) || !ok(s.URL, 2048) || strings.ContainsAny(s.URL, " \n\r\t") {
			return errors.New("请填写以 http:// 或 https:// 开头的地址")
		}
		if len(s.Headers) > 32 {
			return errors.New("请求头太多")
		}
		for k, v := range s.Headers {
			if !reHeaderName.MatchString(k) || !ok(v, 4096) || strings.ContainsAny(v, "\r\n") {
				return errors.New("请求头名称或值无效")
			}
		}
	default:
		return errors.New("请选择本地命令、HTTP 或 SSE")
	}
	if len(s.Env) > 32 {
		return errors.New("环境变量太多")
	}
	for k, v := range s.Env {
		if !reEnvName.MatchString(k) || !ok(v, 4096) {
			return errors.New("环境变量名或值无效")
		}
	}
	return nil
}

// claudeEntry is the exact JSON Claude Code expects for one server.
func claudeEntry(s MCPServer) map[string]any {
	if s.Type == "http" || s.Type == "sse" {
		e := map[string]any{"type": s.Type, "url": s.URL}
		if len(s.Headers) > 0 {
			e["headers"] = s.Headers
		}
		return e
	}
	e := map[string]any{"type": "stdio", "command": s.Command, "args": nonNil(s.Args)}
	if len(s.Env) > 0 {
		e["env"] = s.Env
	}
	if s.Cwd != "" {
		e["cwd"] = s.Cwd
	}
	return e
}

func nonNil(a []string) []string {
	if a == nil {
		return []string{}
	}
	return a
}

// readClaudeJSON keeps every top-level field as raw JSON so a rewrite only
// changes "mcpServers".
func readClaudeJSON() (map[string]json.RawMessage, map[string]json.RawMessage, error) {
	top := map[string]json.RawMessage{}
	servers := map[string]json.RawMessage{}
	data, err := os.ReadFile(claudeJSONPath())
	if os.IsNotExist(err) {
		return top, servers, nil
	}
	if err != nil {
		return nil, nil, err
	}
	if len(bytes.TrimSpace(data)) > 0 {
		if err := json.Unmarshal(data, &top); err != nil {
			return nil, nil, errors.New("Claude 的配置文件 ~/.claude.json 格式不正确，没有改动它")
		}
	}
	if raw, ok := top["mcpServers"]; ok && len(raw) > 0 && string(raw) != "null" {
		if err := json.Unmarshal(raw, &servers); err != nil {
			return nil, nil, errors.New("Claude 的 MCP 设置格式不正确，没有改动它")
		}
	}
	return top, servers, nil
}

func writeClaudeServers(top, servers map[string]json.RawMessage) error {
	raw, err := json.Marshal(servers)
	if err != nil {
		return err
	}
	top["mcpServers"] = raw
	data, err := json.MarshalIndent(top, "", "  ")
	if err != nil {
		return err
	}
	if err := backupOnce(claudeJSONPath()); err != nil {
		return err
	}
	return writeUserFile(claudeJSONPath(), data)
}

func sameJSON(a json.RawMessage, b any) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil {
		return false
	}
	bb, _ := json.Marshal(b)
	_ = json.Unmarshal(bb, &y)
	xb, _ := json.Marshal(x)
	yb, _ := json.Marshal(y)
	return bytes.Equal(xb, yb)
}

// backupOnce keeps the first version of a file we ever change, so a user can
// always get back to it by hand.
func backupOnce(path string) error {
	bak := path + ".jieshangai-mcp.bak"
	if _, err := os.Stat(bak); err == nil {
		return nil
	}
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	return writePrivateFile(bak, data)
}

func tomlString(s string) string {
	var b strings.Builder
	b.WriteByte('"')
	for _, r := range s {
		switch r {
		case '"':
			b.WriteString(`\"`)
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		case '\t':
			b.WriteString(`\t`)
		default:
			if r < 0x20 || r == 0x7f {
				fmt.Fprintf(&b, `\u%04X`, r)
			} else {
				b.WriteRune(r)
			}
		}
	}
	b.WriteByte('"')
	return b.String()
}

func codexTable(s MCPServer) string {
	var b strings.Builder
	b.WriteString("[mcp_servers." + s.Name + "]\n")
	// Codex infers the transport from url, so http and sse are written alike.
	if s.Type == "http" || s.Type == "sse" {
		b.WriteString("url = " + tomlString(s.URL) + "\n")
		writeTOMLTable(&b, "[mcp_servers."+s.Name+".http_headers]", s.Headers)
		return b.String()
	}
	b.WriteString("command = " + tomlString(s.Command) + "\n")
	quoted := make([]string, len(s.Args))
	for i, a := range s.Args {
		quoted[i] = tomlString(a)
	}
	b.WriteString("args = [" + strings.Join(quoted, ", ") + "]\n")
	if s.Cwd != "" {
		b.WriteString("cwd = " + tomlString(s.Cwd) + "\n")
	}
	writeTOMLTable(&b, "[mcp_servers."+s.Name+".env]", s.Env)
	return b.String()
}

// writeTOMLTable writes a sorted key/value sub-table, or nothing when empty.
func writeTOMLTable(b *strings.Builder, header string, values map[string]string) {
	if len(values) == 0 {
		return
	}
	b.WriteString("\n" + header + "\n")
	for _, k := range sortedKeys(values) {
		b.WriteString(k + " = " + tomlString(values[k]) + "\n")
	}
}

var reCodexMCPTable = regexp.MustCompile(`^\s*\[\s*mcp_servers\.(?:"([^"]+)"|([A-Za-z0-9_-]+))\s*\]`)

// splitCodexMCP returns the config without our block, the names inside our
// block, and the names of servers the user defined elsewhere.
func splitCodexMCP(text string) (rest []string, ours, theirs map[string]bool) {
	ours, theirs = map[string]bool{}, map[string]bool{}
	inside := false
	for _, line := range strings.Split(strings.ReplaceAll(text, "\r\n", "\n"), "\n") {
		trimmed := strings.TrimSpace(line)
		switch {
		case trimmed == mcpBlockStart:
			inside = true
			continue
		case trimmed == mcpBlockEnd:
			inside = false
			continue
		}
		name := ""
		if m := reCodexMCPTable.FindStringSubmatch(line); m != nil {
			name = m[1] + m[2]
		}
		if inside {
			if name != "" {
				ours[name] = true
			}
			continue
		}
		if name != "" {
			theirs[name] = true
		}
		rest = append(rest, line)
	}
	for len(rest) > 0 && strings.TrimSpace(rest[len(rest)-1]) == "" {
		rest = rest[:len(rest)-1]
	}
	return rest, ours, theirs
}

func readCodexConfig() (string, error) {
	data, err := os.ReadFile(codexConfigPath())
	if os.IsNotExist(err) {
		return "", nil
	}
	return string(data), err
}

func writeCodexMCP(rest []string, enabled []MCPServer) error {
	var b strings.Builder
	if len(rest) > 0 {
		b.WriteString(strings.Join(rest, "\n") + "\n")
	}
	if len(enabled) > 0 {
		sort.Slice(enabled, func(i, j int) bool { return enabled[i].Name < enabled[j].Name })
		if b.Len() > 0 {
			b.WriteString("\n")
		}
		b.WriteString(mcpBlockStart + "\n")
		for i, s := range enabled {
			if i > 0 {
				b.WriteString("\n")
			}
			b.WriteString(codexTable(s))
		}
		b.WriteString(mcpBlockEnd + "\n")
	}
	if err := backupOnce(codexConfigPath()); err != nil {
		return err
	}
	return writeUserFile(codexConfigPath(), []byte(b.String()))
}

// decorate fills Enabled/Conflict from the live file and appends read-only
// rows for servers the user configured themselves.
func decorate(agent string, list []MCPServer) ([]MCPServer, error) {
	known := map[string]bool{}
	for _, s := range list {
		known[s.Name] = true
	}
	switch agent {
	case "claude":
		_, live, err := readClaudeJSON()
		if err != nil {
			return nil, err
		}
		for i := range list {
			if raw, ok := live[list[i].Name]; ok {
				list[i].Enabled = sameJSON(raw, claudeEntry(list[i]))
				list[i].Conflict = !list[i].Enabled
			}
		}
		for _, name := range sortedKeys(live) {
			if !known[name] {
				list = append(list, MCPServer{ID: "external-" + name, Name: name, External: true, Enabled: true})
			}
		}
	case "codex":
		text, err := readCodexConfig()
		if err != nil {
			return nil, err
		}
		_, ours, theirs := splitCodexMCP(text)
		for i := range list {
			list[i].Conflict = theirs[list[i].Name]
			list[i].Enabled = ours[list[i].Name] && !list[i].Conflict
		}
		for _, name := range sortedKeys(theirs) {
			if !known[name] {
				list = append(list, MCPServer{ID: "external-" + name, Name: name, External: true, Enabled: true})
			}
		}
	default:
		return nil, errors.New("请选择 Claude Code 或 Codex")
	}
	return list, nil
}

func sortedKeys[V any](m map[string]V) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

var errMCPConflict = errors.New("助手里已有同名的 MCP 服务，是你自己加的，没有覆盖。请换个名称")

// project takes old off the assistant's config (only if it is still exactly
// ours) and puts next on. list is the stored list after the change.
func project(agent string, list []MCPServer, old, next *MCPServer) error {
	switch agent {
	case "claude":
		top, live, err := readClaudeJSON()
		if err != nil {
			return err
		}
		if old != nil {
			if raw, ok := live[old.Name]; ok && sameJSON(raw, claudeEntry(*old)) {
				delete(live, old.Name)
			}
		}
		if next != nil {
			if raw, ok := live[next.Name]; ok && !sameJSON(raw, claudeEntry(*next)) {
				return errMCPConflict
			}
			raw, err := json.Marshal(claudeEntry(*next))
			if err != nil {
				return err
			}
			live[next.Name] = raw
		}
		if err := writeClaudeServers(top, live); err != nil {
			return errors.New("没能写入 Claude 的配置文件")
		}
		return nil
	case "codex":
		text, err := readCodexConfig()
		if err != nil {
			return err
		}
		rest, ours, theirs := splitCodexMCP(text)
		if old != nil {
			delete(ours, old.Name)
		}
		if next != nil {
			if theirs[next.Name] {
				return errMCPConflict
			}
			ours[next.Name] = true
		}
		var enabled []MCPServer
		for _, s := range list {
			if ours[s.Name] && !s.External {
				enabled = append(enabled, s)
			}
		}
		if err := writeCodexMCP(rest, enabled); err != nil {
			return errors.New("没能写入 Codex 的配置文件")
		}
		return nil
	}
	return errors.New("请选择 Claude Code 或 Codex")
}

func findMCP(list []MCPServer, id string) int {
	for i := range list {
		if list[i].ID == id && !list[i].External {
			return i
		}
	}
	return -1
}

func ListMCP(agent string) ([]MCPServer, error) {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	list, err := loadMCP(agent)
	if err != nil {
		return nil, err
	}
	return decorate(agent, list)
}

// SaveMCP creates (empty ID) or edits a server. Editing an enabled server
// updates the assistant's config in place.
func SaveMCP(agent string, s MCPServer) ([]MCPServer, error) {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	s.Name = strings.TrimSpace(s.Name)
	s.Command = strings.TrimSpace(s.Command)
	s.URL = strings.TrimSpace(s.URL)
	s.Cwd = strings.TrimSpace(s.Cwd)
	if s.Type == "http" || s.Type == "sse" {
		s.Command, s.Args, s.Env, s.Cwd = "", nil, nil, ""
	} else {
		s.URL, s.Headers = "", nil
	}
	if err := validateMCP(s); err != nil {
		return nil, err
	}
	stored, err := loadMCP(agent)
	if err != nil {
		return nil, err
	}
	list, err := decorate(agent, stored)
	if err != nil {
		return nil, err
	}
	for _, other := range list {
		if other.Name == s.Name && other.ID != s.ID {
			return nil, errors.New("已经有同名的 MCP 服务")
		}
	}
	index := -1
	if s.ID != "" {
		if index = findMCP(list, s.ID); index < 0 {
			return nil, errors.New("没有找到这个 MCP 服务")
		}
	}
	if index < 0 {
		if len(stored) >= maxMCPServers {
			return nil, errors.New("MCP 服务太多了，请先删掉一些")
		}
		s.ID = newPromptID("m-")
		stored = append(stored, s)
	} else {
		old := list[index]
		// The UI never sees env values; a blank one means "keep what is stored".
		for k, v := range s.Env {
			if v == "" {
				s.Env[k] = stored[index].Env[k]
			}
		}
		for k, v := range s.Headers {
			if v == "" {
				s.Headers[k] = stored[index].Headers[k]
			}
		}
		stored[index] = s
		if old.Enabled {
			if err := project(agent, stored, &old, &s); err != nil {
				return nil, err
			}
		}
	}
	if err := saveMCP(agent, stored); err != nil {
		return nil, err
	}
	return decorate(agent, stored)
}

func SetMCPEnabled(agent, id string, on bool) ([]MCPServer, error) {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	stored, err := loadMCP(agent)
	if err != nil {
		return nil, err
	}
	list, err := decorate(agent, append([]MCPServer(nil), stored...))
	if err != nil {
		return nil, err
	}
	i := findMCP(list, id)
	if i < 0 {
		return nil, errors.New("没有找到这个 MCP 服务")
	}
	s := list[i]
	if on && !s.Enabled {
		err = project(agent, stored, nil, &s)
	} else if !on && s.Enabled {
		err = project(agent, stored, &s, nil)
	}
	if err != nil {
		return nil, err
	}
	return decorate(agent, stored)
}

// DeleteMCP also takes the server off the assistant if it is enabled.
func DeleteMCP(agent, id string) ([]MCPServer, error) {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	stored, err := loadMCP(agent)
	if err != nil {
		return nil, err
	}
	list, err := decorate(agent, append([]MCPServer(nil), stored...))
	if err != nil {
		return nil, err
	}
	i := findMCP(list, id)
	if i < 0 {
		return nil, errors.New("没有找到这个 MCP 服务")
	}
	old := list[i]
	stored = append(stored[:i:i], stored[i+1:]...)
	if old.Enabled {
		if err := project(agent, stored, &old, nil); err != nil {
			return nil, err
		}
	}
	if err := saveMCP(agent, stored); err != nil {
		return nil, err
	}
	return decorate(agent, stored)
}

// clearMCP is part of the global restore: our servers come off both
// assistants, the stored list stays so they can be enabled again.
func clearMCP() error {
	mcpMu.Lock()
	defer mcpMu.Unlock()
	for _, agent := range []string{"claude", "codex"} {
		stored, err := loadMCP(agent)
		if err != nil {
			return err
		}
		list, err := decorate(agent, append([]MCPServer(nil), stored...))
		if err != nil {
			return err
		}
		for _, s := range list {
			if s.Enabled && !s.External {
				s := s
				if err := project(agent, stored, &s, nil); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

var (
	mcpCacheMu sync.Mutex
	mcpCache   = map[string][]MCPServer{}
)

func cacheMCP(agent string, list []MCPServer) {
	mcpCacheMu.Lock()
	defer mcpCacheMu.Unlock()
	mcpCache[agent] = maskMCP(list)
}

// maskMCP hides environment and header values (usually tokens) from the UI
// state; the form shows the keys and keeps the stored value unless retyped.
func maskMCP(list []MCPServer) []MCPServer {
	out := make([]MCPServer, len(list))
	for i, s := range list {
		out[i] = s
		out[i].Args = append([]string(nil), s.Args...)
		out[i].Env = blankValues(s.Env)
		out[i].Headers = blankValues(s.Headers)
	}
	return out
}

func blankValues(m map[string]string) map[string]string {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]string, len(m))
	for k := range m {
		out[k] = ""
	}
	return out
}

func mcpSnapshot() map[string][]MCPServer {
	mcpCacheMu.Lock()
	defer mcpCacheMu.Unlock()
	if len(mcpCache) == 0 {
		return nil
	}
	out := make(map[string][]MCPServer, len(mcpCache))
	for k, v := range mcpCache {
		out[k] = maskMCP(v)
	}
	return out
}
