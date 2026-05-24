package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ranaroussi/minigun/cli/internal/client"
)

const Version = "0.1.0"

var (
	apiURL   string
	apiToken string

	rootCmd = &cobra.Command{
		Use:           "minigun",
		Short:         "MiniGun CLI — control a MiniGun server from your laptop",
		Long:          "MiniGun CLI talks to a MiniGun server over HTTP. Configure with --api or MINIGUN_API_URL.",
		Version:       Version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
)

func init() {
	rootCmd.PersistentFlags().StringVar(&apiURL, "api", envOr("MINIGUN_API_URL", "http://127.0.0.1:8080"), "MiniGun server URL (env: MINIGUN_API_URL)")
	rootCmd.PersistentFlags().StringVar(&apiToken, "token", os.Getenv("MINIGUN_API_TOKEN"), "API token, sent as Bearer (env: MINIGUN_API_TOKEN)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newClient() *client.Client {
	return client.New(apiURL, apiToken)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func printJSON(raw []byte) {
	if len(raw) == 0 {
		return
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		_, _ = os.Stdout.Write(raw)
		if len(raw) > 0 && raw[len(raw)-1] != '\n' {
			fmt.Println()
		}
		return
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(v)
}
