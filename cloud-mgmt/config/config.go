package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	HttpAddr      string      `json:"httpAddr"`
	GrpcAddr      string      `json:"grpcAddr"`
	MongoURI      string      `json:"mongoURI"`
	Collections   interface{} `json:"collections"`
	KafkaBroker   string      `json:"kafkaBroker"`
	WebHookURL    string      `json:"webHookURL"`
	WebHookSecret string      `json:"webHookSecret"`
	ClientID      string      `json:"clientID"`
	ClientSecret  string      `json:"clientSecret"`
}

func ReadConfig() (*Config, error) {
	viper.SetConfigName("config") // name of config file (without extension)
	viper.SetConfigType("toml")
	viper.AddConfigPath(".") // optionally look for config in the working directory
	//viper.AddConfigPath("/app/")
	err := viper.ReadInConfig() // Find and read the config file

	if err != nil { // Handle errors reading the config file
		return nil, err
	}

	m := &Config{
		HttpAddr:      viper.GetString("app.httpAddr"),
		GrpcAddr:      viper.GetString("app.grpcAddr"),
		MongoURI:      viper.GetString("database.mongoURI"),
		Collections:   viper.Get("database.collections"),
		KafkaBroker:   viper.GetString("watermill.kafkaBroker"),
		WebHookURL:    viper.GetString("github.webHookURL"),
		WebHookSecret: viper.GetString("github.webHookSecret"),
		ClientID:      viper.GetString("github.auth.clientID"),
		ClientSecret:  viper.GetString("github.auth.clientSecret"),
	}

	//fmt.Println("owner name", viper.GetString("owner.name"))
	//fmt.Println("database user", viper.GetString("database.user"))
	return m, nil
}
