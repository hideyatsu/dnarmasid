package repliz

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"dnarmasid/shared/config"
)

// Media represents a single media item in the Repliz payload
type Media struct {
	Alt             string `json:"alt"`
	Type            string `json:"type"`
	Thumbnail       string `json:"thumbnail"`
	URL             string `json:"url"`
	CustomThumbnail bool   `json:"customThumbnail,omitempty"`
}

// Meta represents metadata for the post
type Meta struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	URL         string `json:"url"`
}

// Music represents music info for the post
type Music struct {
	ID        string `json:"id"`
	Artist    string `json:"artist"`
	Name      string `json:"name"`
	Thumbnail string `json:"thumbnail"`
}

// AdditionalInfo represents additional information for the post
type AdditionalInfo struct {
	IsAiGenerated bool     `json:"isAiGenerated"`
	IsDraft       bool     `json:"isDraft"`
	Collaborators []string `json:"collaboratos"` // Based on provided payload, note the spelling
	Music         Music    `json:"music"`
}

// Payload represents the Repliz API request body
type Payload struct {
	Title          string         `json:"title"`
	Description    string         `json:"description"`
	Topic          string         `json:"topic"`
	Type           string         `json:"type"`
	Medias         []Media        `json:"medias"`
	Meta           Meta           `json:"meta"`
	AdditionalInfo AdditionalInfo `json:"additionalInfo"`
	Replies        []string       `json:"replies"` // Could be []struct{}, using []string for empty array based on payload
	AccountID      string         `json:"accountId"`
	ScheduleAt     string         `json:"scheduleAt"`
}

// Client represents the Repliz API client
type Client struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewClient creates a new Repliz API client
func NewClient(cfg *config.Config) *Client {
	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

// UploadPost sends the payload to the Repliz API
func (c *Client) UploadPost(payload Payload) error {
	url := "https://api.repliz.com/public/schedule"

	// Marshal payload to JSON
	reqBody, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	// Create request
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewBuffer(reqBody))
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	// Set headers
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	// Set Basic Auth
	if c.cfg.ReplizAccessKey != "" && c.cfg.ReplizSecretKey != "" {
		req.SetBasicAuth(c.cfg.ReplizAccessKey, c.cfg.ReplizSecretKey)
	} else {
		return fmt.Errorf("repliz credentials (access key / secret key) are missing")
	}

	// Execute request
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute request: %w", err)
	}
	defer resp.Body.Close()

	// Read response
	respBody, _ := io.ReadAll(resp.Body)

	// Check status code (assume 200/201 is success)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("repliz API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}
