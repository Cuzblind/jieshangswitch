// Package core is the shared write / clear / backup / restore module for the
// 接上AI thin client (TRD §11.0, §11.4). Semantics mirror the POSIX scripts
// install/sh/install.sh (/i) and install/sh/uninstall.sh (/u) so that the
// resulting file diffs are identical. Secrets are never logged or put into
// errors; use MaskSecret for any display.
package core

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

const (
	ProviderID        = "jieshangai"
	CodexEnvName      = "JIESHANGAI_CODEX_KEY"
	DefaultCodexModel = "gpt-5.6-sol"
	MarkerStart       = "# jieshangai-provider-start"
	MarkerEnd         = "# jieshangai-provider-end"
	RCStart           = "# jieshangai-connection-start"
	RCEnd             = "# jieshangai-connection-end"
	BackupSuffix      = ".jieshangai.bak"
	ModelTag          = "# jieshangai-model"   // trailing tag on any model line /i writes
	CatalogTag        = "# jieshangai-catalog" // trailing tag on root model_catalog_json line
)

var (
	ErrBadInput         = errors.New("core: bad input")
	ErrConfigUnreadable = errors.New("core: config file unreadable")
	ErrNeedsConfirm     = errors.New("core: files changed after our write; confirmation required")
)

// Paths are relative to Root ($RELAY_HOME or $HOME in the scripts).
type Paths struct{ Root string }

func (p Paths) ClaudeSettings() string { return filepath.Join(p.Root, ".claude", "settings.json") }
func (p Paths) CodexConfig() string    { return filepath.Join(p.Root, ".codex", "config.toml") }
func (p Paths) Dir() string            { return filepath.Join(p.Root, ".jieshangai") }
func (p Paths) CodexEnv() string       { return filepath.Join(p.Dir(), "codex.env") }
func (p Paths) CodexCatalog() string   { return filepath.Join(p.Dir(), "codex-catalog.json") }
func (p Paths) Stamp() string          { return filepath.Join(p.Dir(), "install-stamp.json") }
func (p Paths) ErrorLog() string       { return filepath.Join(p.Dir(), "install-error.log") }
func (p Paths) Zshrc() string          { return filepath.Join(p.Root, ".zshrc") }
func (p Paths) Bashrc() string         { return filepath.Join(p.Root, ".bashrc") }

// RCFor mirrors pick_rc: basename($SHELL)=="zsh" -> .zshrc, else .bashrc.
func (p Paths) RCFor(shell string) string {
	if shell == "" {
		shell = "bash"
	}
	if filepath.Base(shell) == "zsh" {
		return p.Zshrc()
	}
	return p.Bashrc()
}

// MaskSecret returns a display-safe form: never the full value.
func MaskSecret(s string) string {
	if len(s) <= 8 {
		return "****"
	}
	return s[:3] + "****" + s[len(s)-2:]
}

var (
	reSecret = regexp.MustCompile(`^[A-Za-z0-9._~+/=-]{1,200}$`)
	reModel  = regexp.MustCompile(`^[A-Za-z0-9._:-]+$`)
)

// NormalizeOrigin mirrors site_root.
func NormalizeOrigin(origin string) (string, error) {
	o := strings.TrimSpace(origin)
	if o == "" {
		o = "https://jieshangai.com"
	}
	o = strings.TrimSuffix(o, "/")
	if !strings.HasPrefix(o, "http://") && !strings.HasPrefix(o, "https://") {
		return "", ErrBadInput
	}
	if strings.ContainsAny(o, " \t\n\r\v\f'\"\\") {
		return "", ErrBadInput
	}
	return strings.TrimSuffix(o, "/v1"), nil
}

func checkSecret(s string) error {
	if !reSecret.MatchString(s) {
		return ErrBadInput
	}
	return nil
}

// ---------- write (/i) ----------

type ClaudeOpts struct {
	Origin       string // site origin; written as ANTHROPIC_BASE_URL
	Secret       string
	Model        string // startup model (ANTHROPIC_MODEL), required when syncing model aliases
	SonnetModel  string // optional ANTHROPIC_DEFAULT_SONNET_MODEL
	OpusModel    string // optional ANTHROPIC_DEFAULT_OPUS_MODEL
	HaikuModel   string // optional ANTHROPIC_DEFAULT_HAIKU_MODEL
	ManageModels bool   // sync the four managed keys, or remove them all when no models are supplied
}

