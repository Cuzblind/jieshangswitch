package clientruntime

import (
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"
)

// Skill management is local only. A skill is a folder with a SKILL.md in
// ~/.claude/skills or ~/.codex/skills. Importing one keeps a copy in
// ~/.jieshangai/skills/files; enabling it for an assistant copies that folder
// into the assistant's directory with a marker file. Only marked folders are
// ever removed, so a skill the user added by hand is never deleted.
// Copies are used instead of symlinks, which need extra rights on Windows.

const (
	skillMarker    = ".jieshangai-skill"
	maxSkillBytes  = 50 << 20
	maxSkillFiles  = 5000
	maxSkillsCount = 200
)

const maxSkillDirRunes = 64

// skillDirProblem says why a folder name cannot be managed, or "" when it can.
// Everyday names (Chinese, spaces, brackets) are fine; only names that could
// leave the skills folder or that Windows cannot create are refused. Listing
// and importing share this rule, so a listed folder never fails to import.
func skillDirProblem(dir string) string {
	switch {
	case dir == "" || dir == "." || dir == ".." || !utf8.ValidString(dir):
		return "文件夹名无效"
	case strings.HasPrefix(dir, "."):
		return "以点开头的文件夹不会被助手读取"
	case utf8.RuneCountInString(dir) > maxSkillDirRunes:
		return "文件夹名太长（超过 64 个字）"
	case strings.ContainsAny(dir, `/\:*?"<>|`) || strings.IndexFunc(dir, unicode.IsControl) >= 0:
		return "文件夹名含有不能使用的字符"
	case strings.HasSuffix(dir, " ") || strings.HasSuffix(dir, "."):
		return "文件夹名不能以空格或点结尾"
	}
	base, _, _ := strings.Cut(strings.ToLower(dir), ".")
	switch base {
	case "con", "prn", "aux", "nul":
		return "这是 Windows 保留的名字"
	}
	if len(base) == 4 && (strings.HasPrefix(base, "com") || strings.HasPrefix(base, "lpt")) && base[3] >= '1' && base[3] <= '9' {
		return "这是 Windows 保留的名字"
	}
	return ""
}

type Skill struct {
	Dir         string `json:"dir"`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	// Origin is the assistant the skill was imported from.
	Origin string `json:"origin,omitempty"`
	// Derived on every read, never stored.
	Enabled  bool `json:"enabled"`
	Managed  bool `json:"managed"`
	Conflict bool `json:"conflict,omitempty"`
	// Unsupported says why an unmanaged folder cannot be taken under management.
	Unsupported string `json:"unsupported,omitempty"`
}

var skillMu sync.Mutex

func skillAgentDir(agent string) (string, error) {
	switch agent {
	case "claude":
		return filepath.Join(homeDir(), ".claude", "skills"), nil
	case "codex":
		return filepath.Join(homeDir(), ".codex", "skills"), nil
	}
	return "", errors.New("请选择 Claude Code 或 Codex")
}

func skillRoot() string      { return filepath.Join(homeDir(), ".jieshangai", "skills") }
func skillIndexPath() string { return filepath.Join(skillRoot(), "index.json") }
func skillFiles(dir string) string {
	return filepath.Join(skillRoot(), "files", dir)
}

func loadSkills() ([]Skill, error) {
	data, err := os.ReadFile(skillIndexPath())
	if os.IsNotExist(err) {
		return []Skill{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []Skill
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, errors.New("Skills 记录已损坏，原文件未改动")
	}
	return list, nil
}

func saveSkills(list []Skill) error {
	clean := make([]Skill, len(list))
	for i, s := range list {
		clean[i] = Skill{Dir: s.Dir, Name: s.Name, Description: s.Description, Origin: s.Origin}
	}
	data, err := json.MarshalIndent(clean, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(skillRoot(), 0o700); err != nil {
		return err
	}
	return writePrivateFile(skillIndexPath(), data)
}

// readSkillMeta takes name and description from the SKILL.md front matter.
func readSkillMeta(dir, fallback string) (string, string) {
	f, err := os.Open(filepath.Join(dir, "SKILL.md"))
	if err != nil {
		return fallback, ""
	}
	defer f.Close()
	head, _ := io.ReadAll(io.LimitReader(f, 16<<10))
	lines := strings.Split(strings.ReplaceAll(string(head), "\r\n", "\n"), "\n")
	name, desc := fallback, ""
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != "---" {
		return name, desc
	}
	for _, line := range lines[1:] {
		if strings.TrimSpace(line) == "---" {
			break
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			continue
		}
		value = strings.Trim(strings.TrimSpace(value), `"'`)
		switch strings.TrimSpace(key) {
		case "name":
			if value != "" {
				name = value
			}
		case "description":
			desc = value
		}
	}
	return clip(name, 80), clip(desc, 240)
}

func clip(s string, n int) string {
	if !utf8.ValidString(s) {
		s = strings.ToValidUTF8(s, "")
	}
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func isSkillFolder(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || !st.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, "SKILL.md"))
	return err == nil
}

// linkedSkillFolder reports a symlink or junction that leads to a skill. Copies
// never follow links, so such a folder is listed but cannot be managed.
func linkedSkillFolder(path string) bool {
	st, err := os.Lstat(path)
	if err != nil || st.Mode()&(fs.ModeSymlink|fs.ModeIrregular) == 0 {
		return false
	}
	target, err := os.Stat(path)
	if err != nil || !target.IsDir() {
		return false
	}
	_, err = os.Stat(filepath.Join(path, "SKILL.md"))
	return err == nil
}

func hasMarker(path string) bool {
	_, err := os.Lstat(filepath.Join(path, skillMarker))
	return err == nil
}

// copySkill copies regular files only; links are skipped rather than followed
// so a skill can never pull in files from elsewhere on the disk.
func copySkill(src, dst string) error {
	var files int
	var total int64
	return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == skillMarker {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		if !d.Type().IsRegular() {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		files++
		total += info.Size()
		if files > maxSkillFiles || total > maxSkillBytes {
			return errors.New("这个 Skill 太大了（超过 50 MB 或 5000 个文件）")
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode().Perm()|0o200)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, in); err != nil {
			out.Close()
			return err
		}
		return out.Close()
	})
}

