package clientruntime

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strings"
)

const maxRemoteSkillZipBytes = 20 << 20 // 20 MiB hard ceiling matching gateway

var reRemoteSkillSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9_-]{1,62}$`)

// RemoteSkillCategory mirrors gateway's public skill category descriptor.
type RemoteSkillCategory struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

// RemoteSkillEntitlement reports whether the connected account has unlocked premium skills.
type RemoteSkillEntitlement struct {
	Authenticated   bool  `json:"authenticated"`
	PaidFen         int64 `json:"paid_fen"`
	ThresholdFen    int64 `json:"threshold_fen"`
	IsTeamMember    bool  `json:"is_team_member"`
	PremiumUnlocked bool  `json:"premium_unlocked"`
}

// RemoteSkillItem is a skill entry fetched from the official gateway catalog.
type RemoteSkillItem struct {
	ID                int      `json:"id"`
	Slug              string   `json:"slug"`
	Title             string   `json:"title"`
	Category          string   `json:"category"`
	Tier              string   `json:"tier"`
	Summary           string   `json:"summary"`
	SupportedAgents   []string `json:"supported_agents"`
	InstallCommand    string   `json:"install_command"`
	HasInstallCommand bool     `json:"has_install_command"`
	HasZip            bool     `json:"has_zip"`
	ZipSizeBytes      int64    `json:"zip_size_bytes"`
	ZipSHA256         string   `json:"zip_sha256"`
	HasPreset         bool     `json:"has_preset"`
	Unlocked          bool     `json:"unlocked"`
	InstallCount      int64    `json:"install_count"`
	SortOrder         int      `json:"sort_order"`
	CreatedAt         int64    `json:"created_at"`
	UpdatedAt         int64    `json:"updated_at"`
}

// RemoteSkillsCatalog is cached on DesktopState for the Desktop Skills page.
type RemoteSkillsCatalog struct {
	Categories  []RemoteSkillCategory  `json:"categories"`
	Items       []RemoteSkillItem      `json:"items"`
	Entitlement RemoteSkillEntitlement `json:"entitlement"`
}

func (a *App) desktopSkillSecret(preferredAgent string) string {
	candidates := []string{"claude", "codex"}
	if validAgent(preferredAgent) {
		if preferredAgent == "codex" {
			candidates = []string{"codex", "claude"}
		}
	}
	for _, ag := range candidates {
		if secret, ok := a.env.Configured(ag, a.origin); ok && strings.TrimSpace(secret) != "" {
			return secret
		}
	}
	return ""
}

func (a *App) loadReservedRemoteSkillsCatalog(epoch, seq uint64, preferredAgent string) error {
	a.connectionMu.Lock()
	a.mu.Lock()
	stale := epoch != a.walletEpoch
	a.mu.Unlock()
	secret := ""
	if !stale {
		secret = a.desktopSkillSecret(preferredAgent)
	}
	a.connectionMu.Unlock()
	if stale {
		return nil
	}
	if secret == "" {
		a.mu.Lock()
		if epoch == a.walletEpoch && seq == a.skillCatalogReq {
			a.skillCatalog = nil
		}
		a.mu.Unlock()
		return errors.New("请先接上 Claude Code 或 Codex 后再查看官方 Skill 广场")
	}

	cat, err := a.fetchRemoteSkillsCatalog(secret)
	a.connectionMu.Lock()
	defer a.connectionMu.Unlock()
	currentSecret := a.desktopSkillSecret(preferredAgent)
	a.mu.Lock()
	defer a.mu.Unlock()
	if epoch != a.walletEpoch || seq != a.skillCatalogReq || currentSecret != secret {
		return nil
	}
	if err != nil {
		return err
	}
	a.skillCatalog = cat
	return nil
}

func (a *App) fetchRemoteSkillsCatalog(secret string) (*RemoteSkillsCatalog, error) {
	req, err := http.NewRequest(http.MethodGet, a.origin+"/api/install/skills", nil)
	if err != nil {
		return nil, errors.New("无法创建请求")
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("暂时连不上服务器，请稍后再试")
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusUnauthorized {
		return nil, errors.New("连接凭证已失效，请回首页重新接上后再试")
	}
	if resp.StatusCode != http.StatusOK {
		return nil, errors.New("暂时读不到官方 Skill 广场，请稍后再试")
	}
	var out struct {
		Success bool                `json:"success"`
		Message string              `json:"message"`
		Data    RemoteSkillsCatalog `json:"data"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 2<<20)).Decode(&out); err != nil || !out.Success {
		if msg := strings.TrimSpace(out.Message); msg != "" && len(msg) <= 120 {
			return nil, errors.New(msg)
		}
		return nil, errors.New("官方 Skill 广场返回格式异常，请稍后再试")
	}
	if out.Data.Categories == nil {
		out.Data.Categories = []RemoteSkillCategory{}
	}
	if out.Data.Items == nil {
		out.Data.Items = []RemoteSkillItem{}
	}
	return &out.Data, nil
}