// WriteClaude mirrors write_claude_settings (python3 branch).
func (p Paths) WriteClaude(o ClaudeOpts) error {
	origin, err := NormalizeOrigin(o.Origin)
	if err != nil {
		return err
	}
	if err := checkSecret(o.Secret); err != nil {
		return err
	}
	for _, m := range []string{o.Model, o.SonnetModel, o.OpusModel, o.HaikuModel} {
		if trimmed := strings.TrimSpace(m); trimmed != "" && !reModel.MatchString(trimmed) {
			return ErrBadInput
		}
	}
	hasAliases := strings.TrimSpace(o.SonnetModel) != "" ||
		strings.TrimSpace(o.OpusModel) != "" || strings.TrimSpace(o.HaikuModel) != ""
	if o.ManageModels && hasAliases && strings.TrimSpace(o.Model) == "" {
		return fmt.Errorf("%w: managed Claude aliases require ClaudeOpts.Model", ErrBadInput)
	}
	path := p.ClaudeSettings()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	m, err := p.loadManifest()
	if err != nil {
		return err
	}
	if err := m.backupOnce(path); err != nil {
		return err
	}
	data := newObject()
	if raw, err := os.ReadFile(path); err == nil && len(raw) > 0 {
		v, perr := parseJSON(raw)
		if perr != nil {
			return ErrConfigUnreadable
		}
		obj, ok := v.(*jsonObject)
		if !ok {
			return ErrConfigUnreadable
		}
		data = obj
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	env, ok := func() (*jsonObject, bool) { v, _ := data.get("env"); e, ok := v.(*jsonObject); return e, ok }()
	if !ok {
		env = newObject()
	}
	env.set("ANTHROPIC_BASE_URL", origin)
	env.set("ANTHROPIC_AUTH_TOKEN", o.Secret)
	// Avoid Claude Code's "Auth conflict" warning when both token and API key coexist.
	env.del("ANTHROPIC_API_KEY")
	// Prevent first-run stalls / telemetry timeouts on mainland China networks.
	env.set("CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC", "1")
	env.set("DISABLE_TELEMETRY", "1")
	env.set("DISABLE_ERROR_REPORTING", "1")
	if o.ManageModels || hasAliases || strings.TrimSpace(o.Model) != "" {
		setOrDel := func(key, val string) {
			v := strings.TrimSpace(val)
			if v != "" {
				env.set(key, v)
			} else if o.ManageModels {
				env.del(key)
			}
		}
		setOrDel("ANTHROPIC_MODEL", o.Model)
		setOrDel("ANTHROPIC_DEFAULT_SONNET_MODEL", o.SonnetModel)
		setOrDel("ANTHROPIC_DEFAULT_OPUS_MODEL", o.OpusModel)
		setOrDel("ANTHROPIC_DEFAULT_HAIKU_MODEL", o.HaikuModel)
	}
	data.set("env", env)
	out := dumpJSON(data)
	if err := atomicWrite(path, out, 0o600); err != nil {
		return err
	}
	if err := m.recordWrite(path); err != nil {
		return err
	}
	return m.save()
}

type CatalogModel struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name,omitempty"`
	Vendor        string `json:"vendor,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
	Visibility    string `json:"visibility,omitempty"` // "list" (default) | "hide"
}

type CodexOpts struct {
	Origin        string // site origin; base_url = origin + "/v1"
	Secret        string
	Model         string         // RELAY_CODEX_MODEL or authorized catalog default
	ForceModel    bool           // Explicit user model choice overrides existing config.toml model
	Shell         string         // $SHELL, selects rc file
	CatalogModels []CatalogModel // Optional authorized multi-brand menu catalog
	AllowedModels []string       // Existing user-selected model is preserved when in this set (unless ForceModel)
	ClearCatalog  bool           // Explicitly remove managed catalog when server withdraws it
	SkipShellEnv  bool           // Skip POSIX codex.env and shell rc writes (e.g. Windows desktop sync)
}

// WriteCodexCatalog writes the managed Codex native model catalog JSON file
// matching Codex CLI's model_catalog_json schema atomically and verifies read-back.
func (p Paths) WriteCodexCatalog(models []CatalogModel) (string, error) {
	if len(models) == 0 {
		return "", nil
	}
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		return "", err
	}
	type codexReasoningLevel struct {
		Effort      string `json:"effort"`
		Description string `json:"description"`
	}
	type codexTruncationPolicy struct {
		Mode  string `json:"mode"`
		Limit int    `json:"limit"`
	}
	type codexCatalogEntry struct {
		Slug                       string                `json:"slug"`
		DisplayName                string                `json:"display_name"`
		Description                string                `json:"description,omitempty"`
		DefaultReasoningLevel      string                `json:"default_reasoning_level"`
		SupportedReasoningLevels   []codexReasoningLevel `json:"supported_reasoning_levels"`
		ShellType                  string                `json:"shell_type"`
		Visibility                 string                `json:"visibility"`
		SupportedInAPI             bool                  `json:"supported_in_api"`
		Priority                   int                   `json:"priority"`
		BaseInstructions           string                `json:"base_instructions"`
		SupportsReasoningSummaries bool                  `json:"supports_reasoning_summaries"`
		SupportVerbosity           bool                  `json:"support_verbosity"`
		TruncationPolicy           codexTruncationPolicy `json:"truncation_policy"`
		SupportsParallelToolCalls  bool                  `json:"supports_parallel_tool_calls"`
		ContextWindow              int                   `json:"context_window,omitempty"`
		ExperimentalSupportedTools []string              `json:"experimental_supported_tools"`
	}
	defaultLevels := []codexReasoningLevel{
		{Effort: "low", Description: "Faster responses with lighter reasoning"},
		{Effort: "medium", Description: "Balanced speed and reasoning depth"},
		{Effort: "high", Description: "Greater reasoning depth for complex tasks"},
	}
	entries := make([]codexCatalogEntry, 0, len(models))
	for i, m := range models {
		id := strings.TrimSpace(m.ID)
		if id == "" || !reModel.MatchString(id) {
			return "", ErrBadInput
		}
		name := strings.TrimSpace(m.DisplayName)
		if name == "" {
			name = id
		}
		ctxWin := m.ContextWindow
		if ctxWin <= 0 {
			ctxWin = 128000
		}
		vis := strings.TrimSpace(m.Visibility)
		if vis != "hide" {
			vis = "list"
		}
		entries = append(entries, codexCatalogEntry{
			Slug:                       id,
			DisplayName:                name,
			Description:                strings.TrimSpace(m.Vendor),
			DefaultReasoningLevel:      "medium",
			SupportedReasoningLevels:   defaultLevels,
			ShellType:                  "unified_exec",
			Visibility:                 vis,
			SupportedInAPI:             true,
			Priority:                   i + 1,
			BaseInstructions:           "You are a helpful coding assistant.",
			SupportsReasoningSummaries: false,
			SupportVerbosity:           false,
			TruncationPolicy:           codexTruncationPolicy{Mode: "bytes", Limit: 10000},
			SupportsParallelToolCalls:  true,
			ContextWindow:              ctxWin,
			ExperimentalSupportedTools: []string{},
		})
	}
	payload := map[string]any{"models": entries}
	raw, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return "", err
	}
	raw = append(raw, '\n')
	catPath := p.CodexCatalog()
	if err := atomicWrite(catPath, raw, 0o600); err != nil {
		return "", err
	}
	if verify, err := os.ReadFile(catPath); err != nil || len(verify) == 0 {
		return "", ErrConfigUnreadable
	}
	return catPath, nil
}

// WriteCodex mirrors write_codex_config + write_codex_env_file + write_shell_rc.
func (p Paths) WriteCodex(o CodexOpts) error {
	origin, err := NormalizeOrigin(o.Origin)
	if err != nil {
		return err
	}
	if err := checkSecret(o.Secret); err != nil {
		return err
	}
	model := strings.TrimSpace(o.Model)
	if model != "" && !reModel.MatchString(model) {
		return ErrBadInput
	}
	catalogPath := ""
	if o.ClearCatalog {
		_ = os.Remove(p.CodexCatalog())
	} else if len(o.CatalogModels) > 0 {
		catalogPath, err = p.WriteCodexCatalog(o.CatalogModels)
		if err != nil {
			return err
		}
	}
	m, err := p.loadManifest()
	if err != nil {
		return err
	}
	// config.toml
	path := p.CodexConfig()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if err := m.backupOnce(path); err != nil {
		return err
	}
	cur, err := readText(path)
	if err != nil {
		return err
	}
	if !o.ForceModel && len(o.AllowedModels) > 0 {
		if existing := extractCodexModel(cur); existing != "" {
			for _, allowed := range o.AllowedModels {
				if existing == allowed {
					model = "" // preserve user's still-valid model selection
					break
				}
			}
		}
	}
	out := renderCodexConfigWithCatalog(cur, origin+"/v1", model, catalogPath, o.ClearCatalog)
	if err := atomicWrite(path, []byte(out), 0o600); err != nil {
		return err
	}
	if err := m.recordWrite(path); err != nil {
		return err
	}
	if !o.SkipShellEnv {
		// codex.env (ours; never backed up)
		if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
			return err
		}
		env := fmt.Sprintf("export %s=%s\n", CodexEnvName, shellQuote(o.Secret))
		if err := atomicWrite(p.CodexEnv(), []byte(env), 0o600); err != nil {
			return err
		}
		// shell rc
		rc := p.RCFor(o.Shell)
		if err := os.MkdirAll(filepath.Dir(rc), 0o700); err != nil {
			return err
		}
		if err := m.backupOnce(rc); err != nil {
			return err
		}
		mode := os.FileMode(0o644)
		if st, err := os.Stat(rc); err == nil {
			mode = st.Mode().Perm()
		}
		cur, err = readText(rc)
		if err != nil {
			return err
		}
		rcOut := renderRC(cur)
		if err := atomicWrite(rc, []byte(rcOut), mode); err != nil {
			return err
		}
		if err := m.recordWrite(rc); err != nil {
			return err
		}
	}
	return m.save()
}

// WriteStamp mirrors write_stamp + clear_log (called after a successful install).
func (p Paths) WriteStamp(assistant, cli string) error {
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		return err
	}
	s := fmt.Sprintf("{\"assistant\":\"%s\",\"cli\":\"%s\",\"connection\":\"ok\"}\n", assistant, cli)
	if err := atomicWrite(p.Stamp(), []byte(s), 0o600); err != nil {
		return err
	}
	_ = os.Remove(p.ErrorLog())
	return nil
}

// LogCode mirrors log_code (local half; progress report is the caller's job).
func (p Paths) LogCode(code string) error {
	if err := os.MkdirAll(p.Dir(), 0o700); err != nil {
		return err
	}
	return atomicWrite(p.ErrorLog(), []byte(code+"\n"), 0o600)
}

var (
	reModelProvider = regexp.MustCompile(`^model_provider[[:space:]]*=`)
	reModelLine     = regexp.MustCompile(`^model[[:space:]]*=`)
	reCatalogLine   = regexp.MustCompile(`^model_catalog_json[[:space:]]*=`)
	reOurProvider   = regexp.MustCompile(`^model_provider[[:space:]]*=[[:space:]]*"jieshangai"[[:space:]]*$`)
	reOurModel      = regexp.MustCompile(`^model[[:space:]]*=.*# jieshangai-model$`)
	reOurCatalog    = regexp.MustCompile(`^model_catalog_json[[:space:]]*=.*# jieshangai-catalog$`)
	reTableHeader   = regexp.MustCompile(`^[[:space:]]*\[`)
)

