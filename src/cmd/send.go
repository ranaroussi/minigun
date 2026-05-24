package cmd

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"

	"github.com/spf13/cobra"
)

var sendCmd = &cobra.Command{
	Use:   "send",
	Short: "Send emails",
}

var (
	sendList       string
	sendSubject    string
	sendPreheader  string
	sendFrom       string
	sendReplyTo    string
	sendMDFile     string
	sendHTMLFile   string
	sendTemplate   string
	sendBatchSize  int
	sendThrottleMS int
	sendNotifyTo   string
	sendUnsubMode  string
	sendUnsubRedir string
	sendUnsubURL   string
	sendTo         string
)

var sendBulkCmd = &cobra.Command{
	Use:   "bulk",
	Short: "Trigger a bulk send",
	RunE:  runSendBulk,
}

var sendSingleCmd = &cobra.Command{
	Use:   "single",
	Short: "Send a single email",
	RunE:  runSendSingle,
}

var sendResumeCmd = &cobra.Command{
	Use:   "resume <send_id>",
	Short: "Resume a paused or failed send",
	Args:  cobra.ExactArgs(1),
	RunE:  runSendResume,
}

func init() {
	sendBulkCmd.Flags().StringVar(&sendList, "list", "", "List slug or id")
	sendBulkCmd.Flags().StringVar(&sendSubject, "subject", "", "Email subject")
	sendBulkCmd.Flags().StringVar(&sendPreheader, "preheader", "", "Preheader text")
	sendBulkCmd.Flags().StringVar(&sendFrom, "from", "", "From header (e.g. 'Ran <ran@example.com>')")
	sendBulkCmd.Flags().StringVar(&sendReplyTo, "reply-to", "", "Reply-To address")
	sendBulkCmd.Flags().StringVar(&sendMDFile, "md", "", "Markdown body file")
	sendBulkCmd.Flags().StringVar(&sendHTMLFile, "html", "", "HTML body file")
	sendBulkCmd.Flags().StringVar(&sendTemplate, "template", "", "Wrapper template name")
	sendBulkCmd.Flags().IntVar(&sendBatchSize, "batch-size", 500, "Batch size")
	sendBulkCmd.Flags().IntVar(&sendThrottleMS, "throttle-ms", 1000, "Throttle between batches in ms")
	sendBulkCmd.Flags().StringVar(&sendNotifyTo, "notify", "", "Email to notify on completion or failure")
	sendBulkCmd.Flags().StringVar(&sendUnsubMode, "unsub-mode", "local", "Unsubscribe mode: local|redirect|external")
	sendBulkCmd.Flags().StringVar(&sendUnsubRedir, "unsub-redir", "", "Redirect URL for redirect mode")
	sendBulkCmd.Flags().StringVar(&sendUnsubURL, "unsub-url", "", "External handler URL for external mode")
	_ = sendBulkCmd.MarkFlagRequired("list")
	_ = sendBulkCmd.MarkFlagRequired("subject")
	_ = sendBulkCmd.MarkFlagRequired("from")

	sendSingleCmd.Flags().StringVar(&sendTo, "to", "", "Recipient email")
	sendSingleCmd.Flags().StringVar(&sendSubject, "subject", "", "Email subject")
	sendSingleCmd.Flags().StringVar(&sendFrom, "from", "", "From header")
	sendSingleCmd.Flags().StringVar(&sendReplyTo, "reply-to", "", "Reply-To address")
	sendSingleCmd.Flags().StringVar(&sendMDFile, "md", "", "Markdown body file")
	sendSingleCmd.Flags().StringVar(&sendHTMLFile, "html", "", "HTML body file")
	_ = sendSingleCmd.MarkFlagRequired("to")
	_ = sendSingleCmd.MarkFlagRequired("subject")
	_ = sendSingleCmd.MarkFlagRequired("from")

	sendCmd.AddCommand(sendBulkCmd, sendSingleCmd, sendResumeCmd)
	rootCmd.AddCommand(sendCmd)
}

func runSendBulk(cmd *cobra.Command, args []string) error {
	mdBody, htmlBody, err := readBody()
	if err != nil {
		return err
	}
	payload := map[string]any{
		"list":         sendList,
		"subject":      sendSubject,
		"preheader":    sendPreheader,
		"from":         sendFrom,
		"reply_to":     sendReplyTo,
		"template":     sendTemplate,
		"batch_size":   sendBatchSize,
		"throttle_ms":  sendThrottleMS,
		"notify_email": sendNotifyTo,
		"unsub_mode":   sendUnsubMode,
		"unsub_redir":  sendUnsubRedir,
		"unsub_url":    sendUnsubURL,
		"md":           mdBody,
		"html":         htmlBody,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return postJSON("/send/bulk", body)
}

func runSendSingle(cmd *cobra.Command, args []string) error {
	mdBody, htmlBody, err := readBody()
	if err != nil {
		return err
	}
	payload := map[string]any{
		"to":       sendTo,
		"subject":  sendSubject,
		"from":     sendFrom,
		"reply_to": sendReplyTo,
		"md":       mdBody,
		"html":     htmlBody,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return postJSON("/send/single", body)
}

func runSendResume(cmd *cobra.Command, args []string) error {
	id := args[0]
	req, err := http.NewRequest(http.MethodPost, apiURL+"/send/"+id+"/resume", nil)
	if err != nil {
		return err
	}
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

func readBody() (md, html string, err error) {
	if sendMDFile == "" && sendHTMLFile == "" {
		return "", "", errors.New("either --md or --html is required")
	}
	if sendMDFile != "" {
		b, e := os.ReadFile(sendMDFile)
		if e != nil {
			return "", "", e
		}
		md = string(b)
	}
	if sendHTMLFile != "" {
		b, e := os.ReadFile(sendHTMLFile)
		if e != nil {
			return "", "", e
		}
		html = string(b)
	}
	return md, html, nil
}
