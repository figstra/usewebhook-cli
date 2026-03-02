// Package main boots the UseWebhook CLI tool.
package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fatih/color"
	"github.com/gorilla/websocket"
	"github.com/spf13/cobra"
)

// Global variables that can be set via LDFLAGS during build time
var (
	// Version is set during release
	Version          = "dev"
	APIURL           = "https://usewebhook.com/api/webhooks/"
	BaseURL          = "https://usewebhook.com"
	WSURL            = "wss://usewebhook.com/ws/webhook/"
	SettingsFilename = ".usewebhook"
)

// WebhookRequest represents a single webhook request
type WebhookRequest struct {
	RequestID   string            `json:"request_id"`
	Timestamp   string            `json:"timestamp"`
	IP          string            `json:"ip"`
	CountryCode string            `json:"country_code"`
	UserAgent   string            `json:"user_agent"`
	Method      string            `json:"method"`
	Scheme      string            `json:"scheme"`
	Hostname    string            `json:"hostname"`
	Path        string            `json:"path"`
	Query       string            `json:"query"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
}

// WebhookResponse represents the response from the webhook HTTP API
type WebhookResponse struct {
	Requests []WebhookRequest `json:"requests"`
}

// WSMessage is the envelope for WebSocket messages from the server.
// type "webhook.init" contains historical Requests; type "webhook.new" contains a single Request.
type WSMessage struct {
	Type     string           `json:"type"`
	Requests []WebhookRequest `json:"requests"`
	Request  *WebhookRequest  `json:"request"`
}

// Config represents the user's configuration
type Config struct {
	WebhookHistory []string `json:"webhook_history"`
	LastUsed       string   `json:"last_used"`
}

// AppConfig holds the configuration for the current run of the application
type AppConfig struct {
	FullLog      bool
	ForwardTo    string
	WebhookID    string
	RequestID    string
	InitialSleep time.Duration
}

// fetchWebhookData retrieves webhook data from the API
func fetchWebhookData(webhookID string, params url.Values) (*WebhookResponse, error) {
	requestURL := APIURL + webhookID
	if len(params) > 0 {
		requestURL += "?" + params.Encode()
	}

	resp, err := http.Get(requestURL)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unable to fetch webhook data (status code %d)", resp.StatusCode)
	}

	var webhookResp WebhookResponse
	if err := json.NewDecoder(resp.Body).Decode(&webhookResp); err != nil {
		return nil, err
	}

	return &webhookResp, nil
}

// getValueOrEmpty returns a string representation of the value or "(empty)" if it's nil or an empty string
func getValueOrEmpty(value interface{}) string {
	if value == nil || value == "" {
		return "(empty)"
	}

	switch v := value.(type) {
	case string:
		return v
	case []byte:
		return string(v)
	default:
		return fmt.Sprintf("%v", v)
	}
}

// logRequest logs the details of a webhook request
func logRequest(request WebhookRequest, fullLog bool) {
	if fullLog {
		color.Yellow("\n=== Start of Request ID: %s ===\n", request.RequestID)
		color.Cyan("Timestamp: %s", color.HiBlackString(request.Timestamp))
		color.Cyan("Source IP (anonymized): %s", color.HiBlackString(getValueOrEmpty(request.IP)))
		color.Cyan("Method: %s", color.HiBlackString(getValueOrEmpty(request.Method)))
		color.Cyan("Query: %s", color.HiBlackString(getValueOrEmpty(request.Query)))
		color.Cyan("Headers: %s", color.HiBlackString(getValueOrEmpty(prettyJSON(request.Headers))))
		color.Cyan("Body: %s", color.HiBlackString(getValueOrEmpty(request.Body)))
		color.Yellow("=== End of Request ID: %s ===\n", request.RequestID)
	} else {
		// Log in one line
		color.Yellow("[INCOMING] %s%s %s%s %s%s %s%s",
			color.HiBlackString("timestamp="), getValueOrEmpty(request.Timestamp),
			color.HiBlackString("ip="), getValueOrEmpty(request.IP),
			color.HiBlackString("method="), getValueOrEmpty(request.Method),
			color.HiBlackString("request_id="), getValueOrEmpty(request.RequestID),
		)
	}
}

// prettyJSON returns a formatted JSON string
func prettyJSON(v interface{}) string {
	jsonData, err := json.MarshalIndent(v, "", "    ")
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(jsonData)
}

// forwardRequest forwards the webhook request to the specified URL
func forwardRequest(request WebhookRequest, forwardTo string) {
	client := &http.Client{}

	reqURL := forwardTo
	if request.Query != "" {
		reqURL += "?" + request.Query
	}

	req, err := http.NewRequest(request.Method, reqURL, strings.NewReader(request.Body))
	if err != nil {
		color.Red("Error creating forward request: %v", err)
		return
	}

	// Set headers
	for k, v := range request.Headers {
		req.Header.Set(k, v)
	}

	// Handle Base64 content-type logic
	if req.Header.Get("Content-Type") == "application/base64" {
		decodedBody, originalContentType, err := decodeBase64Body(request.Body)
		if err != nil {
			color.Red("Error decoding Base64 body: %v", err)
			return
		}
		req.Body = io.NopCloser(strings.NewReader(decodedBody)) // Set decoded body
		req.Header.Set("Content-Type", originalContentType)     // Set original content-type
		req.Header.Del("X-Original-Content-Type")
	}

	parsedURL, err := url.Parse(forwardTo)
	if err != nil {
		color.Red("Error parsing forward URL: %v", err)
		return
	}
	req.Header.Set("Host", parsedURL.Host)

	start := time.Now()
	resp, err := client.Do(req)
	if err != nil {
		color.Red("Error forwarding request to %s: %v", reqURL, err)
		return
	}
	defer resp.Body.Close()

	duration := time.Since(start)
	color.Blue("[FORWARDED] %s%d %s%dms %s%s", color.HiBlackString("status="), resp.StatusCode, color.HiBlackString("time="), duration.Milliseconds(), color.HiBlackString("destination="), reqURL)
}

// decodeBase64Body decodes a Base64 encoded body and extracts the original content-type
func decodeBase64Body(encodedBody string) (string, string, error) {
	decoded, err := base64.StdEncoding.DecodeString(encodedBody)
	if err != nil {
		return "", "", err
	}

	// Extract the original content-type from the decoded body
	originalContentType := http.DetectContentType(decoded)
	return string(decoded), originalContentType, nil
}

// fetchSingleRequest fetches a specific request by ID from the HTTP API and exits
func fetchSingleRequest(config AppConfig) {
	params := url.Values{}
	params.Set("request_id", config.RequestID)

	webhookData, err := fetchWebhookData(config.WebhookID, params)
	if err != nil {
		color.Red("Error fetching webhook data: %v", err)
		os.Exit(1)
	}

	if len(webhookData.Requests) == 0 {
		color.Red("No requests found for request ID: %s", config.RequestID)
		os.Exit(1)
	}

	for _, request := range webhookData.Requests {
		logRequest(request, config.FullLog)
		if config.ForwardTo != "" {
			forwardRequest(request, config.ForwardTo)
		}
	}
	os.Exit(0)
}

// connectAndListen opens a WebSocket connection and dispatches incoming requests until an error occurs.
// seen tracks request IDs already processed; isFirstConnect suppresses the history batch on the initial connection.
func connectAndListen(config AppConfig, seen map[string]bool, isFirstConnect *bool) error {
	conn, _, err := websocket.DefaultDialer.Dial(WSURL+config.WebhookID, http.Header{
		"Origin": []string{BaseURL},
	})
	if err != nil {
		return err
	}
	defer conn.Close()

	for {
		_, message, err := conn.ReadMessage()
		if err != nil {
			return err
		}

		var msg WSMessage
		if err := json.Unmarshal(message, &msg); err != nil {
			color.Yellow("Warning: failed to parse message: %v", err)
			continue
		}

		switch msg.Type {
		case "webhook.init":
			for _, req := range msg.Requests {
				if *isFirstConnect {
					// Mark historical requests as seen without displaying them
					seen[req.RequestID] = true
				} else if !seen[req.RequestID] {
					// Requests that arrived while we were disconnected
					seen[req.RequestID] = true
					logRequest(req, config.FullLog)
					if config.ForwardTo != "" {
						forwardRequest(req, config.ForwardTo)
					}
				}
			}
			*isFirstConnect = false

		case "webhook.new":
			if msg.Request != nil && !seen[msg.Request.RequestID] {
				seen[msg.Request.RequestID] = true
				logRequest(*msg.Request, config.FullLog)
				if config.ForwardTo != "" {
					forwardRequest(*msg.Request, config.ForwardTo)
				}
			}
		}
	}
}

// listenWebSocket connects via WebSocket and reconnects automatically on disconnect.
// The seen map and isFirstConnect flag persist across reconnects to avoid replaying requests.
func listenWebSocket(config AppConfig) {
	seen := make(map[string]bool)
	isFirstConnect := true

	for {
		err := connectAndListen(config, seen, &isFirstConnect)
		if err != nil {
			color.Red("WebSocket error: %v. Reconnecting...", err)
			time.Sleep(config.InitialSleep)
		}
	}
}

// extractIdsFromURLOrArgs extracts the webhook ID and optionally the request ID from various URL patterns
func extractIdsFromURLOrArgs(webhookURL string) (string, string, error) {
	re := regexp.MustCompile(`^(?:https?://)?(?:usewebhook\.com/)?([0-9a-fA-F]{32})(?:\?.*)?$`)

	if matches := re.FindStringSubmatch(webhookURL); matches != nil {
		return matches[1], "", nil
	}

	parsedURL, err := url.Parse(webhookURL)
	if err != nil {
		return "", "", fmt.Errorf("invalid URL format")
	}

	queryParams := parsedURL.Query()
	webhookID := queryParams.Get("id")
	requestID := queryParams.Get("req")

	if webhookID == "" {
		return "", "", fmt.Errorf("invalid webhook ID")
	}

	return webhookID, requestID, nil
}

// getConfigFilePath returns the path to the config file
func getConfigFilePath() string {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		color.Red("Error getting user home directory: %v", err)
		return ""
	}
	return filepath.Join(homeDir, SettingsFilename)
}

