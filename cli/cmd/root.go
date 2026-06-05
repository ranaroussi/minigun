package cmd

import (
	"encoding/json"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/ranaroussi/minigun/cli/internal/client"
)

const Version = "0.4.4"

const defaultAPIURL = "http://127.0.0.1:8080"

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
	rootCmd.PersistentFlags().StringVar(&apiURL, "api", "", "MiniGun server URL (env: MINIGUN_API_URL, default \""+defaultAPIURL+"\")")
	rootCmd.PersistentFlags().StringVar(&apiToken, "token", "", "API token, sent as Bearer (env: MINIGUN_API_TOKEN)")
}

func Execute() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func newClient() *client.Client {
	url := apiURL
	if url == "" {
		url = os.Getenv("MINIGUN_API_URL")
	}
	if url == "" {
		url = defaultAPIURL
	}
	token := apiToken
	if token == "" {
		token = os.Getenv("MINIGUN_API_TOKEN")
	}
	return client.New(url, token)
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