func renderCodexConfig(cur, base, model string) string {
	return renderCodexConfigWithCatalog(cur, base, model, "", false)
}

func extractCodexModel(cur string) string {
	for _, l := range splitLines(trimNL(cur)) {
		if reTableHeader.MatchString(l) {
			break // only inspect root table
		}
		if !reModelLine.MatchString(l) {
			continue
		}
		_, rhs, ok := strings.Cut(l, "=")
		if !ok {
			continue
		}
		rhs = strings.TrimSpace(rhs)
		if idx := strings.Index(rhs, "#"); idx >= 0 {
			rhs = strings.TrimSpace(rhs[:idx])
		}
		rhs = strings.Trim(rhs, `"'`)
		if reModel.MatchString(rhs) {
			return rhs
		}
	}
	return ""
}

func renderCodexConfigWithCatalog(cur, base, model, catalogPath string, clearCatalog bool) string {
	lines := stripBlock(splitLines(trimNL(cur)), MarkerStart, MarkerEnd)
	// Strip previously managed root model_catalog_json line when rewriting or clearing.
	if catalogPath != "" || clearCatalog {
		filtered := make([]string, 0, len(lines))
		for _, l := range lines {
			if reOurCatalog.MatchString(l) {
				continue
			}
			filtered = append(filtered, l)
		}
		lines = filtered
	}
	// Only inspect root table (lines before the first [table] header) so existing
	// [profiles.*] tables never swallow root keys.
	firstTable := len(lines)
	for i, l := range lines {
		if reTableHeader.MatchString(l) {
			firstTable = i
			break
		}
	}
	has := false
	for i := 0; i < firstTable; i++ {
		if reModelProvider.MatchString(lines[i]) {
			lines[i] = `model_provider = "jieshangai"`
			has = true
		}
	}
	if !has {
		lines = append([]string{`model_provider = "jieshangai"`}, lines...)
		firstTable++
	}
	hasModel := false
	for i := 0; i < firstTable; i++ {
		if reModelLine.MatchString(lines[i]) {
			hasModel = true
		}
	}
	switch {
	case model != "" && hasModel:
		for i := 0; i < firstTable; i++ {
			if reModelLine.MatchString(lines[i]) {
				lines[i] = modelLine(model)
			}
		}
	case model != "":
		lines = insertModel(lines, model)
		firstTable++
	case !hasModel:
		lines = insertModel(lines, DefaultCodexModel)
		firstTable++
	}
	if catalogPath != "" {
		catLine := `model_catalog_json = ` + tomlQuote(catalogPath) + ` ` + CatalogTag
		replaced := false
		for i := 0; i < firstTable; i++ {
			if reCatalogLine.MatchString(lines[i]) {
				lines[i] = catLine
				replaced = true
				break
			}
		}
		if !replaced {
			// Insert right after model_provider / model in the root table, strictly before any [table].
			insertAt := 0
			for i := 0; i < firstTable; i++ {
				if reModelLine.MatchString(lines[i]) || reModelProvider.MatchString(lines[i]) {
					insertAt = i + 1
				}
			}
			outLines := make([]string, 0, len(lines)+1)
			outLines = append(outLines, lines[:insertAt]...)
			outLines = append(outLines, catLine)
			outLines = append(outLines, lines[insertAt:]...)
			lines = outLines
		}
	}
	text := trimNL(strings.Join(lines, "\n"))
	var b strings.Builder
	b.WriteString(text + "\n")
	b.WriteString("\n" + MarkerStart + "\n")
	b.WriteString("[model_providers." + ProviderID + "]\n")
	b.WriteString("name = \"接上AI\"\n")
	b.WriteString("base_url = \"" + base + "\"\n")
	b.WriteString("env_key = \"" + CodexEnvName + "\"\n")
	b.WriteString("wire_api = \"responses\"\n")
	b.WriteString(MarkerEnd + "\n")
	return b.String()
}

