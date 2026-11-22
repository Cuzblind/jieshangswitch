package clientruntime

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

const desktopProfileID = "00000000-0000-4000-8000-0000005a1e00"

var guiConfigMu sync.Mutex

type guiFileBackup struct {
	Existed bool   `json:"existed"`
	Data    []byte `json:"data,omitempty"`
	Applied []byte `json:"applied,omitempty"`
}

func desktopGUIFiles() []string {
	normal, third := guiConfigDirs()
	lib := filepath.Join(third, "configLibrary")
	return []string{filepath.Join(normal, "claude_desktop_config.json"), filepath.Join(third, "claude_desktop_config.json"), filepath.Join(lib, desktopProfileID+".json"), filepath.Join(lib, "_meta.json")}
}
func guiBackupPath() string { return filepath.Join(paths().Dir(), "desktop-gui-backup.json") }
func readGUIBackups() (map[string]guiFileBackup, error) {
	out := map[string]guiFileBackup{}
	b, e := os.ReadFile(guiBackupPath())
	if os.IsNotExist(e) {
		return out, nil
	}
	if e != nil {
		return out, e
	}
	if e = json.Unmarshal(b, &out); e != nil {
		return out, errors.New("桌面版备份文件无法读取，请联系客服")
	}
	allowed := map[string]bool{}
	for _, p := range desktopGUIFiles() {
		allowed[p] = true
	}
	for p := range out {
		if !allowed[p] {
			return nil, errors.New("桌面版备份包含无效路径")
		}
	}
	return out, nil
}
func readJSONObject(path string) (map[string]any, error) {
	v := map[string]any{}
	b, e := os.ReadFile(path)
	if os.IsNotExist(e) {
		return v, nil
	}
	if e != nil {
		return nil, e
	}
	if json.Unmarshal(b, &v) != nil || v == nil {
		return nil, errors.New("桌面版现有配置无法读取，请先检查配置")
	}
	return v, nil
}
type DesktopCatalogModel struct {
	ID            string `json:"id"`
	DisplayName   string `json:"display_name,omitempty"`
	Vendor        string `json:"vendor,omitempty"`
	ContextWindow int    `json:"context_window,omitempty"`
}

type DesktopCatalogPayload struct {
	Enabled      bool                  `json:"enabled"`
	Version      int64                 `json:"version"`
	Agent        string                `json:"agent"`
	Tier         string                `json:"tier"`
	DefaultModel string                `json:"default_model"`
	Models       []DesktopCatalogModel `json:"models"`
}

func configureDesktopGUI(agent, origin, secret string) error {
	return configureDesktopGUIWithCatalog(agent, origin, secret, nil)
}

