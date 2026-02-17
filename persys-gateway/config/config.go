package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	App       AppConfig       `mapstructure:"app"`
	Database  DatabaseConfig  `mapstructure:"database"`
	TLS       TLSConfig       `mapstructure:"tls"`
	CoreDNS   CoreDNSConfig   `mapstructure:"coreDNS"`
	CFSSL     CFSSLConfig     `mapstructure:"cfssl"`
	Prow      ProwConfig      `mapstructure:"prow"`
	GitHub    GitHubConfig    `mapstructure:"github"`
	Log       LogConfig       `mapstructure:"log"`
	Telemetry TelemetryConfig `mapstructure:"telemetry"`
}

type AppConfig struct {
	HTTPAddr         string            `mapstructure:"httpAddr"`
	HTTPAddrNonMTLS  string            `mapstructure:"httpAddrNonMTLS"`
	GRPCAddr         string            `mapstructure:"grpcAddr"`
	Storage          string            `mapstructure:"storage"`
	Metadata         map[string]string `mapstructure:"metadata"`
}

type DatabaseConfig struct {
	MongoURI    string   `mapstructure:"mongoURI"`
	Collections []string `mapstructure:"collections"`
	Name        string   `mapstructure:"name"`
}

type TLSConfig struct {
	CertPath string `mapstructure:"certPath"`
	KeyPath  string `mapstructure:"keyPath"`
	CAPath   string `mapstructure:"caPath"`
}

type CoreDNSConfig struct {
	Addr string `mapstructure:"addr"`
}

type CFSSLConfig struct {
	APIURL string `mapstructure:"apiUrl"`
}

type ProwConfig struct {
	SchedulerAddr    string `mapstructure:"schedulerAddr"`
	EnableProxy      bool   `mapstructure:"enableProxy"`
	DiscoveryDomain  string `mapstructure:"discoveryDomain"`
	DiscoveryService string `mapstructure:"discoveryService"`
}

type GitHubConfig struct {
	WebHookURL    string `mapstructure:"webHookURL"`
	WebHookSecret string `mapstructure:"webHookSecret"`
	Auth          struct {
		ClientID     string `mapstructure:"clientID"`
		ClientSecret string `mapstructure:"clientSecret"`
	} `mapstructure:"auth"`
}

type LogConfig struct {
	LokiEndpoint string `mapstructure:"lokiEndpoint"`
	Level        string `mapstructure:"level"`
}

type TelemetryConfig struct {
	Addr string `mapstructure:"addr"`
}

func LoadConfig() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("toml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")

	if err := viper.ReadInConfig(); err != nil {
		return nil, err
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, err
	}

	return &config, nil
}
