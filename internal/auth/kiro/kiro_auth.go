// Package kiro provides OAuth2 authentication functionality for Kiro/AWS CodeWhisperer API.
// This package implements token refresh and credential loading from AWS SSO cache.
package kiro

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
)

const (
	defaultRegion     = "us-east-1"
	defaultCredPath   = ".aws/sso/cache"
	kiroAuthTokenFile = "kiro-auth-token.json"
)

// refreshTokenRequest represents the request body for token refresh
type refreshTokenRequest struct {
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	GrantType    string `json:"grantType,omitempty"`
}

// refreshTokenResponse represents the response from token refresh endpoint
type refreshTokenResponse struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ProfileArn   string `json:"profileArn,omitempty"`
	ExpiresIn    int    `json:"expiresIn"`
}

// KiroAuth handles Kiro authentication flow.
// It provides methods for loading credentials from AWS SSO cache,
// refreshing tokens, and managing authentication state.
type KiroAuth struct {
	httpClient *http.Client
	constants  KiroConstants
}

// NewKiroAuth creates a new Kiro authentication service.
// It initializes the HTTP client with proxy settings from the configuration.
//
// Parameters:
//   - cfg: The application configuration containing proxy settings
//
// Returns:
//   - *KiroAuth: A new Kiro authentication service instance
func NewKiroAuth(cfg *config.Config) *KiroAuth {
	return &KiroAuth{
		httpClient: util.SetProxy(&cfg.SDKConfig, &http.Client{
			Timeout: 5 * time.Minute,
		}),
		constants: DefaultConstants(),
	}
}

// LoadCredentialsFromDirectory loads Kiro credentials from the AWS SSO cache.
// It supports both file path and directory path.
//
// Parameters:
//   - credPath: The path to a credentials file or directory (defaults to ~/.aws/sso/cache)
//
// Returns:
//   - *KiroTokenData: The loaded token data
//   - error: An error if loading fails
func (k *KiroAuth) LoadCredentialsFromDirectory(credPath string) (*KiroTokenData, error) {
	// Expand ~ to home directory
	if strings.HasPrefix(credPath, "~/") {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		credPath = filepath.Join(homeDir, credPath[2:])
	}

	if credPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return nil, fmt.Errorf("failed to get home directory: %w", err)
		}
		credPath = filepath.Join(homeDir, defaultCredPath)
	}

	// Check if credPath is a file
	fileInfo, err := os.Stat(credPath)
	if err == nil && !fileInfo.IsDir() {
		// It's a file, load directly
		return k.loadCredentialsFromFile(credPath)
	}

	targetFilePath := filepath.Join(credPath, kiroAuthTokenFile)
	merged := &KiroTokenData{}

	// First try to read the target file
	if data, err := os.ReadFile(targetFilePath); err == nil {
		var creds KiroTokenData
		if err := json.Unmarshal(data, &creds); err == nil {
			merged = &creds
			log.Debugf("[Kiro Auth] Loaded credentials from %s", targetFilePath)
		}
	}

	// Then read other JSON files in the directory
	files, err := os.ReadDir(credPath)
	if err != nil {
		if merged.AccessToken != "" || merged.RefreshToken != "" {
			return merged, nil
		}
		return nil, fmt.Errorf("failed to read credentials directory: %w", err)
	}

	for _, file := range files {
		if !strings.HasSuffix(file.Name(), ".json") || file.Name() == kiroAuthTokenFile {
			continue
		}

		filePath := filepath.Join(credPath, file.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			continue
		}

		var creds KiroTokenData
		if err := json.Unmarshal(data, &creds); err != nil {
			continue
		}

		// Merge credentials, preserving expiresAt from the main file
		if creds.ClientID != "" && merged.ClientID == "" {
			merged.ClientID = creds.ClientID
		}
		if creds.ClientSecret != "" && merged.ClientSecret == "" {
			merged.ClientSecret = creds.ClientSecret
		}
		if creds.AuthMethod != "" && merged.AuthMethod == "" {
			merged.AuthMethod = creds.AuthMethod
		}
		if creds.Region != "" && merged.Region == "" {
			merged.Region = creds.Region
		}
		if creds.ProfileArn != "" && merged.ProfileArn == "" {
			merged.ProfileArn = creds.ProfileArn
		}
		// Only copy tokens if main file doesn't have them
		if creds.AccessToken != "" && merged.AccessToken == "" {
			merged.AccessToken = creds.AccessToken
		}
		if creds.RefreshToken != "" && merged.RefreshToken == "" {
			merged.RefreshToken = creds.RefreshToken
		}
		log.Debugf("[Kiro Auth] Loaded additional credentials from %s", file.Name())
	}

	if merged.AccessToken == "" && merged.RefreshToken == "" {
		return nil, fmt.Errorf("no valid credentials found in %s", credPath)
	}

	// Set default region if not specified
	if merged.Region == "" {
		merged.Region = defaultRegion
	}

	return merged, nil
}

