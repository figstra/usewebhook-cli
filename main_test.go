package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

// sharedWebhookID is a fixed test webhook — reused across all live tests
const sharedWebhookID = "a1b2c3d4e5f6a1b2c3d4e5f6a1b2c3d4"

func dialTestWS(t *testing.T) *websocket.Conn {
	t.Helper()
	conn, _, err := websocket.DefaultDialer.Dial(WSURL+sharedWebhookID, http.Header{
		"Origin": []string{BaseURL},
	})
	if err != nil {
		t.Fatalf("failed to connect to WebSocket: %v", err)
	}
	return conn
}

func readWSMessage(t *testing.T, conn *websocket.Conn, timeout time.Duration) WSMessage {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(timeout))
	_, raw, err := conn.ReadMessage()
	if err != nil {
		t.Fatalf("failed to read WebSocket message: %v", err)
	}
	var msg WSMessage
	if err := json.Unmarshal(raw, &msg); err != nil {
		t.Fatalf("failed to unmarshal message: %v\nraw: %s", err, raw)
	}
	return msg
}

func sendWebhookRequest(t *testing.T, method, body string) {
	t.Helper()
	url := fmt.Sprintf("%s/%s", BaseURL, sharedWebhookID)
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	if err != nil {
		t.Fatalf("failed to create HTTP request: %v", err)
	}
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("failed to send webhook request: %v", err)
	}
	defer resp.Body.Close()
}

// TestWSConnectReceivesInit verifies that connecting to the WebSocket returns a webhook.init message
func TestWSConnectReceivesInit(t *testing.T) {
	conn := dialTestWS(t)
	defer conn.Close()

	msg := readWSMessage(t, conn, 5*time.Second)

	if msg.Type != "webhook.init" {
		t.Errorf("expected type webhook.init, got %q", msg.Type)
	}
}

// TestWSReceivesNewRequestOnHTTPPost verifies that sending an HTTP request triggers a webhook.new message
func TestWSReceivesNewRequestOnHTTPPost(t *testing.T) {
	conn := dialTestWS(t)
	defer conn.Close()

	// Consume the init message
	readWSMessage(t, conn, 5*time.Second)

	// Send a POST request to the webhook URL
	payload := `{"test": "live-integration"}`
	sendWebhookRequest(t, http.MethodPost, payload)

	msg := readWSMessage(t, conn, 10*time.Second)

	if msg.Type != "webhook.new" {
		t.Errorf("expected type webhook.new, got %q", msg.Type)
	}
	if msg.Request == nil {
		t.Fatal("expected request to be non-nil")
	}
	if msg.Request.Method != http.MethodPost {
		t.Errorf("expected method POST, got %q", msg.Request.Method)
	}
	if !strings.Contains(msg.Request.Body, "live-integration") {
		t.Errorf("expected body to contain 'live-integration', got %q", msg.Request.Body)
	}
}

// TestWSReceivesNewRequestOnHTTPGet verifies a GET request also triggers webhook.new
func TestWSReceivesNewRequestOnHTTPGet(t *testing.T) {
	conn := dialTestWS(t)
	defer conn.Close()

	// Consume the init message
	readWSMessage(t, conn, 5*time.Second)

	sendWebhookRequest(t, http.MethodGet, "")

	msg := readWSMessage(t, conn, 10*time.Second)

	if msg.Type != "webhook.new" {
		t.Errorf("expected type webhook.new, got %q", msg.Type)
	}
	if msg.Request == nil {
		t.Fatal("expected request to be non-nil")
	}
	if msg.Request.Method != http.MethodGet {
		t.Errorf("expected method GET, got %q", msg.Request.Method)
	}
}

// TestExtractIdsFromURLOrArgs covers the various input formats accepted by the CLI
func TestExtractIdsFromURLOrArgs(t *testing.T) {
	id := "409bdb1f81abfa826c2022d18ddff2e5"

	cases := []struct {
		input     string
		wantID    string
		wantReqID string
		wantErr   bool
	}{
		{id, id, "", false},
		{"https://usewebhook.com/" + id, id, "", false},
		{"http://usewebhook.com/" + id, id, "", false},
		{"usewebhook.com/" + id, id, "", false},
		{"https://usewebhook.com/?id=" + id + "&req=abc123", id, "abc123", false},
		{"not-a-valid-id", "", "", true},
		{"", "", "", true},
	}

	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			gotID, gotReqID, err := extractIdsFromURLOrArgs(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got id=%q", gotID)
				}
				return
			}
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}
			if gotID != tc.wantID {
				t.Errorf("webhook ID: want %q, got %q", tc.wantID, gotID)
			}
			if gotReqID != tc.wantReqID {
				t.Errorf("request ID: want %q, got %q", tc.wantReqID, gotReqID)
			}
		})
	}
}
