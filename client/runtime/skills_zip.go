package clientruntime

import (
	"archive/zip"
	"errors"
	"os"
	"path/filepath"
	"strings"
)

// ImportSkillZip unpacks a skill shared as a zip into the assistant's skills
// folder and takes it under management. The zip path comes from the native
// file picker, never from the page. Everything is checked before anything is
// written, and a failed import leaves no folder behind.
func ImportSkillZip(agent, zipPath string) ([]Skill, error) {
	return ImportSkillZipWithName(agent, zipPath, "")
}

// ImportSkillZipWithName unpacks a skill zip into the assistant's skills folder.
// When preferredDir is non-empty (e.g. a remote Skill store slug), it overrides
// the inferred directory name so temp-file zips always install under their slug.
func ImportSkillZipWithName(agent, zipPath, preferredDir string) ([]Skill, error) {
	agentDir, err := skillAgentDir(agent)
	if err != nil {
		return nil, err
	}
	z, err := zip.OpenReader(zipPath)
	if err != nil {
		return nil, errors.New("这不是可以打开的 zip 文件")
	}
	defer z.Close()
	prefix, dir, err := skillZipLayout(z.File, zipPath)
	if err != nil {
		return nil, err
	}
	if preferred := strings.TrimSpace(preferredDir); preferred != "" {
		dir = preferred
	}
	if problem := skillDirProblem(dir); problem != "" {
		return nil, errors.New("Skill " + problem)
	}
	dest := filepath.Join(agentDir, dir)
	if _, err := os.Lstat(dest); err == nil {
		return nil, errors.New("助手目录里已有同名文件夹，接上AI不会覆盖它")
	}
	if err := os.MkdirAll(agentDir, 0o755); err != nil {
		return nil, err
	}
	// Stage next to the destination so moving it in is a rename on one volume;
	// the dot prefix keeps it out of the skill list meanwhile.
	stage, err := os.MkdirTemp(agentDir, ".jieshangai-zip-")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(stage)
	root := filepath.Join(stage, "skill")
	if err := extractSkillZip(z.File, prefix, root); err != nil {
		return nil, err
	}
	if err := os.Rename(root, dest); err != nil {
		return nil, err
	}
	list, err := ImportSkill(agent, dir)
	if err != nil {
		_ = os.RemoveAll(dest)
		return nil, err
	}
	return list, nil
}

func zipJunk(name string) bool {
	return name == "__MACOSX" || strings.HasPrefix(name, "__MACOSX/")
}

// skillZipLayout checks every entry and finds the skill: SKILL.md at the top
// (named after the zip) or inside a single top-level folder (named after it).
func skillZipLayout(files []*zip.File, zipPath string) (prefix, dir string, err error) {
	check := filepath.Join(os.TempDir(), "jieshangai-zip-check")
	var count int
	var total uint64
	tops := map[string]bool{}
	names := map[string]bool{}
	for _, f := range files {
		if _, err := archivePath(check, f.Name); err != nil {
			return "", "", errors.New("压缩包里有不安全的路径，已拒绝导入")
		}
		if f.Mode()&os.ModeSymlink != 0 {
			return "", "", errors.New("压缩包里有链接文件，已拒绝导入")
		}
		if zipJunk(f.Name) {
			continue
		}
		if !f.FileInfo().IsDir() {
			count++
			total += f.UncompressedSize64
			if count > maxSkillFiles || total > maxSkillBytes {
				return "", "", errors.New("这个 Skill 太大了（超过 50 MB 或 5000 个文件）")
			}
		}
		name := strings.TrimSuffix(f.Name, "/")
		names[name] = true
		top, _, _ := strings.Cut(name, "/")
		if !strings.HasPrefix(top, ".") {
			tops[top] = true
		}
	}
	if names["SKILL.md"] {
		return "", strings.TrimSuffix(filepath.Base(zipPath), filepath.Ext(zipPath)), nil
	}
	if len(tops) == 1 {
		for top := range tops {
			if names[top+"/SKILL.md"] {
				return top + "/", top, nil
			}
		}
	}
	return "", "", errors.New("压缩包里没有找到唯一的 SKILL.md，请确认它是一个 Skill")
}

// extractSkillZip writes the entries under prefix into root.
func extractSkillZip(files []*zip.File, prefix, root string) error {
	if err := os.MkdirAll(root, 0o755); err != nil {
		return err
	}
	for _, f := range files {
		if zipJunk(f.Name) || !strings.HasPrefix(f.Name, prefix) {
			continue
		}
		rel := strings.TrimPrefix(f.Name, prefix)
		if rel == "" || rel == "/" {
			continue
		}
		p, err := archivePath(root, rel)
		if err != nil {
			return errors.New("压缩包里有不安全的路径，已拒绝导入")
		}
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(p, 0o755); err != nil {
				return err
			}
			continue
		}
		r, err := f.Open()
		if err != nil {
			return err
		}
		err = writeArchiveFile(p, f.Mode()|0o644, r, int64(f.UncompressedSize64))
		r.Close()
		if err != nil {
			return err
		}
	}
	return nil
}
