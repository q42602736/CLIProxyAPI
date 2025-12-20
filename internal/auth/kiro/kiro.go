// Package kiro provides authentication and token management functionality
// for Kiro (AWS CodeWhisperer) AI services. It handles OAuth2 token storage,
// refresh, and retrieval for maintaining authenticated sessions with the Kiro API.
package kiro

// KiroConstants holds all constant values for Kiro API interactions
type KiroConstants struct {
	RefreshURL    string
	RefreshIDCURL string
	BaseURL       string
	AmazonQURL    string
	UsageLimitsURL string
	DefaultModel  string
	UserAgent     string
	KiroVersion   string
}

// DefaultConstants returns the default Kiro API constants
func DefaultConstants() KiroConstants {
	return KiroConstants{
		RefreshURL:     "https://prod.{{region}}.auth.desktop.kiro.dev/refreshToken",
		RefreshIDCURL:  "https://oidc.{{region}}.amazonaws.com/token",
		BaseURL:        "https://codewhisperer.{{region}}.amazonaws.com/generateAssistantResponse",
		AmazonQURL:     "https://codewhisperer.{{region}}.amazonaws.com/SendMessageStreaming",
		UsageLimitsURL: "https://q.{{region}}.amazonaws.com/getUsageLimits",
		DefaultModel:   "claude-opus-4-5",
		UserAgent:      "KiroIDE",
		KiroVersion:    "0.7.5",
	}
}

// PKCECodes holds PKCE verification codes for OAuth2 PKCE flow (if needed)
type PKCECodes struct {
	CodeVerifier  string `json:"code_verifier"`
	CodeChallenge string `json:"code_challenge"`
}

// KiroTokenData holds OAuth token information from Kiro/AWS
type KiroTokenData struct {
	AccessToken  string `json:"accessToken"`
	RefreshToken string `json:"refreshToken"`
	ClientID     string `json:"clientId,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
	AuthMethod   string `json:"authMethod,omitempty"`
	ExpiresAt    string `json:"expiresAt,omitempty"`
	ProfileArn   string `json:"profileArn,omitempty"`
	Region       string `json:"region,omitempty"`
}

// KiroAuthBundle aggregates authentication data after OAuth flow completion
type KiroAuthBundle struct {
	TokenData   KiroTokenData `json:"token_data"`
	LastRefresh string        `json:"last_refresh"`
}

// AuthMethodSocial is the social authentication method constant
const AuthMethodSocial = "social"

// OriginAIEditor is the origin constant for AI Editor
const OriginAIEditor = "AI_EDITOR"

// ChatTriggerTypeManual is the manual chat trigger type
const ChatTriggerTypeManual = "MANUAL"