// placeSkill copies a folder next to its destination and renames it into place.
func placeSkill(src, dst, marker string) error {
	tmp := filepath.Join(filepath.Dir(dst), ".jieshangai-tmp-"+filepath.Base(dst))
	_ = os.RemoveAll(tmp)
	if err := os.MkdirAll(tmp, 0o755); err != nil {
		return err
	}
	if err := copySkill(src, tmp); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if marker != "" {
		if err := os.WriteFile(filepath.Join(tmp, skillMarker), []byte(marker+"\n"), 0o644); err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	return nil
}

func findSkill(list []Skill, dir string) int {
	for i := range list {
		if list[i].Dir == dir {
			return i
		}
	}
	return -1
}

// skillState says whether a managed skill is live in an assistant's directory,
// and whether a folder of the same name belongs to the user instead.
func skillState(agentDir, dir string) (enabled, conflict bool) {
	path := filepath.Join(agentDir, dir)
	if _, err := os.Lstat(path); err != nil {
		return false, false
	}
	if isSkillFolder(path) && hasMarker(path) {
		return true, false
	}
	return false, true
}

func listSkills(agent string) ([]Skill, error) {
	agentDir, err := skillAgentDir(agent)
	if err != nil {
		return nil, err
	}
	stored, err := loadSkills()
	if err != nil {
		return nil, err
	}
	out := make([]Skill, 0, len(stored))
	known := map[string]bool{}
	for _, s := range stored {
		s.Managed = true
		s.Enabled, s.Conflict = skillState(agentDir, s.Dir)
		known[s.Dir] = true
		out = append(out, s)
	}
	entries, _ := os.ReadDir(agentDir)
	for _, e := range entries {
		dir := e.Name()
		path := filepath.Join(agentDir, dir)
		if known[dir] || strings.HasPrefix(dir, ".") {
			continue
		}
		problem := ""
		switch {
		case isSkillFolder(path):
			problem = skillDirProblem(dir)
		case linkedSkillFolder(path):
			problem = "这是链接文件夹，暂不支持纳入管理"
		default:
			continue
		}
		name, desc := readSkillMeta(path, dir)
		out = append(out, Skill{Dir: dir, Name: name, Description: desc, Enabled: true, Unsupported: problem})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Managed != out[j].Managed {
			return out[i].Managed
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func ListSkills(agent string) ([]Skill, error) {
	skillMu.Lock()
	defer skillMu.Unlock()
	return listSkills(agent)
}

// ImportSkill takes a folder the user already has under management. The
// folder stays where it is and only gains a marker; a copy is kept so the
// skill can be enabled for the other assistant or after being disabled.
func ImportSkill(agent, dir string) ([]Skill, error) {
	skillMu.Lock()
	defer skillMu.Unlock()
	agentDir, err := skillAgentDir(agent)
	if err != nil {
		return nil, err
	}
	if problem := skillDirProblem(dir); problem != "" {
		return nil, errors.New("Skill " + problem)
	}
	stored, err := loadSkills()
	if err != nil {
		return nil, err
	}
	if findSkill(stored, dir) >= 0 {
		return nil, errors.New("已经有同名的 Skill")
	}
	if len(stored) >= maxSkillsCount {
		return nil, errors.New("Skills 太多了，请先删掉一些")
	}
	src := filepath.Join(agentDir, dir)
	// A marker without an index entry (lost index, folder copied from another
	// machine) is stale; the folder is unmanaged and the marker is rewritten below.
	if !isSkillFolder(src) {
		return nil, errors.New("没有找到这个 Skill，或它缺少 SKILL.md")
	}
	if err := os.MkdirAll(filepath.Join(skillRoot(), "files"), 0o700); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(skillFiles(dir))
	if err := placeSkill(src, skillFiles(dir), ""); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(src, skillMarker), []byte("imported\n"), 0o644); err != nil {
		return nil, errors.New("没能标记这个 Skill，原文件未改动")
	}
	name, desc := readSkillMeta(src, dir)
	stored = append(stored, Skill{Dir: dir, Name: name, Description: desc, Origin: agent})
	if err := saveSkills(stored); err != nil {
		_ = os.Remove(filepath.Join(src, skillMarker))
		return nil, err
	}
	return listSkills(agent)
}

func SetSkillEnabled(agent, dir string, on bool) ([]Skill, error) {
	skillMu.Lock()
	defer skillMu.Unlock()
	agentDir, err := skillAgentDir(agent)
	if err != nil {
		return nil, err
	}
	stored, err := loadSkills()
	if err != nil {
		return nil, err
	}
	if findSkill(stored, dir) < 0 {
		return nil, errors.New("没有找到这个 Skill")
	}
	enabled, conflict := skillState(agentDir, dir)
	switch {
	case on && conflict:
		return nil, errors.New("助手目录里已有同名文件夹，接上AI不会覆盖它")
	case on && !enabled:
		if err := os.MkdirAll(agentDir, 0o755); err != nil {
			return nil, err
		}
		if err := placeSkill(skillFiles(dir), filepath.Join(agentDir, dir), "enabled"); err != nil {
			return nil, errors.New("没能启用这个 Skill：" + err.Error())
		}
	case !on && enabled:
		if err := disableSkill(agentDir, dir); err != nil {
			return nil, err
		}
	}
	return listSkills(agent)
}

// disableSkill keeps hand edits made in the assistant's copy, then removes it.
func disableSkill(agentDir, dir string) error {
	live := filepath.Join(agentDir, dir)
	fresh := skillFiles(dir) + ".new"
	_ = os.RemoveAll(fresh)
	if err := placeSkill(live, fresh, ""); err == nil {
		_ = os.RemoveAll(skillFiles(dir))
		if err := os.Rename(fresh, skillFiles(dir)); err != nil {
			return err
		}
	}
	if err := os.RemoveAll(live); err != nil {
		return errors.New("没能停用这个 Skill，请关闭正在使用它的程序后再试")
	}
	return nil
}

// DeleteSkill refuses while the skill is still enabled for either assistant.
func DeleteSkill(agent, dir string) ([]Skill, error) {
	skillMu.Lock()
	defer skillMu.Unlock()
	stored, err := loadSkills()
	if err != nil {
		return nil, err
	}
	i := findSkill(stored, dir)
	if i < 0 {
		return nil, errors.New("没有找到这个 Skill")
	}
	for _, a := range []string{"claude", "codex"} {
		d, _ := skillAgentDir(a)
		if on, _ := skillState(d, dir); on {
			return nil, errors.New("请先在 Claude Code 和 Codex 里都停用它，再删除")
		}
	}
	if err := saveSkills(append(stored[:i:i], stored[i+1:]...)); err != nil {
		return nil, err
	}
	_ = os.RemoveAll(skillFiles(dir))
	return listSkills(agent)
}

// clearSkills is part of the global restore. A skill the user had before
// importing stays where it was and only loses its marker; every copy this app
// put there is removed. The stored copies stay so they can be enabled again.
func clearSkills() error {
	skillMu.Lock()
	defer skillMu.Unlock()
	stored, err := loadSkills()
	if err != nil {
		return err
	}
	for _, agent := range []string{"claude", "codex"} {
		agentDir, _ := skillAgentDir(agent)
		for _, s := range stored {
			if on, _ := skillState(agentDir, s.Dir); !on {
				continue
			}
			live := filepath.Join(agentDir, s.Dir)
			marker, _ := os.ReadFile(filepath.Join(live, skillMarker))
			if s.Origin == agent && strings.TrimSpace(string(marker)) == "imported" {
				err = os.Remove(filepath.Join(live, skillMarker))
			} else {
				err = disableSkill(agentDir, s.Dir)
			}
			if err != nil {
				return err
			}
		}
	}
	return nil
}

var (
	skillCacheMu sync.Mutex
	skillCache   = map[string][]Skill{}
)

func cacheSkills(agent string, list []Skill) {
	skillCacheMu.Lock()
	defer skillCacheMu.Unlock()
	skillCache[agent] = append([]Skill(nil), list...)
}

func skillSnapshot() map[string][]Skill {
	skillCacheMu.Lock()
	defer skillCacheMu.Unlock()
	if len(skillCache) == 0 {
		return nil
	}
	out := make(map[string][]Skill, len(skillCache))
	for agent, list := range skillCache {
		out[agent] = append([]Skill(nil), list...)
	}
	return out
}
