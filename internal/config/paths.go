package config

import "path/filepath"

const appDirName = "VideoCompress"

// DefaultSettingsPath returns <UserConfigDir>/VideoCompress/settings.json.
func DefaultSettingsPath() (string, error) {
	base, err := osUserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName, "settings.json"), nil
}

// CacheDirPath returns <UserCacheDir>/VideoCompress.
func CacheDirPath() (string, error) {
	base, err := osUserCacheDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(base, appDirName), nil
}

// TempRootPath returns <UserCacheDir>/VideoCompress/temp.
func TempRootPath() (string, error) {
	cache, err := CacheDirPath()
	if err != nil {
		return "", err
	}
	return filepath.Join(cache, "temp"), nil
}
