//go:build !windows && !darwin

package clientruntime

import (
	"context"
	"errors"
)

func inspectGUI(ctx context.Context, agent string) (guiInfo, error) { return guiInfo{}, nil }
func (m *toolManager) latestGUI(ctx context.Context, agent string) (guiRelease, error) {
	return guiRelease{}, errors.New("这个系统暂不支持官方桌面版安装")
}
func (m *toolManager) latestGUIVersion(ctx context.Context, agent string) (string, error) {
	return "", errors.New("这个系统暂不支持官方桌面版安装")
}
func (m *toolManager) installGUIPlatform(ctx context.Context, agent string, update bool, p func(string)) error {
	return errors.New("这个系统暂不支持官方桌面版安装")
}
