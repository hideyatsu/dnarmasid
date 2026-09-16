package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"dnarmasid/shared/models"
)

func TestBuildSuccessText(t *testing.T) {
	timeStr := "2026-09-16 16:45:00 WIB"
	tests := []struct {
		name       string
		action     string
		price1g    int64
		wantAction string
		wantPrice  string
	}{
		{
			name:       "new data ingested",
			action:     "New data ingested",
			price1g:    1850000,
			wantAction: "• <b>Action:</b> New data ingested",
			wantPrice:  "• <b>Price 1g:</b> Rp 1.850.000",
		},
		{
			name:       "skipped up to date",
			action:     "Skipped - up to date",
			price1g:    1850000,
			wantAction: "• <b>Action:</b> Skipped - up to date",
			wantPrice:  "• <b>Price 1g:</b> Rp 1.850.000",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSuccessText(tt.action, tt.price1g, timeStr)
			if !strings.Contains(got, "📊 <b>[dnarmasid-scraper] Gold Price Scrape Succeeded</b>") {
				t.Errorf("missing header: %s", got)
			}
			if !strings.Contains(got, "• <b>Status:</b> 🟢 SUCCESS") {
				t.Errorf("missing status: %s", got)
			}
			if !strings.Contains(got, tt.wantAction) {
				t.Errorf("missing action %q: %s", tt.wantAction, got)
			}
			if !strings.Contains(got, tt.wantPrice) {
				t.Errorf("missing price %q: %s", tt.wantPrice, got)
			}
			if !strings.Contains(got, "• <b>Time:</b> "+timeStr) {
				t.Errorf("missing time: %s", got)
			}
		})
	}
}

func TestBuildFailureText(t *testing.T) {
	timeStr := "2026-09-16 16:45:00 WIB"
	err := errors.New("connection failed <timeout & reset>")

	got := buildFailureText(err, timeStr)
	if !strings.Contains(got, "⚠️ <b>[dnarmasid-scraper] Gold Price Scrape Failed</b>") {
		t.Errorf("missing header: %s", got)
	}
	if !strings.Contains(got, "• <b>Status:</b> 🔴 FAILED") {
		t.Errorf("missing status: %s", got)
	}
	// HTML escaping check
	expectedErrTag := "• <b>Error:</b> <code>connection failed &lt;timeout &amp; reset&gt;</code>"
	if !strings.Contains(got, expectedErrTag) {
		t.Errorf("error not properly escaped. want %s, got %s", expectedErrTag, got)
	}
	if !strings.Contains(got, "• <b>Time:</b> "+timeStr) {
		t.Errorf("missing time: %s", got)
	}
}

func TestFormatRupiah(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{500, "500"},
		{1000, "1.000"},
		{50000, "50.000"},
		{950000, "950.000"},
		{1850000, "1.850.000"},
		{18050000, "18.050.000"},
		{-1000, "-1.000"},
	}

	for _, tt := range tests {
		got := formatRupiah(tt.input)
		if got != tt.want {
			t.Errorf("formatRupiah(%d) = %q; want %q", tt.input, got, tt.want)
		}
	}
}

func TestExtractPrice1g(t *testing.T) {
	prices := []models.GoldPrice{
		{Gram: 0.5, BuyPrice: 950000},
		{Gram: 1.0, BuyPrice: 1850000},
		{Gram: 2.0, BuyPrice: 3650000},
	}
	if got := extractPrice1g(prices); got != 1850000 {
		t.Errorf("extractPrice1g = %d; want 1850000", got)
	}

	emptyPrices := []models.GoldPrice{
		{Gram: 5.0, BuyPrice: 9000000},
	}
	if got := extractPrice1g(emptyPrices); got != 0 {
		t.Errorf("extractPrice1g = %d; want 0", got)
	}
}

func TestSendHTTPRequest(t *testing.T) {
	var receivedAuth string
	var receivedService string
	var receivedBody map[string]string

	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedAuth = r.Header.Get("Authorization")
		receivedService = r.Header.Get("X-Service-Name")
		bodyBytes, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(bodyBytes, &receivedBody)
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"success":true}`))
	}))
	defer ts.Close()

	payload := map[string]string{
		"source":     "dnarmasid-scraper",
		"target":     "log",
		"channel":    "telegram",
		"priority":   "default",
		"parse_mode": "HTML",
		"text":       "Test notification",
	}

	err := sendHTTPRequest(ts.URL, "test_token_123", payload)
	if err != nil {
		t.Fatalf("sendHTTPRequest returned error: %v", err)
	}

	if receivedAuth != "Bearer test_token_123" {
		t.Errorf("auth header mismatch: got %q", receivedAuth)
	}
	if receivedService != "dnarmasid-scraper" {
		t.Errorf("service header mismatch: got %q", receivedService)
	}
	if receivedBody["source"] != "dnarmasid-scraper" {
		t.Errorf("payload source mismatch: got %q", receivedBody["source"])
	}
	if receivedBody["target"] != "log" {
		t.Errorf("payload target mismatch: got %q", receivedBody["target"])
	}
	if receivedBody["channel"] != "telegram" {
		t.Errorf("payload channel mismatch: got %q", receivedBody["channel"])
	}
	if receivedBody["parse_mode"] != "HTML" {
		t.Errorf("payload parse_mode mismatch: got %q", receivedBody["parse_mode"])
	}
	if receivedBody["text"] != "Test notification" {
		t.Errorf("payload text mismatch: got %q", receivedBody["text"])
	}
}