// loadCredentialsFromFile is a private helper that loads credentials from a single file.
// It automatically detects and converts kiro-account-manager export format.
func (k *KiroAuth) loadCredentialsFromFile(filePath string) (*KiroTokenData, error) {
	data, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read credentials file: %w", err)
	}

	// Check if this is kiro-account-manager export format
	if isKiroAccountManagerFormat(data) {
		storage, err := convertFromKiroAccountManager(data)
		if err != nil {
			return nil, err
		}
		log.Debugf("[Kiro Auth] Loaded kiro-account-manager format credentials from file: %s", filePath)
		return storage.ToTokenData(), nil
	}

	var creds KiroTokenData
	if err := json.Unmarshal(data, &creds); err != nil {
		return nil, fmt.Errorf("failed to parse credentials file: %w", err)
	}

	// Set default region if not specified
	if creds.Region == "" {
		creds.Region = defaultRegion
	}

	log.Debugf("[Kiro Auth] Loaded credentials from file: %s", filePath)
	return &creds, nil
}

// LoadCredentialsFromFile loads Kiro credentials from a specific file.
//
// Parameters:
//   - filePath: The path to the credentials file
//
// Returns:
//   - *KiroTokenData: The loaded token data
//   - error: An error if loading fails
func (k *KiroAuth) LoadCredentialsFromFile(filePath string) (*KiroTokenData, error) {
	return k.loadCredentialsFromFile(filePath)
}

// RefreshTokens refreshes the access token using the refresh token.
// This method exchanges a valid refresh token for a new access token.
//
// Parameters:
//   - ctx: The context for the request
//   - tokenData: The current token data containing the refresh token
//
// Returns:
//   - *KiroTokenData: The new token data with updated access token
//   - error: An error if token refresh fails
func (k *KiroAuth) RefreshTokens(ctx context.Context, tokenData *KiroTokenData) (*KiroTokenData, error) {
	if tokenData.RefreshToken == "" {
		return nil, fmt.Errorf("refresh token is required")
	}

	region := tokenData.Region
	if region == "" {
		region = defaultRegion
	}

	// Determine refresh URL based on auth method
	var refreshURL string
	if tokenData.AuthMethod == AuthMethodSocial {
		refreshURL = strings.ReplaceAll(k.constants.RefreshURL, "{{region}}", region)
	} else {
		refreshURL = strings.ReplaceAll(k.constants.RefreshIDCURL, "{{region}}", region)
	}

	// Build request body
	reqBody := refreshTokenRequest{
		RefreshToken: tokenData.RefreshToken,
	}
	if tokenData.AuthMethod != AuthMethodSocial {
		reqBody.ClientID = tokenData.ClientID
		reqBody.ClientSecret = tokenData.ClientSecret
		reqBody.GrantType = "refresh_token"
	}

	jsonBody, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, "POST", refreshURL, strings.NewReader(string(jsonBody)))
	if err != nil {
		return nil, fmt.Errorf("failed to create refresh request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("token refresh request failed: %w", err)
	}
	defer func() {
		_ = resp.Body.Close()
	}()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read refresh response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("token refresh failed with status %d: %s", resp.StatusCode, string(body))
	}

	var tokenResp refreshTokenResponse
	if err = json.Unmarshal(body, &tokenResp); err != nil {
		return nil, fmt.Errorf("failed to parse token response: %w", err)
	}

	log.Info("[Kiro Auth] Access token refreshed successfully")

	// Calculate expiration time
	expiresAt := time.Now().Add(time.Duration(tokenResp.ExpiresIn) * time.Second).Format(time.RFC3339)

	// Create updated token data
	newTokenData := &KiroTokenData{
		AccessToken:  tokenResp.AccessToken,
		RefreshToken: tokenResp.RefreshToken,
		ClientID:     tokenData.ClientID,
		ClientSecret: tokenData.ClientSecret,
		AuthMethod:   tokenData.AuthMethod,
		ExpiresAt:    expiresAt,
		ProfileArn:   tokenResp.ProfileArn,
		Region:       region,
	}

	// Preserve profile ARN if not returned in response
	if newTokenData.ProfileArn == "" {
		newTokenData.ProfileArn = tokenData.ProfileArn
	}

	return newTokenData, nil
}

