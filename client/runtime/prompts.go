package clientruntime

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Prompt management is local only: prompts live in ~/.jieshangai/prompts and
// the enabled one is projected into the assistant's own instruction file.
// An existing hand-written file is never overwritten without being saved first.

const (
	maxPromptName    = 80
	maxPromptContent = 32 * 1024
)

type Prompt struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Content   string `json:"content"`
	Enabled   bool   `json:"enabled"`
	UpdatedAt string `json:"updatedAt"`
}

var promptMu sync.Mutex

func promptTarget(agent string) (string, error) {
	switch agent {
	case "claude":
		return filepath.Join(homeDir(), ".claude", "CLAUDE.md"), nil
	case "codex":
		return filepath.Join(homeDir(), ".codex", "AGENTS.md"), nil
	}
	return "", errors.New("请选择 Claude Code 或 Codex")
}

func promptStore(agent string) string {
	return filepath.Join(homeDir(), ".jieshangai", "prompts", agent+".json")
}

func newPromptID(prefix string) string {
	b := make([]byte, 6)
	_, _ = rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// writeUserFile replaces a file atomically without touching the permissions of
// its directory, which belongs to the assistant rather than to this app.
func writeUserFile(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp := path + ".jieshangai.tmp"
	if err := os.WriteFile(tmp, data, mode); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func loadPrompts(agent string) ([]Prompt, error) {
	data, err := os.ReadFile(promptStore(agent))
	if os.IsNotExist(err) {
		return []Prompt{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Prompt
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New("提示词记录已损坏，原文件未改动")
	}
	return list, nil
}

func savePrompts(agent string, list []Prompt) error {
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(promptStore(agent)), 0o700); err != nil {
		return err
	}
	return writePrivateFile(promptStore(agent), data)
}

func validatePrompt(name, content string) error {
	if strings.TrimSpace(name) == "" || len([]rune(name)) > maxPromptName {
		return errors.New("名称不能为空，且不超过 80 个字")
	}
	if len(content) > maxPromptContent {
		return errors.New("内容过长，请控制在 32 KB 以内")
	}
	return nil
}

func now() string { return time.Now().UTC().Format(time.RFC3339) }

// syncLive folds hand edits made to the live file back into the enabled prompt,
// or keeps them as a disabled backup when nothing is enabled.
func syncLive(agent, target string, list []Prompt) ([]Prompt, error) {
	raw, err := os.ReadFile(target)
	if os.IsNotExist(err) {
		return list, nil
	}
	if err != nil {
		return nil, err
	}
	live := string(raw)
	if strings.TrimSpace(live) == "" {
		return list, nil
	}
	for i := range list {
		if list[i].Enabled {
			if list[i].Content != live {
				list[i].Content, list[i].UpdatedAt = live, now()
			}
			return list, nil
		}
	}
	for _, p := range list {
		if strings.TrimSpace(p.Content) == strings.TrimSpace(live) {
			return list, nil
		}
	}
	return append(list, Prompt{ID: newPromptID("backup-"), Name: "原有设置（自动备份）", Content: live, UpdatedAt: now()}), nil
}

func ListPrompts(agent string) ([]Prompt, error) {
	promptMu.Lock()
	defer promptMu.Unlock()
	if _, err := promptTarget(agent); err != nil {
		return nil, err
	}
	return loadPrompts(agent)
}

// SavePrompt creates (empty id) or edits a prompt. Saving an enabled prompt
// updates the live file; saving a disabled one never touches it.
func SavePrompt(agent, id, name, content string) ([]Prompt, error) {
	promptMu.Lock()
	defer promptMu.Unlock()
	target, err := promptTarget(agent)
	if err != nil {
		return nil, err
	}
	if err := validatePrompt(name, content); err != nil {
		return nil, err
	}
	list, err := loadPrompts(agent)
	if err != nil {
		return nil, err
	}
	index := -1
	for i := range list {
		if id != "" && list[i].ID == id {
			index = i
		}
	}
	if id != "" && index < 0 {
		return nil, errors.New("没有找到这条提示词")
	}
	if index < 0 {
		list = append(list, Prompt{ID: newPromptID("p-"), Name: name, Content: content, UpdatedAt: now()})
		return list, savePrompts(agent, list)
	}
	list[index].Name, list[index].Content, list[index].UpdatedAt = name, content, now()
	if list[index].Enabled {
		if err := writeUserFile(target, []byte(content)); err != nil {
			return nil, errors.New("没能写入助手的提示词文件")
		}
	}
	return list, savePrompts(agent, list)
}

// EnablePrompt makes one prompt live. An empty id disables the current one.
func EnablePrompt(agent, id string) ([]Prompt, error) {
	promptMu.Lock()
	defer promptMu.Unlock()
	target, err := promptTarget(agent)
	if err != nil {
		return nil, err
	}
	list, err := loadPrompts(agent)
	if err != nil {
		return nil, err
	}
	chosen := -1
	for i := range list {
		if id != "" && list[i].ID == id {
			chosen = i
		}
	}
	if id != "" && chosen < 0 {
		return nil, errors.New("没有找到这条提示词")
	}
	// Save whatever is in the live file first, so nothing is silently lost.
	if list, err = syncLive(agent, target, list); err != nil {
		return nil, err
	}
	if err := savePrompts(agent, list); err != nil {
		return nil, err
	}
	for i := range list {
		list[i].Enabled = i == chosen
	}
	if chosen >= 0 {
		err = writeUserFile(target, []byte(list[chosen].Content))
	} else if _, statErr := os.Stat(target); statErr == nil {
		err = writeUserFile(target, nil)
	}
	if err != nil {
		return nil, errors.New("没能写入助手的提示词文件")
	}
	return list, savePrompts(agent, list)
}

// DeletePrompt refuses to remove the prompt that is currently live.
func DeletePrompt(agent, id string) ([]Prompt, error) {
	promptMu.Lock()
	defer promptMu.Unlock()
	if _, err := promptTarget(agent); err != nil {
		return nil, err
	}
	list, err := loadPrompts(agent)
	if err != nil {
		return nil, err
	}
	out := list[:0]
	found := false
	for _, p := range list {
		if p.ID == id {
			if p.Enabled {
				return nil, errors.New("正在使用的提示词不能删除，请先停用")
			}
			found = true
			continue
		}
		out = append(out, p)
	}
	if !found {
		return nil, errors.New("没有找到这条提示词")
	}
	return out, savePrompts(agent, out)
}

// The desktop state is serialized every 200 ms, so it reads this cache instead
// of the files. Only the prompt actions refresh it.
var (
	promptCacheMu sync.Mutex
	promptCache   = map[string][]Prompt{}
)

func cachePrompts(agent string, list []Prompt) {
	promptCacheMu.Lock()
	defer promptCacheMu.Unlock()
	promptCache[agent] = append([]Prompt(nil), list...)
}

func promptSnapshot() map[string][]Prompt {
	promptCacheMu.Lock()
	defer promptCacheMu.Unlock()
	if len(promptCache) == 0 {
		return nil
	}
	out := make(map[string][]Prompt, len(promptCache))
	for agent, list := range promptCache {
		out[agent] = append([]Prompt(nil), list...)
	}
	return out
}
