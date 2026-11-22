package clientruntime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"time"

	"jieshangai/client/core"
)

var reTicket = regexp.MustCompile(`^[A-Za-z0-9_-]{8,128}$`)

// Env carries the platform hooks; tests swap them out.
type Env struct {
	GOOS, OSLabel, Origin, Web string
	HTTP                       *http.Client

	Write              func(agent, origin, secret string) error
	WriteStamp         func(agent, cli string) error
	Restore            func(confirm bool) (usedBackup bool, err error)
	Edited             func() bool
	Configured         func(agent, origin string) (secret string, ok bool)
	FindCLI            func(agent string) bool
	InstallCLI         func(agent string) error
	InstallDesktop     func(context.Context, string, string, func(string)) error
	ToolSnapshot       func() []ToolStatus
	InspectTools       func()
	CheckToolUpdates   func(context.Context) error
	InstallDiagnostics func(agent string) string
	LaunchGUI          func(agent, secret, origin string) error
	Running            func(agent string) bool
	Kill               func(agent string) error
	Launch             func(agent, secret string) error
	OfficialLogin      func(agent string) bool
	Logout             func(agent string) error
	Open               func(target string) error // URL or a folder
	OpenDir            func(which string) error
}

var (
	errExpired = errors.New("expired")
	errNetwork = errors.New("network")
	errOther   = errors.New("other")
)

// ---------- gateway calls (same endpoints as /i) ----------

func (a *App) post(path string, body any) (*http.Response, error) {
	return a.postAuth(path, body, "")
}

