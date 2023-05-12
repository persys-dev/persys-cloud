package read_toml

import (
	"github.com/spf13/viper"
)

type manifest struct {
	AppName            string `json:"appName"`
	Version            string `json:"appVersion"`
	Language           string `json:"appLanguage"`
	Description        string `json:"description"`
	GithubURL          string `json:"githubURL"`
	Ports              string `json:"ports"`
	Build              string `json:"build"`
	ImageTag           string `json:"imageTag"`
	Repositories       string `json:"repositories"`
	Tests              string `json:"tests"`
	Deploy             string `json:"deploy"`
	Namespace          string `json:"namespace"`
	Cloud              string `json:"cloud"`
	Replica            string `json:"replica"`
	CheckPoint         string `json:"checkPoint"`
	DependencyServices string `json:"dependencyServices"`
	Config             string `json:"config"`
}

func ReadManifest(directory string) (*manifest, error) {
	viper.SetConfigName("manifest") // name of config file (without extension)
	viper.AddConfigPath(directory)  // optionally look for config in the working directory
	err := viper.ReadInConfig()     // Find and read the config file

	if err != nil { // Handle errors reading the config file
		return nil, err
	}

	m := &manifest{
		AppName:            viper.GetString("app.name"),
		Version:            viper.GetString("app.version"),
		Language:           viper.GetString("app.language"),
		Description:        viper.GetString("app.description"),
		GithubURL:          viper.GetString("app.githubURL"),
		Ports:              viper.GetString("app.services.ports"),
		Build:              viper.GetString("app.build.build"),
		ImageTag:           viper.GetString("app.build.imageTag"),
		Repositories:       viper.GetString("app.build.repositories"),
		Tests:              viper.GetString("app.test.tests"),
		Deploy:             viper.GetString("app.deploy.deploy"),
		Namespace:          viper.GetString("app.deploy.namespace"),
		Cloud:              viper.GetString("app.deploy.cloud"),
		Replica:            viper.GetString("app.deploy.replica"),
		CheckPoint:         viper.GetString("app.deploy.checkpoint"),
		DependencyServices: viper.GetString("app.dependencies.services"),
		Config:             viper.GetString("app.config.file"),
	}

	//fmt.Println("owner name", viper.GetString("owner.name"))
	//fmt.Println("database user", viper.GetString("database.user"))
	return m, nil
}