// SaveTokens saves the token data to the credentials file.
//
// Parameters:
//   - credPath: The path to the credentials directory
//   - tokenData: The token data to save
//
// Returns:
//   - error: An error if saving fails
func (k *KiroAuth) SaveTokens(credPath string, tokenData *KiroTokenData) error {
	if credPath == "" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return fmt.Errorf("failed to get home directory: %w", err)
		}
		credPath = filepath.Join(homeDir, defaultCredPath)
	}

	targetFilePath := filepath.Join(credPath, kiroAuthTokenFile)

	// Read existing data to merge
	existingData := make(map[string]interface{})
	if data, err := os.ReadFile(targetFilePath); err == nil {
		_ = json.Unmarshal(data, &existingData)
	}

	// Update with new token data
	existingData["accessToken"] = tokenData.AccessToken
	existingData["refreshToken"] = tokenData.RefreshToken
	existingData["expiresAt"] = tokenData.ExpiresAt
	if tokenData.ProfileArn != "" {
		existingData["profileArn"] = tokenData.ProfileArn
	}

	// Create directory if needed
	if err := os.MkdirAll(credPath, 0700); err != nil {
		return fmt.Errorf("failed to create credentials directory: %w", err)
	}

	// Write updated data
	jsonData, err := json.MarshalIndent(existingData, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal token data: %w", err)
	}

	if err := os.WriteFile(targetFilePath, jsonData, 0600); err != nil {
		return fmt.Errorf("failed to write token file: %w", err)
	}

	log.Infof("[Kiro Auth] Updated token file: %s", targetFilePath)
	return nil
}

// IsTokenExpiringSoon checks if the token is expiring within the specified minutes.
//
// Parameters:
//   - tokenData: The token data to check
//   - nearMinutes: The number of minutes to consider as "near expiry"
//
// Returns:
//   - bool: True if the token is expiring soon or already expired
func (k *KiroAuth) IsTokenExpiringSoon(tokenData *KiroTokenData, nearMinutes int) bool {
	if tokenData.ExpiresAt == "" {
		return false
	}

	expirationTime, err := time.Parse(time.RFC3339, tokenData.ExpiresAt)
	if err != nil {
		log.Errorf("[Kiro Auth] Error parsing expiry date: %v", err)
		return false
	}

	thresholdTime := time.Now().Add(time.Duration(nearMinutes) * time.Minute)
	return expirationTime.Before(thresholdTime)
}

// CreateTokenStorage creates a new KiroTokenStorage from token data.
//
// Parameters:
//   - tokenData: The token data to convert
//
// Returns:
//   - *KiroTokenStorage: A new token storage instance
func (k *KiroAuth) CreateTokenStorage(tokenData *KiroTokenData) *KiroTokenStorage {
	return &KiroTokenStorage{
		AccessToken:  tokenData.AccessToken,
		RefreshToken: tokenData.RefreshToken,
		ClientID:     tokenData.ClientID,
		ClientSecret: tokenData.ClientSecret,
		AuthMethod:   tokenData.AuthMethod,
		ExpiresAt:    tokenData.ExpiresAt,
		ProfileArn:   tokenData.ProfileArn,
		Region:       tokenData.Region,
		LastRefresh:  time.Now().Format(time.RFC3339),
		Type:         "kiro",
	}
}

// GetBaseURL returns the base URL for Kiro API calls.
//
// Parameters:
//   - region: The AWS region
//
// Returns:
//   - string: The base URL
func (k *KiroAuth) GetBaseURL(region string) string {
	if region == "" {
		region = defaultRegion
	}
	return strings.ReplaceAll(k.constants.BaseURL, "{{region}}", region)
}

// GetAmazonQURL returns the Amazon Q URL for Kiro API calls.
//
// Parameters:
//   - region: The AWS region
//
// Returns:
//   - string: The Amazon Q URL
func (k *KiroAuth) GetAmazonQURL(region string) string {
	if region == "" {
		region = defaultRegion
	}
	return strings.ReplaceAll(k.constants.AmazonQURL, "{{region}}", region)
}

// GetUsageLimits fetches the usage limits from the Kiro API.
//
// Parameters:
//   - ctx: The context for the request
//   - tokenData: The token data containing access token and profile info
//
// Returns:
//   - map[string]any: The usage limits response
//   - error: An error if fetching fails
func (k *KiroAuth) GetUsageLimits(ctx context.Context, tokenData *KiroTokenData) (map[string]any, error) {
	if tokenData.AccessToken == "" {
		return nil, fmt.Errorf("access token is required")
	}

	region := tokenData.Region
	if region == "" {
		region = defaultRegion
	}

	// Build URL with query parameters
	usageLimitsURL := strings.ReplaceAll(k.constants.UsageLimitsURL, "{{region}}", region)
	params := fmt.Sprintf("isEmailRequired=true&origin=%s&resourceType=AGENTIC_REQUEST", OriginAIEditor)
	if tokenData.AuthMethod == AuthMethodSocial && tokenData.ProfileArn != "" {
		params += "&profileArn=" + tokenData.ProfileArn
	}
	fullURL := usageLimitsURL + "?" + params

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	req.Header.Set("amz-sdk-invocation-id", fmt.Sprintf("%d", time.Now().UnixNano()))

	resp, err := k.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch usage limits: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage limits request failed with status %d: %s", resp.StatusCode, string(body))
	}

	var result map[string]any
	if err = json.Unmarshal(body, &result); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	log.Debug("[Kiro Auth] Usage limits fetched successfully")
	return result, nil
}