func (a *App) postAuth(path string, body any, auth string) (*http.Response, error) {
	b, _ := json.Marshal(body)
	req, _ := http.NewRequest(http.MethodPost, a.origin+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if auth != "" {
		req.Header.Set("Authorization", "Bearer "+auth)
	}
	return a.env.HTTP.Do(req)
}

func classifyNet(err error) error {
	var ne net.Error
	var ue *url.Error
	if errors.As(err, &ne) || errors.As(err, &ue) {
		return errNetwork
	}
	return errNetwork
}

// claim redeems the one-time ticket for the connection secret (M1).
func (a *App) claim(ticket string) (string, error) {
	body := map[string]any{"token": ticket, "client": true}
	if d := a.thisDevice(); d != nil {
		body["device"] = d
	}
	resp, err := a.post("/api/install/claim", body)
	if err != nil {
		return "", classifyNet(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		return "", errExpired
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		return "", errNetwork
	case resp.StatusCode != http.StatusOK:
		return "", errOther
	}
	var out struct {
		Success bool
		Data    struct {
			Key        string `json:"key"`
			ClientAuth string `json:"client_auth"`
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil || !out.Success || out.Data.Key == "" {
		return "", errExpired
	}
	if out.Data.ClientAuth != "" {
		a.saveClientAuth(out.Data.ClientAuth)
	}
	return out.Data.Key, nil
}

func (a *App) issueClientTicket(agent string) (string, error) {
	auth := a.clientAuth()
	if auth == "" {
		return "", errExpired
	}
	resp, err := a.postAuth("/api/install/client/token", map[string]any{"agent": agent}, auth)
	if err != nil {
		return "", classifyNet(err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusUnauthorized, resp.StatusCode == http.StatusForbidden, resp.StatusCode == http.StatusNotFound, resp.StatusCode == http.StatusGone:
		return "", errExpired
	case resp.StatusCode >= 500, resp.StatusCode == http.StatusTooManyRequests:
		return "", errNetwork
	case resp.StatusCode != http.StatusOK:
		return "", errOther
	}
	var out struct {
		Success bool
		Data    struct {
			Token string `json:"token"`
		}
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return "", errNetwork // parse failure: keep auth, allow retry
	}
	if !out.Success || !reTicket.MatchString(out.Data.Token) {
		return "", errOther // business failure: keep auth, allow retry
	}
	return out.Data.Token, nil
}

func (a *App) connectFromAuth(agent string) {
	ticket, err := a.issueClientTicket(agent)
	if err != nil {
		if errors.Is(err, errExpired) {
			a.clearClientAuth()
			a.mu.Lock()
			a.ticket = ""
			a.mu.Unlock()
			a.setView(errorView("reauth", agent))
			if !a.desktop {
				go a.refreshStatus(false)
			}
			return
		}
		a.fail(agent, err, "claim-failed", "")
		return
	}
	a.mu.Lock()
	a.ticket = ticket
	a.retries = map[string]int{}
	a.current = session{} // do not reuse a key that claim may have replaced
	a.mu.Unlock()
	a.connect(agent, ticket)
}

func (a *App) progress(ticket string, beat int, status, code string) {
	if ticket == "" {
		return
	}
	body := map[string]any{"token": ticket, "status": status}
	if beat >= 0 {
		body["beat"] = beat
	}
	if code != "" {
		body["error"] = code
	}
	if resp, err := a.post("/api/install/progress", body); err == nil {
		resp.Body.Close()
	}
}

// check is the light connectivity test (M8): GET /v1/models + Bearer, as /i.
// Validates JSON response body so empty bodies, empty/null catalogs, or business
// errors are never treated as a healthy connection.
func (a *App) check(secret string) error {
	req, _ := http.NewRequest(http.MethodGet, a.origin+"/v1/models", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return errNetwork
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	switch {
	case resp.StatusCode == http.StatusOK:
		trimmed := bytes.TrimSpace(body)
		if len(trimmed) == 0 {
			return errOther
		}
		var probe struct {
			Success *bool           `json:"success"`
			Error   json.RawMessage `json:"error"`
			Data    []struct {
				ID string `json:"id"`
			} `json:"data"`
		}
		if json.Unmarshal(trimmed, &probe) != nil {
			return errOther
		}
		if probe.Success != nil && !*probe.Success {
			return errOther
		}
		if len(bytes.TrimSpace(probe.Error)) > 0 && string(bytes.TrimSpace(probe.Error)) != "null" {
			return errOther
		}
		for _, item := range probe.Data {
			if strings.TrimSpace(item.ID) != "" {
				return nil
			}
		}
		return errOther
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return errExpired
	case resp.StatusCode >= 500 || resp.StatusCode == http.StatusTooManyRequests:
		return errNetwork
	}
	return errOther
}

// fetchDesktopCatalog retrieves the authorized multi-brand desktop model catalog
// for a connected assistant without treating empty/failed payloads as valid.
func (a *App) fetchDesktopCatalog(secret string) (*DesktopCatalogPayload, error) {
	req, _ := http.NewRequest(http.MethodGet, a.origin+"/api/install/catalog", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return nil, errNetwork
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errExpired
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errOther
	}
	var out struct {
		Success bool                  `json:"success"`
		Data    DesktopCatalogPayload `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<18)).Decode(&out); err != nil || !out.Success {
		return nil, errOther
	}
	// Distinguish valid zero-authorized catalog ("models": []) from malformed/missing field ("models": null).
	if out.Data.Enabled && out.Data.Models == nil {
		return nil, errOther
	}
	return &out.Data, nil
}

// syncDesktopCatalog fetches the authorized desktop catalog and writes native
// model menus for Claude Desktop or Codex.
// - On transient fetch error: preserves the previously written valid catalog.
// - On explicit withdrawal (cat.Enabled == false) or zero authorized models: removes managed catalog entries.
func (a *App) syncDesktopCatalog(agent, secret string) {
	cat, err := a.fetchDesktopCatalog(secret)
	if err != nil || cat == nil {
		return
	}
	a.mu.Lock()
	if a.catalogs == nil {
		a.catalogs = make(map[string]*DesktopCatalogPayload)
	}
	a.catalogs[agent] = cat
	a.mu.Unlock()
	switch agent {
	case "claude":
		_ = configureDesktopGUIWithCatalog(agent, a.origin, secret, cat)
	case "codex":
		if !cat.Enabled || len(cat.Models) == 0 {
			_ = paths().WriteCodex(core.CodexOpts{
				Origin:       a.origin,
				Secret:       secret,
				Shell:        os.Getenv("SHELL"),
				ClearCatalog: true,
			})
			return
		}
		catalogModels := make([]core.CatalogModel, 0, len(cat.Models))
		allowed := make([]string, 0, len(cat.Models))
		for _, m := range cat.Models {
			catalogModels = append(catalogModels, core.CatalogModel{
				ID:            m.ID,
				DisplayName:   m.DisplayName,
				Vendor:        m.Vendor,
				ContextWindow: m.ContextWindow,
			})
			allowed = append(allowed, m.ID)
		}
		_ = paths().WriteCodex(core.CodexOpts{
			Origin:        a.origin,
			Secret:        secret,
			Model:         cat.DefaultModel,
			Shell:         os.Getenv("SHELL"),
			CatalogModels: catalogModels,
			AllowedModels: allowed,
		})
	}
}

// ensureClaudeOnboarding surgically sets hasCompletedOnboarding=true in ~/.claude.json
// while preserving mcpServers and all existing user fields. Corrupt JSON is never overwritten.
func ensureClaudeOnboarding() error {
	path := claudeJSONPath()
	doc := map[string]any{}
	if raw, err := os.ReadFile(path); err == nil {
		if len(bytes.TrimSpace(raw)) > 0 {
			if err := json.Unmarshal(raw, &doc); err != nil || doc == nil {
				return err
			}
		}
	} else if !os.IsNotExist(err) {
		return err
	}
	if done, ok := doc["hasCompletedOnboarding"].(bool); ok && done {
		return nil
	}
	doc["hasCompletedOnboarding"] = true
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(path, append(out, '\n'))
}

var reCodexConfiguredModel = regexp.MustCompile(`(?m)^\s*model\s*=\s*"([^"]+)"`)

func configuredCodexModel() string {
	raw, err := os.ReadFile(paths().CodexConfig())
	if err != nil {
		return ""
	}
	m := reCodexConfiguredModel.FindSubmatch(raw)
	if len(m) < 2 {
		return ""
	}
	return strings.TrimSpace(string(m[1]))
}

// testChat sends a minimal real inference request (max_tokens=8) to verify the user can
// actually send their first message, not just list /v1/models.
func (a *App) testChat(agent string) (string, time.Duration, error) {
	secret, ok := a.env.Configured(agent, a.origin)
	if !ok || secret == "" {
		return "", 0, errors.New("请先接上这个助手再测试对话")
	}
	model := "claude-sonnet-4-5"
	if agent == "codex" {
		model = configuredCodexModel()
		if model == "" {
			model = core.DefaultCodexModel
		}
	}
	if cat, err := a.fetchDesktopCatalog(secret); err == nil && cat != nil && cat.Enabled {
		if len(cat.Models) == 0 {
			return "", 0, errors.New("当前线路暂无授权模型，请切换线路或联系管理员")
		}
		if cat.DefaultModel != "" {
			model = cat.DefaultModel
		} else if cat.Models[0].ID != "" {
			model = cat.Models[0].ID
		}
	}
	client := a.env.HTTP
	if client == nil {
		client = &http.Client{Timeout: 25 * time.Second}
	} else {
		copied := *client
		if copied.Timeout == 0 || copied.Timeout < 25*time.Second {
			copied.Timeout = 25 * time.Second
		}
		client = &copied
	}
	start := time.Now()
	if agent == "claude" {
		payload, _ := json.Marshal(map[string]any{
			"model":      model,
			"max_tokens": 8,
			"messages": []map[string]string{
				{"role": "user", "content": "Reply OK"},
			},
		})
		req, _ := http.NewRequest(http.MethodPost, a.origin+"/v1/messages", bytes.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("x-api-key", secret)
		req.Header.Set("Authorization", "Bearer "+secret)
		req.Header.Set("anthropic-version", "2023-06-01")
		resp, err := client.Do(req)
		if err != nil {
			return "", 0, errors.New("暂时连不上服务器，请检查网络后再试")
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
		if err := classifyChatProbeFailure(resp.StatusCode, raw); err != nil {
			return "", 0, err
		}
		var out struct {
			Model   string `json:"model"`
			Content []any  `json:"content"`
		}
		if err := json.Unmarshal(raw, &out); err != nil || len(out.Content) == 0 {
			return "", 0, errors.New("服务器返回了异常响应，可切换其他线路后再试")
		}
		if out.Model != "" {
			model = out.Model
		}
		return model, time.Since(start), nil
	}

	// Codex is configured with wire_api = "responses"; probe /v1/responses strictly so
	// test-chat never reports green when the Responses endpoint itself is unavailable.
	responsesPayload, _ := json.Marshal(map[string]any{
		"model":             model,
		"max_output_tokens": 16,
		"input":             "Reply OK",
	})
	req, _ := http.NewRequest(http.MethodPost, a.origin+"/v1/responses", bytes.NewReader(responsesPayload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := client.Do(req)
	if err != nil {
		return "", 0, errors.New("暂时连不上服务器，请检查网络后再试")
	}
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<16))
	statusCode := resp.StatusCode
	resp.Body.Close()
	if statusCode == http.StatusNotFound || statusCode == http.StatusMethodNotAllowed {
		return "", 0, errors.New("当前线路不支持 Codex Responses 接口，请切换其他线路或联系客服")
	}
	if err := classifyChatProbeFailure(statusCode, raw); err != nil {
		return "", 0, err
	}
	var out struct {
		Model      string `json:"model"`
		Output     []any  `json:"output"`
		OutputText string `json:"output_text"`
	}
	if err := json.Unmarshal(raw, &out); err != nil || (len(out.Output) == 0 && strings.TrimSpace(out.OutputText) == "") {
		return "", 0, errors.New("服务器返回了异常响应，可切换其他线路后再试")
	}
	if out.Model != "" {
		model = out.Model
	}
	return model, time.Since(start), nil
}

func classifyChatProbeFailure(status int, raw []byte) error {
	lower := strings.ToLower(string(raw))
	if status == http.StatusUnauthorized {
		return errors.New("连接凭证已失效，请点「一键修复」或重新接上")
	}
	if status == http.StatusPaymentRequired || strings.Contains(lower, "insufficient") || strings.Contains(lower, "quota") || strings.Contains(lower, "余额") {
		return errors.New("账户余额不足，请先点击左下角「去网页充值」")
	}
	if status == http.StatusTooManyRequests || status >= 500 {
		return errors.New("当前线路暂时拥挤或上游异常，可切换到其他线路后再试")
	}
	if status != http.StatusOK {
		return errors.New("对话测试未通过，可切换线路或点击「检查连接」排查")
	}
	var envelope struct {
		Success *bool `json:"success"`
		Error   any   `json:"error"`
	}
	if json.Unmarshal(raw, &envelope) == nil {
		if (envelope.Success != nil && !*envelope.Success) || envelope.Error != nil {
			return errors.New("上游模型暂时拒绝请求，可切换到其他线路后再试")
		}
	}
	return nil
}

// fetchBalance reads the user's balance with the local connection (no login).
type connState int

const (
	connOK connState = iota
	connStopped
	connExpired
	connNetwork
	connDisabled
)

type balanceMeta struct {
	accountLabel string
	workspace    *WorkspaceInfo
	today        *TodayUsageSummary
}

// fetchBalance reads the balance and, with it, whether the web stopped this
// connection (401 + stopped:true → 已停用).
func (a *App) fetchBalance(secret string) (*Balance, *balanceMeta, connState) {
	bal, meta, _, _, cs := a.fetchBalanceAndTier(secret)
	return bal, meta, cs
}

func (a *App) fetchBalanceAndTier(secret string) (*Balance, *balanceMeta, string, []TierOption, connState) {
	req, _ := http.NewRequest(http.MethodGet, a.origin+"/api/install/balance", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return nil, nil, "", nil, connNetwork
	}
	defer resp.Body.Close()
	var out struct {
		Success         bool
		Stopped         bool `json:"stopped"`
		AccountDisabled bool `json:"account_disabled"`
		Message         string
		Data            struct {
			BalanceCNY    json.RawMessage `json:"balance_cny"`
			Low           bool            `json:"balance_low"`
			Tier          string          `json:"tier"`
			Tiers         []TierOption    `json:"tiers"`
			AccountLabel  string          `json:"account_label"`
			Workspace     *struct {
				Kind   string `json:"kind"`
				Label  string `json:"label"`
				TeamID int    `json:"team_id"`
			} `json:"workspace"`
			TodayCostCNY  string `json:"today_cost_cny"`
			TodayRequests int    `json:"today_requests"`
			TodayTokens   int64  `json:"today_tokens"`
		}
	}
	decErr := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out)
	switch {
	case resp.StatusCode == http.StatusUnauthorized && out.Stopped:
		return nil, nil, "", nil, connStopped
	case resp.StatusCode == http.StatusForbidden && out.AccountDisabled:
		return nil, nil, "", nil, connDisabled
	case resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden:
		return nil, nil, "", nil, connExpired
	case resp.StatusCode == http.StatusServiceUnavailable:
		// Hi-fi: keep「能用了」, show soft balance line.
		msg := strings.TrimSpace(out.Message)
		if msg == "" {
			msg = "暂时读不到余额，过一会儿再试。"
		}
		return &Balance{Text: msg}, nil, out.Data.Tier, out.Data.Tiers, connOK
	case resp.StatusCode != http.StatusOK || decErr != nil || !out.Success:
		return nil, nil, "", nil, connNetwork
	}
	meta := &balanceMeta{
		accountLabel: strings.TrimSpace(out.Data.AccountLabel),
	}
	if out.Data.Workspace != nil && strings.TrimSpace(out.Data.Workspace.Label) != "" {
		meta.workspace = &WorkspaceInfo{
			Kind:   strings.TrimSpace(out.Data.Workspace.Kind),
			Label:  strings.TrimSpace(out.Data.Workspace.Label),
			TeamID: out.Data.Workspace.TeamID,
		}
	}
	if strings.TrimSpace(out.Data.TodayCostCNY) != "" || out.Data.TodayRequests > 0 || out.Data.TodayTokens > 0 {
		cost := strings.TrimSpace(out.Data.TodayCostCNY)
		if cost == "" {
			cost = "0.00"
		}
		meta.today = &TodayUsageSummary{
			CostCNY:  cost,
			Requests: out.Data.TodayRequests,
			Tokens:   out.Data.TodayTokens,
		}
	}
	s := strings.Trim(string(out.Data.BalanceCNY), `"`)
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return nil, meta, out.Data.Tier, out.Data.Tiers, connOK
	}
	return balanceView(f, out.Data.Low), meta, out.Data.Tier, out.Data.Tiers, connOK
}

func (a *App) connectedSecret(preferredAgent string) string {
	order := []string{"claude", "codex"}
	if preferredAgent == "codex" {
		order = []string{"codex", "claude"}
	}
	for _, ag := range order {
		if secret, ok := a.env.Configured(ag, a.origin); ok && strings.TrimSpace(secret) != "" {
			return secret
		}
	}
	return ""
}

func normalizeUsageRange(raw string) string {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "7d", "30d", "90d":
		return strings.ToLower(strings.TrimSpace(raw))
	default:
		return "24h"
	}
}

// fetchUsage loads the desktop Usage & Logs dashboard snapshot from /api/install/usage.
// Always uses the same connected wallet that owns balance, workspace, and today summary.
func (a *App) fetchUsage(rangeKey string) (*UsageSnapshot, error) {
	rangeKey = normalizeUsageRange(rangeKey)
	a.mu.Lock()
	walletAgent := a.walletAgent
	a.mu.Unlock()
	secret := a.connectedSecret(walletAgent)
	if secret == "" {
		a.mu.Lock()
		a.usage = nil
		a.mu.Unlock()
		return nil, nil
	}
	req, _ := http.NewRequest(http.MethodGet, a.origin+"/api/install/usage?range="+rangeKey, nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("暂时连不上服务器，没能读取用量记录")
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return nil, errors.New("连接凭证已失效，请回首页重新接上后再看用量")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("暂时读不到用量记录，请稍后再试")
	}
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			Range            string `json:"range"`
			CostCNY          string `json:"cost_cny"`
			Requests         int    `json:"requests"`
			Tokens           int64  `json:"tokens"`
			PromptTokens     int64  `json:"prompt_tokens"`
			CompletionTokens int64  `json:"completion_tokens"`
			Heatmap          []struct {
				Date     string `json:"date"`
				Requests int    `json:"requests"`
				Tokens   int64  `json:"tokens"`
				CostCNY  string `json:"cost_cny"`
			} `json:"heatmap"`
			Logs []struct {
				ID               int    `json:"id"`
				CreatedAt        int64  `json:"created_at"`
				Model            string `json:"model"`
				PromptTokens     int    `json:"prompt_tokens"`
				CompletionTokens int    `json:"completion_tokens"`
				CostCNY          string `json:"cost_cny"`
				UseTimeMs        int    `json:"use_time_ms"`
			} `json:"logs"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&out); err != nil || !out.Success {
		return nil, errors.New("用量数据格式异常，请稍后再试")
	}
	heatmap := make([]UsageHeatmapDay, 0, len(out.Data.Heatmap))
	for _, h := range out.Data.Heatmap {
		heatmap = append(heatmap, UsageHeatmapDay{
			Date:     h.Date,
			Requests: h.Requests,
			Tokens:   h.Tokens,
			CostCNY:  h.CostCNY,
		})
	}
	logs := make([]UsageLogRow, 0, len(out.Data.Logs))
	for _, l := range out.Data.Logs {
		logs = append(logs, UsageLogRow{
			ID:               l.ID,
			CreatedAt:        l.CreatedAt,
			Model:            l.Model,
			PromptTokens:     l.PromptTokens,
			CompletionTokens: l.CompletionTokens,
			CostCNY:          l.CostCNY,
			UseTimeMs:        l.UseTimeMs,
		})
	}
	cost := strings.TrimSpace(out.Data.CostCNY)
	if cost == "" {
		cost = "0.00"
	}
	snap := &UsageSnapshot{
		Range:            normalizeUsageRange(out.Data.Range),
		CostCNY:          cost,
		Requests:         out.Data.Requests,
		Tokens:           out.Data.Tokens,
		PromptTokens:     out.Data.PromptTokens,
		CompletionTokens: out.Data.CompletionTokens,
		Heatmap:          heatmap,
		Logs:             logs,
	}
	a.mu.Lock()
	a.usage = snap
	a.mu.Unlock()
	return snap, nil
}

// switchTier changes the connected assistant's routing tier on the gateway
// without rewriting local config files or restarting the assistant.
func (a *App) switchTier(agent, group string) (string, error) {
	group = strings.TrimSpace(group)
	if !validAgent(agent) || group == "" {
		return "", errors.New("请选择有效的助手和线路")
	}
	secret, ok := a.env.Configured(agent, a.origin)
	if !ok || strings.TrimSpace(secret) == "" {
		return "", errors.New("请先接上 " + agentName(agent) + " 再切换线路")
	}
	payload, _ := json.Marshal(map[string]string{"group": group})
	req, _ := http.NewRequest(http.MethodPut, a.origin+"/api/install/tier", bytes.NewReader(payload))
	req.Header.Set("Authorization", "Bearer "+secret)
	req.Header.Set("Content-Type", "application/json")
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return "", errors.New("没能连上服务器，线路没有改动，请稍后重试")
	}
	defer resp.Body.Close()
	var out struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
		Data    struct {
			Tier  string       `json:"tier"`
			Tiers []TierOption `json:"tiers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil || resp.StatusCode != http.StatusOK || !out.Success {
		if msg := strings.TrimSpace(out.Message); msg != "" && resp.StatusCode < 500 {
			return "", errors.New(msg)
		}
		return "", errors.New("没换成，还是原来的线路")
	}
	label := group
	for _, t := range out.Data.Tiers {
		if t.Name == out.Data.Tier && t.Label != "" {
			label = t.Label
			break
		}
	}
	return label, nil
}

// balanceView: C27 display + C41 prompt. Amount is abs, 2 decimals, ¥; never "¥-X".
// D16: when !hasSupport(), use neutral hints (no 「联系客服」).
func balanceView(f float64, low bool) *Balance {
	c := math.Round(f*100) / 100
	switch {
	case c < 0:
		// W20: one D35 sentence only — no stacked short+long.
		return &Balance{Text: fmt.Sprintf("欠 ¥%.2f，%s", -c, debtPromptTail()), Low: true, Red: true}
	case c == 0:
		hint := "余额用完了，联系客服续上"
		if !hasSupport() {
			hint = "余额用完了，开通续费后再用"
		}
		return &Balance{Text: "余额 ¥0.00", Low: true, Hint: hint, Red: true}
	case c < 10:
		hint := "余额快用完了，联系客服续上"
		if !hasSupport() {
			hint = "余额快用完了，记得续费"
		}
		return &Balance{Text: fmt.Sprintf("余额 ¥%.2f", c), Low: true, Hint: hint}
	}
	return &Balance{Text: fmt.Sprintf("余额 ¥%.2f", c), Low: low}
}

// ---------- the 5-step connect (detect → install → save → connect → check) ----------

type session struct {
	agent, ticket, secret string
}

// restoreConnections trusts the machine state rather than the script's exit
// code: a connection that is still configured means the restore did not work.
func (a *App) restoreConnections() (bool, error) {
	used, err := a.env.Restore(true)
	if err != nil {
		return used, err
	}
	for _, agent := range []string{"claude", "codex"} {
		if _, ok := a.env.Configured(agent, a.origin); ok {
			return used, errors.New(agent + " connection remains after restore")
		}
	}
	return used, nil
}

func working(agent, line string) View {
	return View{Screen: "working", Agent: agent, Name: agentName(agent), Line: line, Busy: true}
}

// connecting is the 3-step loading screen: 装上 → 接上 → 试一下 (UX-WALK #5).
func connecting(agent string, step int) View {
	v := working(agent, "正在帮你接上 "+agentName(agent)+"……")
	v.Screen = "connecting"
	v.Data = map[string]string{"step": strconv.Itoa(step)}
	return v
}

func (a *App) connect(agent, ticket string) {
	name := agentName(agent)
	_ = name
	a.setView(connecting(agent, 1))
	// Every new claim starts clean: a lost claim response must not let retry reuse
	// the previous key that the server may already have replaced.
	a.mu.Lock()
	a.current = session{}
	a.mu.Unlock()
	secret, err := a.claim(ticket)
	if err != nil {
		a.fail(agent, err, "claim-failed", ticket)
		return
	}
	a.mu.Lock()
	a.current = session{agent: agent, ticket: ticket, secret: secret}
	a.mu.Unlock()
	a.progress(ticket, 1, "running", "")
	a.progress(ticket, 2, "running", "")
	a.finish(agent, true)
}

// finish runs install(if needed) → write → check for the a.current session.
func (a *App) finish(agent string, write bool) {
	a.mu.Lock()
	s := a.current
	a.mu.Unlock()
	cli := "present"
	a.setView(connecting(agent, 1))
	if !a.env.FindCLI(agent) {
		var installErr error
		if a.desktop {
			installErr = a.waitForCLIInstall(agent)
			if installErr == nil && !a.env.FindCLI(agent) {
				installErr = a.installDesktopComponent(agent, "cli")
			}
			if installErr != nil {
				a.setView(desktopInstallFailure(agent, "cli", installErr))
				return
			}
		} else {
			installErr = a.env.InstallCLI(agent)
		}
		if installErr != nil || !a.env.FindCLI(agent) {
			cli = "failed"
		} else {
			cli = "installed"
		}
	}
	a.setView(connecting(agent, 2))
	if write {
		a.progress(s.ticket, 3, "running", "")
		if err := a.env.Write(agent, a.origin, s.secret); err != nil {
			a.fail(agent, errOther, "config-write-failed", s.ticket)
			return
		}
		if agent == "claude" {
			// Skip Claude Code's first-run official OAuth onboarding wall on fresh machines.
			_ = ensureClaudeOnboarding()
		}
	}
	if cli == "failed" {
		a.fail(agent, errCLI, "cli-install-failed", s.ticket)
		return
	}
	a.setView(connecting(agent, 3))
	a.progress(s.ticket, 4, "running", "")
	if err := a.check(s.secret); err != nil {
		a.fail(agent, err, "connection-test-failed", s.ticket)
		return
	}
	if a.env.WriteStamp != nil {
		_ = a.env.WriteStamp(agent, cli)
	}
	a.progress(s.ticket, 5, "done", "")
	a.mu.Lock()
	a.last.check, a.last.reason = "能用了", ""
	a.mu.Unlock()
	if !a.desktop {
		go a.refreshStatus(true)
	}
	if a.env.OfficialLogin(agent) {
		a.setView(errorView("conflict", agent))
		return
	}
	a.setView(successView(agent, a.env.Running(agent)))
}

var errCLI = errors.New("cli")

func (a *App) fail(agent string, err error, code, ticket string) {
	kind := "other"
	switch {
	case errors.Is(err, errExpired):
		kind = "expired"
	case errors.Is(err, errNetwork):
		kind = "network"
	case errors.Is(err, errCLI):
		kind = "cli"
	}
	a.progress(ticket, -1, "failed", code)
	a.mu.Lock()
	a.retries[kind]++
	n := a.retries[kind]
	a.last.check = map[string]string{"expired": "连接过期了", "network": "连不上", "cli": "没装上", "other": "没接好"}[kind]
	a.last.reason = code
	if kind == "expired" {
		a.ticket = ""
	}
	a.mu.Unlock()
	if kind == "network" && n >= 2 {
		kind = "network-again"
	}
	a.setView(errorView(kind, agent))
	if !a.desktop {
		go a.refreshStatus(false)
	}
}

// ---------- screens (copy: docs/CLIENT-ZERO-BASE-COPY.md, verbatim) ----------

// otherAgent supports F5 serial 「再配另一个」.
func otherAgent(agent string) string {
	if agent == "codex" {
		return "claude"
	}
	return "codex"
}

// successView: ZERO-BASE ⑤, plus 「再配 {另一个}」 for the other assistant.
func successView(agent string, running bool) View {
	n := agentName(agent)
	other := otherAgent(agent)
	data := map[string]string{"other": other}
	v := View{
		Screen: "success", Agent: agent, Name: n,
		Second: "再配 " + agentName(other), Second2: "connect",
		Data: data,
	}
	if running {
		v.Line = n + " 还开着，要用新的连接得先关掉它。"
		v.Button, v.Action = "帮我关掉再打开", "restart"
		v.Note = "如果你在别的软件里用着 " + n + "，那里的对话会断开，软件本身不会关。"
		return v
	}
	// UX-WALK #9/#14: not running → just 「能用了」 + a first sentence to try; no 「关掉再打开」.
	v.Line = "先试一句：复制下面这句话，在 " + n + " 里发出去。"
	v.Button, v.Action = "复制，打开 "+n, "launch-try"
	v.Note = "会弹出一个新窗口，粘贴进去发出就行。"
	data["try"] = shared.TryFirst
	return v
}

func errorView(kind, agent string) View {
	n := agentName(agent)
	v := View{Screen: "error", Agent: agent, Name: n, Data: map[string]string{"kind": kind}}
	switch kind {
	case "expired":
		v.Line, v.Button, v.Action = "这个连接过期了，需要重新接一次。", "重新接上", "connect"
	case "network":
		v.Line, v.Button, v.Action = "现在连不上，可能是网络不太好。", "再试一次", "retry"
	case "conflict":
		v.Line, v.Button, v.Action = n+" 里还登着另一个账号，挡住了我们的连接。", "一键修好", "fix-login"
	case "stale":
		// ZERO-BASE / ACCEPT §3.3
		v.Line, v.Button, v.Action = n+" 还在用之前的设置。", "一键修好", "fix-stale"
	case "noticket":
		// F4 / ACCEPT：主路径回网页，不强调复制命令
		v.Line, v.Button, v.Action = "没收到网页发来的连接。", "回网页再点一次", "open-web"
	case "reauth":
		v.Line, v.Button, v.Action = "需要打开网页，再点一次接上。", "回网页继续", "open-web"
	case "network-again": // D31 / W16: primary stays 「再试一次」
		v.Line, v.Button, v.Action = "还是连不上。换个网络，或过会儿再试一次。", "再试一次", "retry"
	case "cli":
		v.Screen = "cli-missing"
		v.Line, v.Button, v.Action = "没能自动装上。重新打开接上AI，再试一次。", "再试一次", "install-cli" // D31/W16
	default:
		v.Line, v.Button, v.Action = "这次没接好，不是你的问题。", "再试一次", "retry" // W16
	}
	return v
}

// ---------- actions ----------

func (a *App) act(action, agent string) {
	if !validAgent(agent) {
		agent = a.getView().Agent
		if !validAgent(agent) {
			agent = "claude"
		}
	}
	if a.getView().Busy && action != "noop" {
		return
	}
	n := agentName(agent)
	switch action {
	case "home", "done":
		a.setView(View{Screen: "home"})
		if !a.desktop {
			go a.refreshStatus(true)
		}
	case "web-continue", "open-web":
		// F4: deep-link / no-ticket → back to web (not copy-command main path)
		_ = a.env.Open(a.web() + "/download?agent=" + agent + "&continue=1")
		a.setView(View{Screen: "home"})
	case "web-connect", "connect":
		a.mu.Lock()
		if a.view.Busy {
			a.mu.Unlock()
			return
		}
		if a.clientAuthLocked() == "" {
			a.mu.Unlock()
			a.setView(errorView("reauth", agent))
			return
		}
		a.view = connecting(agent, 1)
		a.mu.Unlock()
		go a.connectFromAuth(agent)
	case "recharge":
		_ = a.env.Open(a.web() + "/account")
	case "spending": // D32: 「看花费明细」 under the balance
		_ = a.env.Open(a.web() + "/spending")
	case "refresh-balance":
		if !a.desktop {
			go a.refreshStatus(true)
		}
	case "support":
		if !hasSupport() { // D30: contact page unreachable until real contact info exists
			return
		}
		a.setView(View{Screen: "support", Agent: agent, Line: "加上后，可以把出问题的情况一起发过去，里面没有你的密码和账户信息。",
			Button: "复制 QQ 号", Action: "__copyqq", Data: map[string]string{"qq": supportQQ(), "line1": supportLine1(), "line2": shared.Support.Lines[1], "report": a.supportReport()}})
	case "retry":
		a.mu.Lock()
		s := a.current
		ticket := a.ticket
		a.mu.Unlock()
		if s.secret != "" && s.agent == agent {
			a.setView(connecting(agent, 1))
			go a.finish(agent, true)
		} else if a.hasClientAuth() {
			a.setView(connecting(agent, 1))
			go a.connectFromAuth(agent)
		} else if ticket != "" {
			go a.connect(agent, ticket)
		} else {
			a.setView(errorView("reauth", agent))
		}
	case "fix":
		go a.recheck(agent)
	case "launch", "launch-try": // launch-try: the page already copied the 先试一句 sentence
		_ = a.env.Launch(agent, a.secretFor(agent))
		a.setView(View{Screen: "home"})
		if !a.desktop {
			go a.refreshStatus(false)
		}
	case "restart", "fix-stale":
		a.setView(working(agent, "正在关掉 "+n+"，马上帮你重新打开……"))
		go func() {
			_ = a.env.Kill(agent)
			time.Sleep(800 * time.Millisecond)
			if a.env.Running(agent) {
				a.setView(errorView("other", agent))
				return
			}
			if action == "fix-stale" {
				a.recheck(agent)
				return
			}
			_ = a.env.Launch(agent, a.secretFor(agent))
			a.setView(View{Screen: "home"})
			a.refreshStatus(false)
		}()
	case "fix-login":
		a.setView(View{Screen: "confirm", Agent: agent, Name: n,
			Line:   "这会退出你在 " + n + " 里登录的官方账号，之后随时可以登回去。你的官方订阅不受影响。",
			Extra:  "以后想换回官方账号，在接上AI 点「恢复成原来的样子」就行。",
			Button: "好，帮我修好", Action: "logout-yes", Second: "先不", Second2: "logout-no"})
	case "logout-yes":
		a.setView(working(agent, "正在帮你接上 "+n+"……"))
		go func() {
			if err := a.env.Logout(agent); err != nil || a.env.OfficialLogin(agent) {
				a.setView(errorView("other", agent))
				return
			}
			a.recheck(agent)
		}()
	case "logout-no":
		a.setView(View{Screen: "error", Agent: agent, Name: n, Line: "好的，没有改动。要用便宜的模型，需要先退出那个账号。",
			Button: supportButton("知道了"), Action: supportAction("home")})
	case "install-cli":
		a.setView(View{Screen: "cli-missing", Agent: agent, Name: n, Line: "这台电脑还没有 " + n + "，我们帮你装上。",
			Button: "帮我装上", Action: "install-cli-run",
			Note: "电脑可能会问你开机密码，输入就行。"})
	case "install-cli-run":
		a.setView(working(agent, "正在帮你装上 "+n+"……"))
		go func() {
			if err := a.env.InstallCLI(agent); err != nil || !a.env.FindCLI(agent) {
				a.setView(errorView("cli", agent))
				return
			}
			a.setView(View{Screen: "home"})
			a.refreshStatus(true)
		}()
	case "restore":
		v := View{Screen: "confirm", Agent: agent, Name: n,
			Line:   n + " 会恢复成接上之前的样子，你用它做出来的东西不会变。",
			Button: "恢复", Action: "restore-yes", Second: "先不", Second2: "home",
			// F1 / M5：本机恢复 vs 网上作废回账户
			Note: "只清这台电脑上的连接。想在网上作废，请回「我的账户」停用。"}
		if a.env.Edited() {
			v.Extra = "你后来自己改过的地方，也会一起恢复。"
		}
		a.setView(v)
	case "restore-yes":
		a.setView(working(agent, "正在恢复……"))
		go func() {
			used, err := a.restoreConnections()
			if err != nil {
				a.setView(errorView("other", agent))
				return
			}
			line := "已经恢复好了。"
			if !used {
				line = "已去掉我们加的设置。如果你以前自己选过模型，需要重新选一次。"
			}
			a.mu.Lock()
			a.current = session{}
			a.balance = nil
			a.mu.Unlock()
			a.setView(View{Screen: "done", Line: line, Button: "好", Action: "done"})
			a.refreshStatus(false)
		}()
	case "open-dir": // staff only
		_ = a.env.OpenDir(agent)
	case "open-dir-site":
		_ = a.env.OpenDir("jieshangai")
	}
}

func (a *App) web() string {
	if a.env.Web != "" {
		return a.env.Web
	}
	return a.origin
}

func (a *App) secretFor(agent string) string {
	if s, ok := a.env.Configured(agent, a.origin); ok {
		return s
	}
	return ""
}

// recheck re-tests an existing local connection and lands on success or one error.
func (a *App) recheck(agent string) {
	n := agentName(agent)
	a.setView(working(agent, "正在帮你接上 "+n+"……"))
	secret, ok := a.env.Configured(agent, a.origin)
	if !ok {
		a.setView(errorView("noticket", agent))
		return
	}
	a.mu.Lock()
	if a.current.agent != agent || a.current.secret == "" {
		a.current = session{agent: agent, secret: secret}
	}
	a.mu.Unlock()
	if err := a.check(secret); err != nil {
		a.fail(agent, err, "connection-test-failed", "")
		return
	}
	a.mu.Lock()
	a.last.check, a.last.reason = "能用了", ""
	a.mu.Unlock()
	if !a.desktop {
		go a.refreshStatus(true)
	}
	if a.env.OfficialLogin(agent) {
		a.setView(errorView("conflict", agent))
		return
	}
	a.setView(successView(agent, a.env.Running(agent)))
}

// fetchSiteStatus reads GET /api/status and applies relay_support_email (U2).
// On failure or a missing field, keep the previous / build-time fallback.
func (a *App) fetchSiteStatus() {
	req, err := http.NewRequest(http.MethodGet, a.origin+"/api/status", nil)
	if err != nil {
		return
	}
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return
	}
	var out struct {
		Success bool `json:"success"`
		Data    struct {
			RelaySupportEmail *string `json:"relay_support_email"`
		} `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil || !out.Success {
		return
	}
	if out.Data.RelaySupportEmail == nil {
		return
	}
	setRuntimeSupportEmail(*out.Data.RelaySupportEmail)
}

// Badge wording shared by every refresh path, so an unchanged state never
// changes its text or width. The card title already names the assistant.
const (
	badgeNoCLI       = "还没安装"
	badgeUnconnected = "还没接"
)

// refreshStatus computes the four badges (M3) and, optionally, the balance.
// Also refreshes runtime support email so admin RelaySupportEmail takes effect
// without a client rebuild (U2: open + manual refresh).
func (a *App) refreshStatus(withBalance bool) {
	a.fetchSiteStatus()
	var out []AgentStatus
	var bal *Balance
	var meta *balanceMeta
	var walletAgent string
	expired := ""
	for _, ag := range []string{"claude", "codex"} {
		n := agentName(ag)
		st := AgentStatus{Agent: ag, Name: n}
		secret, configured := a.env.Configured(ag, a.origin)
		st.Installed, st.Configured, st.Running = a.env.FindCLI(ag), configured, a.env.Running(ag)
		switch {
		case !a.env.FindCLI(ag) && !configured:
			st.State, st.Badge, st.Button, st.Action = "nocli", badgeNoCLI, "帮我装上", "install-cli"
		case !configured:
			st.State, st.Badge, st.Button, st.Action = "none", badgeUnconnected, "帮我接上 "+n, "connect"
		case !a.env.FindCLI(ag):
			st.State, st.Badge, st.Button, st.Action = "nocli", badgeNoCLI, "帮我装上", "install-cli"
		default:
			// one call per assistant: balance + tier + stopped/expired (boss: on open + manual refresh)
			b, m, tier, tiers, cs := a.fetchBalanceAndTier(secret)
			switch cs {
			case connStopped:
				st.State, st.Badge, st.Button, st.Action = "stopped", "已停用", "重新接上 "+n, "connect"
			case connDisabled:
				st.State, st.Badge, st.Button, st.Action = "disabled", "不能用", "现在用不了", "noop"
				st.Note = "这个账户已停用，助手暂时用不了。"
			case connOK:
				st.State, st.Badge, st.Button, st.Action = "ok", "能用了", "恢复成原来的样子", "restore"
				st.Tier, st.Tiers = tier, tiers
				// Bind balance, workspace, accountLabel, today summary, and usage logs
				// atomically to the same single selected wallet connection.
				if walletAgent == "" {
					walletAgent = ag
					if withBalance {
						bal = b
						meta = m
					}
				}
			case connExpired: // D36: 401 → straight to error-expired (button 「重新接上」)
				st.State, st.Badge, st.Button, st.Action = "broken", "暂时用不了", "重新接上", "connect"
				if a.desktop {
					st.State = "expired"
				}
				expired = ag
			default:
				st.State, st.Badge, st.Button, st.Action = "broken", "暂时用不了", "一键修好", "fix"
				if a.desktop {
					st.State = "offline"
				}
			}
		}
		out = append(out, st)
	}
	// After a restore no assistant holds a key; the desktop authorization still
	// reads the account balance, so the home screen keeps showing it.
	if withBalance && walletAgent == "" && expired == "" {
		configured := false
		for _, st := range out {
			configured = configured || st.Configured
		}
		if auth := a.clientAuth(); auth != "" && !configured {
			if b, m, cs := a.fetchBalance(auth); cs == connOK {
				bal = b
				meta = m
			}
		}
	}
	a.mu.Lock()
	a.statuses = out
	a.walletAgent = walletAgent
	if withBalance {
		a.balance = bal
		if meta != nil {
			a.accountLabel = meta.accountLabel
			a.workspace = meta.workspace
			a.today = meta.today
		} else {
			a.accountLabel = ""
			a.workspace = nil
			a.today = nil
		}
	}
	a.mu.Unlock()
	// The desktop card already says "需要重新接上"; only the legacy client opens a dialog.
	if expired != "" && !a.desktop && a.getView().Screen == "home" {
		a.setView(errorView("expired", expired))
	}
}

// ---------- support (memo items 8/9) ----------

func (a *App) diagLocked() []string {
	check := a.last.check
	if check == "" {
		check = "还没检查"
	}
	d := []string{"电脑：" + a.env.OSLabel, "软件：" + a.appVersion, "上次检查：" + check}
	if a.last.reason != "" {
		d = append(d, "出错原因："+reasonText(a.last.reason))
	}
	for _, s := range a.statuses {
		d = append(d, s.Name+"："+s.Badge)
	}
	return d
}

func reasonText(code string) string {
	return map[string]string{
		"claim-failed":           "没拿到网页发来的连接",
		"config-write-failed":    "没能写好设置",
		"cli-install-failed":     "没能自动装上助手",
		"connection-test-failed": "接好后检查没通过",
	}[code]
}

// supportReport is the text the user pastes into QQ (no password, no key).
func (a *App) supportReport() string {
	a.mu.Lock()
	defer a.mu.Unlock()
	report := "你好，我在用「接上AI」时遇到了问题。\n\n" + strings.Join(a.diagLocked(), "\n")
	if a.env.InstallDiagnostics != nil {
		report += "\n\n安装诊断（不含密钥）：\n" + a.env.InstallDiagnostics("claude") + "\n" + a.env.InstallDiagnostics("codex")
	}
	return report
}

// Staff-only commands (M10): same shape as the web copy / INSTALL-COMMANDS.md.
func installCommand(goos, origin, agent string) string {
	if goos == "windows" {
		return "irm " + origin + "/i/{在网页复制} | iex"
	}
	return "curl -fsSL " + origin + "/i/{在网页复制} | bash"
}
