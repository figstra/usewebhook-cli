package main

import (
	"testing"
)

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
