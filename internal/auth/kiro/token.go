// Package kiro provides authentication and token management functionality
// for Kiro (AWS CodeWhisperer) AI services.
package kiro

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
)

// KiroTokenStorage stores OAuth2 token information for Kiro API authentication.
// It maintains compatibility with the existing auth system while adding Kiro-specific fields
// for managing access tokens, refresh tokens, and AWS-related information.
type KiroTokenStorage struct {
	// AccessToken is the OAuth2 access token used for authenticating API requests.
	AccessToken string `json:"accessToken"`

	// RefreshToken is used to obtain new access tokens when the current one expires.
	RefreshToken string `json:"refreshToken"`

	// ClientID is the OAuth client ID (for IDC auth method).
	ClientID string `json:"clientId,omitempty"`

	// ClientSecret is the OAuth client secret (for IDC auth method).
	ClientSecret string `json:"clientSecret,omitempty"`

	// AuthMethod indicates the authentication method ("social" or "idc").
	AuthMethod string `json:"authMethod,omitempty"`

	// ExpiresAt is the timestamp when the current access token expires.
	ExpiresAt string `json:"expiresAt,omitempty"`

	// ProfileArn is the AWS profile ARN for social auth method.
	ProfileArn string `json:"profileArn,omitempty"`

	// Region is the AWS region for API calls.
	Region string `json:"region,omitempty"`

	// LastRefresh is the timestamp of the last token refresh operation.
	LastRefresh string `json:"last_refresh,omitempty"`

	// Type indicates the authentication provider type, always "kiro" for this storage.
	Type string `json:"type"`
}

// SaveTokenToFile serializes the Kiro token storage to a JSON file.
// This method creates the necessary directory structure and writes the token
// data in JSON format to the specified file path for persistent storage.
//
// Parameters:
//   - authFilePath: The full path where the token file should be saved
//
// Returns:
//   - error: An error if the operation fails, nil otherwise
func (ts *KiroTokenStorage) SaveTokenToFile(authFilePath string) error {
	misc.LogSavingCredentials(authFilePath)
	ts.Type = "kiro"

	// Create directory structure if it doesn't exist
	if err := os.MkdirAll(filepath.Dir(authFilePath), 0700); err != nil {
		return fmt.Errorf("failed to create directory: %v", err)
	}

	// Create the token file
	f, err := os.Create(authFilePath)
	if err != nil {
		return fmt.Errorf("failed to create token file: %w", err)
	}
	defer func() {
		_ = f.Close()
	}()

	// Encode and write the token data as JSON
	encoder := json.NewEncoder(f)
	encoder.SetIndent("", "  ")
	if err = encoder.Encode(ts); err != nil {
		return fmt.Errorf("failed to write token to file: %w", err)
	}
	return nil
}

// LoadTokenFromFile loads Kiro token storage from a JSON file.
//
// Parameters:
//   - authFilePath: The full path to the token file
//
// Returns:
//   - *KiroTokenStorage: The loaded token storage
//   - error: An error if the operation fails, nil otherwise
func LoadTokenFromFile(authFilePath string) (*KiroTokenStorage, error) {
	data, err := os.ReadFile(authFilePath)
	if err != nil {
		return nil, fmt.Errorf("failed to read token file: %w", err)
	}

	var storage KiroTokenStorage
	if err = json.Unmarshal(data, &storage); err != nil {
		return nil, fmt.Errorf("failed to parse token file: %w", err)
	}

	return &storage, nil
}

// ToTokenData converts KiroTokenStorage to KiroTokenData
func (ts *KiroTokenStorage) ToTokenData() *KiroTokenData {
	return &KiroTokenData{
		AccessToken:  ts.AccessToken,
		RefreshToken: ts.RefreshToken,
		ClientID:     ts.ClientID,
		ClientSecret: ts.ClientSecret,
		AuthMethod:   ts.AuthMethod,
		ExpiresAt:    ts.ExpiresAt,
		ProfileArn:   ts.ProfileArn,
		Region:       ts.Region,
	}
}

// FromTokenData creates a KiroTokenStorage from KiroTokenData
func FromTokenData(td *KiroTokenData, lastRefresh string) *KiroTokenStorage {
	return &KiroTokenStorage{
		AccessToken:  td.AccessToken,
		RefreshToken: td.RefreshToken,
		ClientID:     td.ClientID,
		ClientSecret: td.ClientSecret,
		AuthMethod:   td.AuthMethod,
		ExpiresAt:    td.ExpiresAt,
		ProfileArn:   td.ProfileArn,
		Region:       td.Region,
		LastRefresh:  lastRefresh,
		Type:         "kiro",
	}
}
