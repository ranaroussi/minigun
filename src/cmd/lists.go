package cmd

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/spf13/cobra"
)

var listCmd = &cobra.Command{
	Use:   "list",
	Short: "Manage lists",
}

var listCreateCmd = &cobra.Command{
	Use:   "create",
	Short: "Create a list",
	RunE:  runListCreate,
}

var (
	listName string
	listSlug string
)

func init() {
	listCreateCmd.Flags().StringVar(&listName, "name", "", "Human-readable list name")
	listCreateCmd.Flags().StringVar(&listSlug, "slug", "", "URL-safe slug")
	_ = listCreateCmd.MarkFlagRequired("name")
	_ = listCreateCmd.MarkFlagRequired("slug")
	listCmd.AddCommand(listCreateCmd)
	rootCmd.AddCommand(listCmd)
}

func runListCreate(cmd *cobra.Command, args []string) error {
	body, err := json.Marshal(map[string]string{
		"name": listName,
		"slug": listSlug,
	})
	if err != nil {
		return err
	}
	return postJSON("/lists", body)
}

func postJSON(path string, body []byte) error {
	req, err := http.NewRequest(http.MethodPost, apiURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	fmt.Println(string(out))
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %d", resp.StatusCode)
	}
	return nil
}