func tomlQuote(s string) string {
	s = strings.ReplaceAll(s, `\`, `\\`)
	s = strings.ReplaceAll(s, `"`, `\"`)
	return `"` + s + `"`
}

func insertModel(lines []string, model string) []string {
	out := make([]string, 0, len(lines)+1)
	done := false
	for _, l := range lines {
		out = append(out, l)
		if !done && reModelProvider.MatchString(l) {
			out = append(out, modelLine(model))
			done = true
		}
	}
	if !done {
		out = append(out, modelLine(model))
	}
	return out
}

func modelLine(model string) string { return `model = "` + model + `" ` + ModelTag }

func renderRC(cur string) string {
	text := trimNL(strings.Join(stripBlock(splitLines(trimNL(cur)), RCStart, RCEnd), "\n"))
	var b strings.Builder
	if text != "" {
		b.WriteString(text + "\n\n")
	}
	b.WriteString(RCStart + "\n")
	b.WriteString("if [ -f \"$HOME/.jieshangai/codex.env\" ]; then\n")
	b.WriteString("  . \"$HOME/.jieshangai/codex.env\"\n")
	b.WriteString("fi\n")
	b.WriteString(RCEnd + "\n")
	return b.String()
}

// ---------- clear (/u) ----------

// Clear removes only our entries (the strip step of /u). It does not touch
// backups, the manifest or official CLIs. For full /u behaviour use Restore.
//
// settings.json is only touched when it is tracked in the manifest or no
// manifest exists (pre-manifest /i installs). config.toml / rc files are only
// rewritten when they contain our marks.
func (p Paths) Clear() error {
	m, err := p.loadManifest()
	if err != nil {
		return err
	}
	if _, tracked := m.Files[m.key(p.ClaudeSettings())]; tracked || len(m.Files) == 0 {
		if err := clearClaude(p.ClaudeSettings()); err != nil {
			return err
		}
	}
	if err := stripMarkedFile(p.CodexConfig(), MarkerStart, MarkerEnd, true); err != nil {
		return err
	}
	if err := stripMarkedFile(p.Zshrc(), RCStart, RCEnd, false); err != nil {
		return err
	}
	if err := stripMarkedFile(p.Bashrc(), RCStart, RCEnd, false); err != nil {
		return err
	}
	for _, f := range []string{p.CodexEnv(), p.CodexCatalog(), filepath.Join(p.Dir(), "model-prefs.json"), p.Stamp(), p.ErrorLog()} {
		if err := os.Remove(f); err != nil && !os.IsNotExist(err) {
			return err
		}
	}
	_ = os.Remove(p.Dir()) // rmdir; fails harmlessly if not empty (e.g. manifest kept)
	return nil
}

func clearClaude(path string) error {
	st, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && !st.Mode().IsRegular()) {
		return nil
	}
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return ErrConfigUnreadable
	}
	if len(raw) == 0 || strings.TrimSpace(string(raw)) == "" {
		return nil
	}
	v, err := parseJSON(raw)
	if err != nil {
		return ErrConfigUnreadable
	}
	data, ok := v.(*jsonObject)
	if !ok {
		return nil
	}
	if ev, _ := data.get("env"); ev != nil {
		if env, ok := ev.(*jsonObject); ok {
			for _, k := range []string{
				"ANTHROPIC_BASE_URL",
				"ANTHROPIC_AUTH_TOKEN",
				"ANTHROPIC_API_KEY",
				"ANTHROPIC_MODEL",
				"ANTHROPIC_DEFAULT_SONNET_MODEL",
				"ANTHROPIC_DEFAULT_OPUS_MODEL",
				"ANTHROPIC_DEFAULT_HAIKU_MODEL",
				"CLAUDE_CODE_DISABLE_NONESSENTIAL_TRAFFIC",
				"DISABLE_TELEMETRY",
				"DISABLE_ERROR_REPORTING",
			} {
				env.del(k)
			}
			if env.len() == 0 {
				data.del("env")
			}
		}
	}
	mode := st.Mode().Perm()
	if mode == 0 {
		mode = 0o600
	}
	return atomicWrite(path, dumpJSON(data), mode)
}

