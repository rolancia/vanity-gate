package config

import (
	"sync"
)

var _config Config = Config{}
var muConfig sync.RWMutex

func GetConfig() Config {
	muConfig.RLock()
	defer muConfig.RUnlock()
	return _config
}

func SetConfig(c Config) {
	muConfig.Lock()
	defer muConfig.Unlock()
	_config = c
}

type Config struct {
	Apps []App `json:"apps"`
}

type App struct {
	Name       string `json:"name"`
	URL        string `json:"url"`
	Entrypoint string `json:"entrypoint"`
}