// loadConfig loads the user's configuration from the config file
func loadConfig() (*Config, error) {
	configPath := getConfigFilePath()
	if configPath == "" {
		return nil, fmt.Errorf("unable to determine config file path")
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		if os.IsNotExist(err) {
			return &Config{}, nil
		}
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// saveConfig saves the user's configuration to the config file
func saveConfig(config *Config) error {
	configPath := getConfigFilePath()
	if configPath == "" {
		return fmt.Errorf("unable to determine config file path")
	}

	data, err := json.MarshalIndent(config, "", "  ")
	if err != nil {
		return err
	}

	return os.WriteFile(configPath, data, 0600)
}

// createRootCommand creates and returns the root command for the CLI
func createRootCommand() *cobra.Command {
	appConfig := AppConfig{
		InitialSleep: 1 * time.Second,
	}

	rootCmd := &cobra.Command{
		Use:     "usewebhook <webhook ID or URL>",
		Short:   "Capture and inspect webhooks from your browser. Replay them on localhost.",
		Version: Version,
		Example: "usewebhook  # creates a new webhook\nusewebhook <webhook ID>\nusewebhook <webhook ID> --log-details\nusewebhook <webhook ID> --request-id <request ID>\nusewebhook <webhook ID> --forward-to http://localhost:3000/your-endpoint",
		Run: func(cmd *cobra.Command, args []string) {
			runRootCommand(cmd, args, &appConfig)
		},
	}

	rootCmd.Flags().StringVarP(&appConfig.RequestID, "request-id", "r", "", "the request ID to fetch (optional)")
	rootCmd.Flags().StringVarP(&appConfig.ForwardTo, "forward-to", "f", "", "forward incoming requests to the provided URL (optional)")
	rootCmd.Flags().BoolVarP(&appConfig.FullLog, "log-details", "l", false, "log full request details (default: false)")

	return rootCmd
}

// runRootCommand executes the main logic of the CLI
func runRootCommand(cmd *cobra.Command, args []string, appConfig *AppConfig) {
	config, err := loadConfig()
	if err != nil {
		color.Red("Error loading config: %v", err)
		os.Exit(1)
	}

	if len(args) == 0 {
		// If no webhook ID or URL is provided, use the last used webhook ID
		appConfig.WebhookID = config.LastUsed
		if appConfig.WebhookID == "" {
			// If still no webhook ID, create a new one
			randomBytes := make([]byte, 16)
			_, err := rand.Read(randomBytes)
			if err != nil {
				color.Red("Error generating random webhook ID: %v", err)
				os.Exit(1)
			}
			appConfig.WebhookID = hex.EncodeToString(randomBytes)
			color.HiBlack("No webhook ID or URL provided, creating a new one...")
		}
	} else {
		// If a webhook ID or URL is provided, extract the webhook ID and optionally the request ID
		webhookID, requestID, err := extractIdsFromURLOrArgs(args[0])
		if err != nil {
			color.Red("Error parsing webhook URL: %v", err)
			os.Exit(1)
		}
		appConfig.WebhookID = webhookID
		if requestID != "" {
			appConfig.RequestID = requestID
		}
	}

	// Update config
	config.LastUsed = appConfig.WebhookID
	if !contains(config.WebhookHistory, appConfig.WebhookID) {
		config.WebhookHistory = append(config.WebhookHistory, appConfig.WebhookID)
	}
	if err := saveConfig(config); err != nil {
		color.Yellow("Warning: Unable to save config: %v", err)
	}

	if appConfig.RequestID != "" {
		color.Green("Single request mode. Retrieving webhook=%s request=%s\n\n", appConfig.WebhookID, appConfig.RequestID)
		fetchSingleRequest(*appConfig)
	} else {
		color.Green("Dashboard: %s/?id=%s", BaseURL, appConfig.WebhookID)
		color.Green("Webhook URL: %s/%s", BaseURL, appConfig.WebhookID)
		if appConfig.ForwardTo != "" {
			color.Green("Forwarding to: %s", appConfig.ForwardTo)
		}
		color.HiBlack("\nPress Ctrl+C to stop\n\n")
		listenWebSocket(*appConfig)
	}
}

// contains checks if a slice contains a specific item
func contains(slice []string, item string) bool {
	for _, s := range slice {
		if s == item {
			return true
		}
	}
	return false
}

// main is the entry point of the application
func main() {
	rootCmd := createRootCommand()
	if err := rootCmd.Execute(); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
