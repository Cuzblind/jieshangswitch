package clientruntime

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

const (
	defaultWorkspaceFolderName = "接上AI工作区"
	maxRecentWorkDirs          = 5
)

var workDirMu sync.Mutex

type workDirStore struct {
	WorkDir    string   `json:"workDir,omitempty"`
	RecentDirs []string `json:"recentDirs,omitempty"`
}

func workDirStorePath() string {
	return filepath.Join(paths().Dir(), "workspace-dirs.json")
}

// defaultWorkspaceDir returns a clean, dedicated folder for non-technical users
// instead of dropping them into $HOME (which triggers trust & TCC prompts).
// It does not create the directory until the user actually launches a terminal.
func defaultWorkspaceDir() string {
	h := homeDir()
	desktop := filepath.Join(h, "Desktop")
	if st, err := os.Stat(desktop); err == nil && st.IsDir() {
		return filepath.Join(desktop, defaultWorkspaceFolderName)
	}
	return filepath.Join(h, "jieshangai-workspace")
}

func readWorkDirStore() workDirStore {
	var st workDirStore
	raw, err := os.ReadFile(workDirStorePath())
	if err != nil {
		return st
	}
	_ = json.Unmarshal(raw, &st)
	return st
}

func saveWorkDirStore(st workDirStore) error {
	dir := filepath.Dir(workDirStorePath())
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return err
	}
	return writePrivateFile(workDirStorePath(), append(b, '\n'))
}

func isUsableDir(dir string) bool {
	if dir == "" || !filepath.IsAbs(dir) || strings.ContainsAny(dir, "\x00\r\n") {
		return false
	}
	st, err := os.Stat(dir)
	return err == nil && st.IsDir()
}

// workDirSnapshot returns the active working folder (or the default workspace
// path when none was manually chosen) and existing recent directories.
func workDirSnapshot() (string, []string) {
	workDirMu.Lock()
	defer workDirMu.Unlock()
	st := readWorkDirStore()
	active := ""
	if isUsableDir(st.WorkDir) {
		active = st.WorkDir
	} else {
		active = defaultWorkspaceDir()
	}
	recent := make([]string, 0, maxRecentWorkDirs)
	seen := map[string]bool{}
	for _, d := range st.RecentDirs {
		clean := filepath.Clean(strings.TrimSpace(d))
		if !isUsableDir(clean) || seen[clean] {
			continue
		}
		seen[clean] = true
		recent = append(recent, clean)
		if len(recent) >= maxRecentWorkDirs {
			break
		}
	}
	return active, recent
}

// setWorkDir validates and persists the user's chosen working folder.
// Passing an empty string resets back to the default workspace folder.
func setWorkDir(raw string) (string, []string, error) {
	workDirMu.Lock()
	defer workDirMu.Unlock()
	st := readWorkDirStore()
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		st.WorkDir = ""
		if err := saveWorkDirStore(st); err != nil {
			return "", nil, errors.New("没能保存工作目录设置，请再试一次")
		}
		return defaultWorkspaceDir(), st.RecentDirs, nil
	}
	if strings.ContainsAny(trimmed, "\x00\r\n") || len(trimmed) > 1024 {
		return "", nil, errors.New("文件夹路径无效，请重新选择")
	}
	clean := filepath.Clean(trimmed)
	if !filepath.IsAbs(clean) {
		return "", nil, errors.New("请选择完整的本机文件夹路径")
	}
	info, err := os.Stat(clean)
	if err != nil || !info.IsDir() {
		return "", nil, errors.New("所选文件夹不存在或无法访问")
	}
	st.WorkDir = clean
	nextRecent := []string{clean}
	for _, d := range st.RecentDirs {
		dc := filepath.Clean(strings.TrimSpace(d))
		if dc == "" || dc == clean || !isUsableDir(dc) {
			continue
		}
		nextRecent = append(nextRecent, dc)
		if len(nextRecent) >= maxRecentWorkDirs {
			break
		}
	}
	st.RecentDirs = nextRecent
	if err := saveWorkDirStore(st); err != nil {
		return "", nil, errors.New("没能保存工作目录设置，请再试一次")
	}
	return clean, nextRecent, nil
}

// resolveLaunchWorkDir returns the directory to cd into when opening a CLI terminal.
// Creates the default clean workspace folder on demand; falls back to homeDir only on error.
func resolveLaunchWorkDir() string {
	workDirMu.Lock()
	st := readWorkDirStore()
	workDirMu.Unlock()
	if isUsableDir(st.WorkDir) {
		return st.WorkDir
	}
	def := defaultWorkspaceDir()
	if err := os.MkdirAll(def, 0o755); err == nil {
		return def
	}
	return homeDir()
}
