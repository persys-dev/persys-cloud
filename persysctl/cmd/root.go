package cmd

import (
	"fmt"
	"os"

	"github.com/persys-dev/persysctl/internal/config"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
)

var cfgFile string
var verbose bool

var rootCmd = &cobra.Command{
	Use:   "persysctl",
	Short: "Persys CLI for interacting with Persys",
	Long:  `A command-line client for managing workloads, nodes, and metrics in the Persys system.`,
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	cobra.OnInitialize(initConfig)

	rootCmd.PersistentFlags().StringVar(&cfgFile, "config", "", "config file (default is $HOME/.persys/config.yaml)")
	rootCmd.PersistentFlags().BoolVarP(&verbose, "verbose", "v", false, "enable verbose logging")

	rootCmd.PersistentFlags().String("transport", "http", "transport to use: http or grpc")
	rootCmd.PersistentFlags().String("grpc-endpoint", "", "gRPC endpoint, e.g. localhost:8085")
	rootCmd.PersistentFlags().Bool("grpc-insecure", false, "use insecure gRPC transport (no TLS)")
	rootCmd.PersistentFlags().String("grpc-target", "", "gRPC target service: scheduler or agent")
	rootCmd.PersistentFlags().Int("rpc-timeout-seconds", 0, "gRPC request timeout in seconds")

	_ = viper.BindPFlag("transport", rootCmd.PersistentFlags().Lookup("transport"))
	_ = viper.BindPFlag("grpc_endpoint", rootCmd.PersistentFlags().Lookup("grpc-endpoint"))
	_ = viper.BindPFlag("grpc_insecure", rootCmd.PersistentFlags().Lookup("grpc-insecure"))
	_ = viper.BindPFlag("grpc_target", rootCmd.PersistentFlags().Lookup("grpc-target"))
	_ = viper.BindPFlag("rpc_timeout_seconds", rootCmd.PersistentFlags().Lookup("rpc-timeout-seconds"))
}

func initConfig() {
	if cfgFile != "" {
		viper.SetConfigFile(cfgFile)
	} else {
		home, err := os.UserHomeDir()
		cobra.CheckErr(err)
		viper.AddConfigPath(home + "/.persys")
		viper.SetConfigName("config")
		viper.SetConfigType("yaml")
	}

	viper.AutomaticEnv()
	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			cobra.CheckErr(fmt.Errorf("failed to read config: %v", err))
		}
	}

	config.InitLogger(verbose)
}
