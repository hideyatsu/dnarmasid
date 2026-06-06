package main

import (
	"dnarmasid/shared/config"
)

// PostType defines how the content is published
type PostType string

const (
	PostTypeImage PostType = "image" // single image
	PostTypeAlbum PostType = "album" // multiple images (carousel)
)

// PlatformTarget represents a social media platform to publish to
type PlatformTarget struct {
	Name      string   // "tiktok", "instagram", "facebook"
	AccountID string   // Repliz account ID
	PostType  PostType // "image" or "album"
}

// getActivePlatforms returns list of platforms with configured account IDs
func getActivePlatforms(cfg *config.Config) []PlatformTarget {
	var platforms []PlatformTarget

	if cfg.ReplizTikTokAccountID != "" {
		platforms = append(platforms, PlatformTarget{
			Name:      "tiktok",
			AccountID: cfg.ReplizTikTokAccountID,
			PostType:  PostTypeAlbum,
		})
	}

	if cfg.ReplizInstagramAccountID != "" {
		platforms = append(platforms, PlatformTarget{
			Name:      "instagram",
			AccountID: cfg.ReplizInstagramAccountID,
			PostType:  PostTypeImage,
		})
	}

	// Facebook — single image, same as Instagram
	if cfg.ReplizFacebookAccountID != "" {
		platforms = append(platforms, PlatformTarget{
			Name:      "facebook",
			AccountID: cfg.ReplizFacebookAccountID,
			PostType:  PostTypeImage,
		})
	}

	return platforms
}
