package config

import "os"

func osUserConfigDir() (string, error) { return os.UserConfigDir() }
func osUserCacheDir() (string, error)  { return os.UserCacheDir() }
