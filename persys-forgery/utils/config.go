package utils

import (
	"io/ioutil"

	"gopkg.in/yaml.v3"
)

type RedisConfig struct {
	Addr     string `yaml:"addr"`
	Password string `yaml:"password"`
	DB       int    `yaml:"db"`
}

type BuildConfig struct {
	Workspace string `yaml:"workspace"`
}

type Config struct {
	MySQLDSN string      `yaml:"mysql_dsn"`
	Port     int         `yaml:"port"`
	Redis    RedisConfig `yaml:"redis"`
	Build    BuildConfig `yaml:"build"`
}

func LoadConfig(path string) (*Config, error) {
	data, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, err
	}
	return &cfg, nil
}