func stripMarkedFile(path, start, end string, codex bool) error {
	st, err := os.Stat(path)
	if os.IsNotExist(err) || (err == nil && !st.Mode().IsRegular()) {
		return nil
	}
	if err != nil {
		return err
	}
	cur, err := readText(path)
	if err != nil {
		return err
	}
	if !hasOurMarks(cur, start, codex) {
		return nil
	}
	lines := stripBlock(splitLines(trimNL(cur)), start, end)
	text := trimNL(strings.Join(lines, "\n"))
	if codex {
		var kept []string
		for _, l := range splitLines(text) {
			if !reOurProvider.MatchString(l) && !reOurModel.MatchString(l) && !reOurCatalog.MatchString(l) {
				kept = append(kept, l)
			}
		}
		text = trimNL(strings.Join(kept, "\n"))
	}
	return atomicWrite(path, []byte(text+"\n"), st.Mode().Perm())
}

func hasOurMarks(text, start string, codex bool) bool {
	for _, l := range strings.Split(text, "\n") {
		if l == start || (codex && (reOurProvider.MatchString(l) || reOurModel.MatchString(l) || reOurCatalog.MatchString(l))) {
			return true
		}
	}
	return false
}

// ---------- helpers ----------

// splitLines mimics `printf '%s\n' "$text" | awk`: "" yields one empty line.
func splitLines(s string) []string { return strings.Split(s, "\n") }

// trimNL mimics $(...) stripping trailing newlines.
func trimNL(s string) string { return strings.TrimRight(s, "\n") }

func stripBlock(lines []string, start, end string) []string {
	out := []string{}
	skip := false
	for _, l := range lines {
		switch {
		case l == start:
			skip = true
		case l == end:
			skip = false
		case !skip:
			out = append(out, l)
		}
	}
	return out
}

func readText(path string) (string, error) {
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func shellQuote(s string) string { return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'" }

func atomicWrite(path string, data []byte, mode os.FileMode) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".jieshangai.tmp.*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}
