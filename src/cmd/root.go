package cmd

import (
	"log/slog"
	"os"

	"github.com/spf13/cobra"
)

var rootCmd = &cobra.Command{
	Use:   "minigun",
	Short: "Self-hosted lightweight email service on top of Mailgun",
}

var (
	apiURL string
	logger *slog.Logger
)

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api", envOr("MINIGUN_API_URL", "http://127.0.0.1:8080"), "MiniGun server URL")
	logger = slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}
