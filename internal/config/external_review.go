package config

import "fmt"

func validateRequiredReviewBotID(botID *string) error {
	if botID != nil && *botID == "" {
		return fmt.Errorf("ci.required_review_bot_id must not be empty")
	}
	return nil
}

func validateRequiredReviewAppID(appID *int64) error {
	if appID != nil && *appID <= 0 {
		return fmt.Errorf("ci.required_review_app_id must be a positive GitHub App ID")
	}
	return nil
}

func validateRequiredReviews(reviewers []string) error {
	seen := map[string]bool{}
	for _, reviewer := range reviewers {
		if reviewer != "coderabbit" {
			return fmt.Errorf("ci.required_reviews: unsupported reviewer %q (supported: coderabbit)", reviewer)
		}
		if seen[reviewer] {
			return fmt.Errorf("ci.required_reviews: duplicate reviewer %q", reviewer)
		}
		seen[reviewer] = true
	}
	return nil
}
