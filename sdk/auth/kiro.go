package auth

import (
	"context"
	"fmt"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

// KiroAuthenticator implements the authentication flow for Kiro (AWS CodeWhisperer) accounts.
// Unlike traditional OAuth flows, Kiro uses AWS SSO cache credentials.
type KiroAuthenticator struct {
	CredPath string
}

// NewKiroAuthenticator constructs a Kiro authenticator with default settings.
func NewKiroAuthenticator() *KiroAuthenticator {
	return &KiroAuthenticator{}
}

func (a *KiroAuthenticator) Provider() string {
	return "kiro"
}

func (a *KiroAuthenticator) RefreshLead() *time.Duration {
	d := 30 * time.Minute
	return &d
}

func (a *KiroAuthenticator) Login(ctx context.Context, cfg *config.Config, opts *LoginOptions) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if opts == nil {
		opts = &LoginOptions{}
	}

	authSvc := kiro.NewKiroAuth(cfg)

	fmt.Println("Loading Kiro credentials from AWS SSO cache...")

	tokenData, err := authSvc.LoadCredentialsFromDirectory(a.CredPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Kiro credentials: %w", err)
	}

	if tokenData.AccessToken == "" {
		return nil, fmt.Errorf("no valid Kiro access token found in credentials")
	}

	// Check if token needs refresh
	if authSvc.IsTokenExpiringSoon(tokenData, 5) {
		log.Info("Kiro token is expiring soon, refreshing...")
		newTokenData, refreshErr := authSvc.RefreshTokens(ctx, tokenData)
		if refreshErr != nil {
			log.Warnf("Failed to refresh Kiro token: %v", refreshErr)
		} else {
			tokenData = newTokenData
			// Save refreshed tokens
			if saveErr := authSvc.SaveTokens(a.CredPath, tokenData); saveErr != nil {
				log.Warnf("Failed to save refreshed Kiro tokens: %v", saveErr)
			}
		}
	}

	tokenStorage := authSvc.CreateTokenStorage(tokenData)

	// Use region and profile ARN for identification
	identifier := "kiro"
	if tokenData.Region != "" {
		identifier = fmt.Sprintf("kiro-%s", tokenData.Region)
	}

	fileName := fmt.Sprintf("%s.json", identifier)
	metadata := map[string]any{
		"accessToken":  tokenData.AccessToken,
		"refreshToken": tokenData.RefreshToken,
		"expiresAt":    tokenData.ExpiresAt,
		"region":       tokenData.Region,
		"authMethod":   tokenData.AuthMethod,
		"type":         "kiro",
	}

	if tokenData.ProfileArn != "" {
		metadata["profileArn"] = tokenData.ProfileArn
	}
	if tokenData.ClientID != "" {
		metadata["clientId"] = tokenData.ClientID
	}
	if tokenData.ClientSecret != "" {
		metadata["clientSecret"] = tokenData.ClientSecret
	}

	fmt.Println("Kiro credentials loaded successfully")
	fmt.Printf("Region: %s\n", tokenData.Region)

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Storage:  tokenStorage,
		Metadata: metadata,
	}, nil
}

// LoadFromFile loads Kiro credentials from a specific file path.
func (a *KiroAuthenticator) LoadFromFile(ctx context.Context, cfg *config.Config, filePath string) (*coreauth.Auth, error) {
	if cfg == nil {
		return nil, fmt.Errorf("cliproxy auth: configuration is required")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	authSvc := kiro.NewKiroAuth(cfg)

	tokenData, err := authSvc.LoadCredentialsFromFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("failed to load Kiro credentials from file: %w", err)
	}

	if tokenData.AccessToken == "" && tokenData.RefreshToken == "" {
		return nil, fmt.Errorf("no valid Kiro credentials found in file")
	}

	// Check if token needs refresh
	if authSvc.IsTokenExpiringSoon(tokenData, 5) && tokenData.RefreshToken != "" {
		log.Info("Kiro token is expiring soon, refreshing...")
		newTokenData, refreshErr := authSvc.RefreshTokens(ctx, tokenData)
		if refreshErr != nil {
			log.Warnf("Failed to refresh Kiro token: %v", refreshErr)
		} else {
			tokenData = newTokenData
		}
	}

	tokenStorage := authSvc.CreateTokenStorage(tokenData)

	identifier := "kiro"
	if tokenData.Region != "" {
		identifier = fmt.Sprintf("kiro-%s", tokenData.Region)
	}

	fileName := fmt.Sprintf("%s.json", identifier)
	metadata := map[string]any{
		"accessToken":  tokenData.AccessToken,
		"refreshToken": tokenData.RefreshToken,
		"expiresAt":    tokenData.ExpiresAt,
		"region":       tokenData.Region,
		"authMethod":   tokenData.AuthMethod,
		"type":         "kiro",
	}

	if tokenData.ProfileArn != "" {
		metadata["profileArn"] = tokenData.ProfileArn
	}
	if tokenData.ClientID != "" {
		metadata["clientId"] = tokenData.ClientID
	}
	if tokenData.ClientSecret != "" {
		metadata["clientSecret"] = tokenData.ClientSecret
	}

	return &coreauth.Auth{
		ID:       fileName,
		Provider: a.Provider(),
		FileName: fileName,
		Storage:  tokenStorage,
		Metadata: metadata,
	}, nil
}
