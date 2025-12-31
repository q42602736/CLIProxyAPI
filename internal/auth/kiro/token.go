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

// kiroAccountManagerExport represents the JSON format exported by kiro-account-manager.
// This struct is used to detect and convert the external format to our internal format.
type kiroAccountManagerExport struct {
	Email        string `json:"email,omitempty"`
	Provider     string `json:"provider,omitempty"`
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ClientIDHash string `json:"clientIdHash,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	Region       string `json:"region,omitempty"`
	Label        string `json:"label,omitempty"`
	MachineID    string `json:"machineId,omitempty"`
}

// isKiroAccountManagerFormat checks if the JSON data is from kiro-account-manager export.
// It detects the format by checking for kiro-account-manager specific fields.
func isKiroAccountManagerFormat(data []byte) bool {
	var raw map[string]interface{}
	if err := json.Unmarshal(data, &raw); err != nil {
		return false
	}
	// kiro-account-manager exports have "provider" field (Google/GitHub/BuilderId)
	// and may have "clientIdHash", "label", "machineId" fields
	_, hasProvider := raw["provider"]
	_, hasClientIdHash := raw["clientIdHash"]
	_, hasLabel := raw["label"]
	_, hasMachineId := raw["machineId"]
	// Must have provider and at least one of the kiro-account-manager specific fields
	return hasProvider && (hasClientIdHash || hasLabel || hasMachineId)
}

// convertFromKiroAccountManager converts kiro-account-manager export format to KiroTokenStorage.
func convertFromKiroAccountManager(data []byte) (*KiroTokenStorage, error) {
	var export kiroAccountManagerExport
	if err := json.Unmarshal(data, &export); err != nil {
		return nil, fmt.Errorf("failed to parse kiro-account-manager format: %w", err)
	}

	// Determine auth method based on provider
	authMethod := "idc" // default to idc
	if export.Provider == "Google" || export.Provider == "GitHub" || export.Provider == "Github" {
		authMethod = "social"
	}

	// For social auth, clientId and clientSecret are not needed
	// For idc auth (BuilderId/Enterprise), they are required
	storage := &KiroTokenStorage{
		AccessToken:  export.AccessToken,
		RefreshToken: export.RefreshToken,
		ClientID:     export.ClientID,
		ClientSecret: export.ClientSecret,
		AuthMethod:   authMethod,
		Region:       export.Region,
		Type:         "kiro",
	}

	// Set default region if not specified
	if storage.Region == "" {
		storage.Region = "us-east-1"
	}

	return storage, nil
}

// LoadTokenFromFile loads Kiro token storage from a JSON file.
// It automatically detects and converts kiro-account-manager export format.
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

	// Check if this is kiro-account-manager export format
	if isKiroAccountManagerFormat(data) {
		return convertFromKiroAccountManager(data)
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
