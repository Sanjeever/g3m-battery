//go:build windows

package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/windows/registry"
)

const (
	startupKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	startupValueName = "G3MBattery"
)

func readStartupEnabled() (bool, error) {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupKeyPath, registry.QUERY_VALUE)
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("打开开机启动注册表项: %w", err)
	}
	defer key.Close()

	value, _, err := key.GetStringValue(startupValueName)
	if err == registry.ErrNotExist {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("读取开机启动注册表值: %w", err)
	}
	executable, err := currentExecutablePath()
	if err != nil {
		return false, err
	}
	return normalizeStartupPath(value) == normalizeStartupPath(executable), nil
}

func enableStartup() error {
	executable, err := currentExecutablePath()
	if err != nil {
		return err
	}

	key, _, err := registry.CreateKey(registry.CURRENT_USER, startupKeyPath, registry.SET_VALUE)
	if err != nil {
		return fmt.Errorf("创建开机启动注册表项: %w", err)
	}
	defer key.Close()

	if err := key.SetStringValue(startupValueName, `"`+executable+`"`); err != nil {
		return fmt.Errorf("写入开机启动注册表值: %w", err)
	}
	return nil
}

func currentExecutablePath() (string, error) {
	executable, err := os.Executable()
	if err != nil {
		return "", fmt.Errorf("获取可执行文件路径: %w", err)
	}
	executable, err = filepath.Abs(executable)
	if err != nil {
		return "", fmt.Errorf("规范化可执行文件路径: %w", err)
	}
	return executable, nil
}

func normalizeStartupPath(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && value[0] == '"' && value[len(value)-1] == '"' {
		value = value[1 : len(value)-1]
	}
	return filepath.Clean(value)
}

func disableStartup() error {
	key, err := registry.OpenKey(registry.CURRENT_USER, startupKeyPath, registry.SET_VALUE)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("打开开机启动注册表项: %w", err)
	}
	defer key.Close()

	err = key.DeleteValue(startupValueName)
	if err == registry.ErrNotExist {
		return nil
	}
	if err != nil {
		return fmt.Errorf("删除开机启动注册表值: %w", err)
	}
	return nil
}