func (a *App) installRemoteSkill(agent, slug string) ([]Skill, error) {
	if !validAgent(agent) {
		return nil, errors.New("请选择 Claude Code 或 Codex")
	}
	slug = strings.TrimSpace(strings.ToLower(slug))
	if !reRemoteSkillSlug.MatchString(slug) {
		return nil, errors.New("Skill 标识无效，已拒绝安装")
	}
	secret := a.desktopSkillSecret(agent)
	if secret == "" {
		return nil, errors.New("请先接上 Claude Code 或 Codex 后再一键安装")
	}

	req, err := http.NewRequest(http.MethodGet, a.origin+"/api/install/skills/"+slug+"/download", nil)
	if err != nil {
		return nil, errors.New("无法创建下载请求")
	}
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := a.env.HTTP.Do(req)
	if err != nil {
		return nil, errors.New("下载 Skill 安装包失败，请检查网络后重试")
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		var apiErr struct {
			Code    string `json:"code"`
			Message string `json:"message"`
		}
		_ = json.NewDecoder(io.LimitReader(resp.Body, 8192)).Decode(&apiErr)
		switch resp.StatusCode {
		case http.StatusUnauthorized:
			return nil, errors.New("连接凭证已失效，请回首页重新接上后再试")
		case http.StatusForbidden:
			if apiErr.Code == "SKILL_PREMIUM_LOCKED" {
				return nil, errors.New("该精品 Skill 需历史累计充值满 ¥30 或加入有效团队后解锁")
			}
			if msg := strings.TrimSpace(apiErr.Message); msg != "" && len(msg) <= 120 {
				return nil, errors.New(msg)
			}
			return nil, errors.New("暂无权限安装该 Skill")
		case http.StatusNotFound:
			return nil, errors.New("该 Skill 暂未提供一键安装包")
		default:
			return nil, fmt.Errorf("下载 Skill 安装包失败（状态 %d），请稍后再试", resp.StatusCode)
		}
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxRemoteSkillZipBytes+1))
	if err != nil {
		return nil, errors.New("读取安装包数据失败，请重试")
	}
	if len(data) == 0 {
		return nil, errors.New("下载到的安装包为空，请重试")
	}
	if len(data) > maxRemoteSkillZipBytes {
		return nil, errors.New("安装包超过 20 MB 上限，已拒绝安装")
	}

	expectedSHA := strings.TrimSpace(strings.ToLower(resp.Header.Get("X-Content-SHA256")))
	if expectedSHA != "" {
		sum := sha256.Sum256(data)
		actualSHA := hex.EncodeToString(sum[:])
		if actualSHA != expectedSHA {
			return nil, errors.New("安装包校验失败（SHA-256 不一致），请重新尝试")
		}
	}

	tmpFile, err := os.CreateTemp("", "jieshangai-skill-*.zip")
	if err != nil {
		return nil, errors.New("无法创建临时文件以安装 Skill")
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		return nil, errors.New("写入临时安装包失败")
	}
	if err := tmpFile.Close(); err != nil {
		return nil, errors.New("保存临时安装包失败")
	}

	return ImportSkillZipWithName(agent, tmpPath, slug)
}