func configureDesktopGUIWithCatalog(agent, origin, secret string, cat *DesktopCatalogPayload) error {
	if !validAgent(agent) || secret == "" || (origin != ProductionOrigin && origin != TestOrigin) {
		return errors.New("请先接上这个助手")
	}
	if agent == "codex" {
		return nil
	} // Same user config.toml as CLI; its key comes from the persisted user env variable install.ps1 writes (Set-CodexProcessEnv), which shell-activated GUI processes also inherit.
	guiConfigMu.Lock()
	defer guiConfigMu.Unlock()
	if guiManagedElsewhere() {
		return errors.New("桌面版已由管理员配置，接上AI不会覆盖系统策略")
	}
	files := desktopGUIFiles()
	changes := map[string][]byte{}
	for i, p := range files {
		v, e := readJSONObject(p)
		if e != nil {
			return e
		}
		switch i {
		case 0, 1:
			v["deploymentMode"] = "3p"
		case 2:
			v["inferenceProvider"] = "gateway"
			v["inferenceGatewayBaseUrl"] = origin
			v["inferenceGatewayApiKey"] = secret
			v["inferenceGatewayAuthScheme"] = "bearer"
			if cat != nil {
				delete(v, "defaultModel")
				if !cat.Enabled {
					delete(v, "inferenceModels")
				} else if len(cat.Models) > 0 {
					// Official Claude Desktop 3p gateway schema:
					// inferenceModels entries use { name, labelOverride } and the first item is the default model.
					ordered := make([]DesktopCatalogModel, 0, len(cat.Models))
					for _, m := range cat.Models {
						if cat.DefaultModel != "" && m.ID == cat.DefaultModel {
							ordered = append(ordered, m)
						}
					}
					for _, m := range cat.Models {
						if cat.DefaultModel == "" || m.ID != cat.DefaultModel {
							ordered = append(ordered, m)
						}
					}
					models := make([]map[string]any, 0, len(ordered))
					for _, m := range ordered {
						item := map[string]any{
							"name": m.ID,
						}
						if label := m.DisplayName; label != "" && label != m.ID {
							item["labelOverride"] = label
						}
						models = append(models, item)
					}
					v["inferenceModels"] = models
				}
			}
		case 3:
			entries := []any{}
			if value, ok := v["entries"]; ok {
				var good bool
				entries, good = value.([]any)
				if !good {
					return errors.New("桌面版配置列表格式无效")
				}
			}
			found := false
			for _, entry := range entries {
				if row, ok := entry.(map[string]any); ok && row["id"] == desktopProfileID {
					row["name"] = "接上AI"
					found = true
				}
			}
			if !found {
				entries = append(entries, map[string]string{"id": desktopProfileID, "name": "接上AI"})
			}
			v["entries"] = entries
			v["appliedId"] = desktopProfileID
		}
		b, _ := json.MarshalIndent(v, "", "  ")
		changes[p] = append(b, '\n')
	}
	// Don't rewrite a live desktop configuration underneath an active task.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	info, _ := inspectGUI(ctx, agent)
	if info.Path != "" && guiIsRunning(info) {
		same := true
		for p, b := range changes {
			before, _ := os.ReadFile(p)
			var x, y any
			_ = json.Unmarshal(before, &x)
			_ = json.Unmarshal(b, &y)
			a, _ := json.Marshal(x)
			c, _ := json.Marshal(y)
			if string(a) != string(c) {
				same = false
			}
		}
		if !same {
			return errors.New("请先保存任务并退出 Claude 桌面版，再打开以应用本站连接")
		}
		return nil
	}
	backups, e := readGUIBackups()
	if e != nil {
		return e
	}
	previous := map[string]guiFileBackup{}
	for _, p := range files {
		b, e := os.ReadFile(p)
		if e != nil && !os.IsNotExist(e) {
			return e
		}
		snapshot := guiFileBackup{Existed: e == nil, Data: b}
		previous[p] = snapshot
		if _, ok := backups[p]; !ok {
			backups[p] = snapshot
		}
		saved := backups[p]
		saved.Applied = changes[p]
		backups[p] = saved
	}
	if e = os.MkdirAll(filepath.Dir(guiBackupPath()), 0700); e != nil {
		return e
	}
	b, _ := json.Marshal(backups)
	if e = writePrivateFile(guiBackupPath(), b); e != nil {
		return e
	}
	for _, p := range files {
		if e = os.MkdirAll(filepath.Dir(p), 0700); e == nil {
			e = writePrivateFile(p, changes[p])
		}
		if e != nil {
			for path, old := range previous {
				if old.Existed {
					_ = writePrivateFile(path, old.Data)
				} else {
					_ = os.Remove(path)
				}
			}
			return errors.New("桌面版接入设置未写完，已尝试恢复原配置")
		}
	}
	return nil
}
func desktopGUIEdited() bool {
	guiConfigMu.Lock()
	defer guiConfigMu.Unlock()
	backups, e := readGUIBackups()
	if e != nil {
		return true
	}
	for p, b := range backups {
		current, e := os.ReadFile(p)
		if e != nil || string(current) != string(b.Applied) {
			return true
		}
	}
	return false
}
func restoreDesktopGUI() error {
	guiConfigMu.Lock()
	defer guiConfigMu.Unlock()
	backups, e := readGUIBackups()
	if e != nil {
		return e
	}
	for p, b := range backups {
		if b.Existed {
			if e = writePrivateFile(p, b.Data); e != nil {
				return e
			}
		} else {
			if e = os.Remove(p); e != nil && !os.IsNotExist(e) {
				return e
			}
		}
	}
	if e = os.Remove(guiBackupPath()); e != nil && !os.IsNotExist(e) {
		return e
	}
	return nil
}
