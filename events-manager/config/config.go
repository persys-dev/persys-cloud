package config

import (
	"github.com/spf13/viper"
)

type Config struct {
	EtcdAddr    []string    `json:"etcdAddr"`
	GrpcAddr    string      `json:"grpcAddr"`
	MongoURI    string      `json:"mongoURI"`
	Collections interface{} `json:"collections"`
	KafkaBroker string      `json:"kafkaBroker"`
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
		EtcdAddr:    viper.GetStringSlice("etcd.uri"),
		GrpcAddr:    viper.GetString("app.grpcAddr"),
		MongoURI:    viper.GetString("database.mongoURI"),
		Collections: viper.Get("database.collections"),
		KafkaBroker: viper.GetString("watermill.kafkaBroker"),
	}

	//fmt.Println("owner name", viper.GetString("owner.name"))
	//fmt.Println("database user", viper.GetString("database.user"))
	return m, nil
}
