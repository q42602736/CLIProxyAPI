package management

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/auth/codex"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	log "github.com/sirupsen/logrus"
)

// CodexUsageWindow 表示一个使用窗口
type CodexUsageWindow struct {
	Used      int    `json:"used"`
	Limit     int    `json:"limit"`
	Remaining int    `json:"remaining"`
	ResetTime string `json:"reset_time"`
	ResetIn   int64  `json:"reset_in"`
}

// CodexCredits 表示积分信息
type CodexCredits struct {
	Balance    float64 `json:"balance"`
	HasCredits bool    `json:"has_credits"`
	Unlimited  bool    `json:"unlimited"`
}

// CodexUsageResponse 表示响应
type CodexUsageResponse struct {
	Email         string            `json:"email"`
	Plan          string            `json:"plan"`
	SessionWindow *CodexUsageWindow `json:"session_window"`
	WeeklyWindow  *CodexUsageWindow `json:"weekly_window"`
	Credits       *CodexCredits     `json:"credits"`
	UpdatedAt     string            `json:"updated_at"`
}

type openAIUsageAPIResponse struct {
	PlanType string `json:"plan_type"`
	RateLimit struct {
		Allowed      bool `json:"allowed"`
		LimitReached bool `json:"limit_reached"`
		PrimaryWindow struct {
			UsedPercent         float64 `json:"used_percent"`
			LimitWindowSeconds  int     `json:"limit_window_seconds"`
			ResetAfterSeconds   int     `json:"reset_after_seconds"`
			ResetAt             int64   `json:"reset_at"`
		} `json:"primary_window"`
		SecondaryWindow struct {
			UsedPercent         float64 `json:"used_percent"`
			LimitWindowSeconds  int     `json:"limit_window_seconds"`
			ResetAfterSeconds   int     `json:"reset_after_seconds"`
			ResetAt             int64   `json:"reset_at"`
		} `json:"secondary_window"`
	} `json:"rate_limit"`
	Credits struct {
		HasCredits bool   `json:"has_credits"`
		Unlimited  bool   `json:"unlimited"`
		Balance    string `json:"balance"`
	} `json:"credits"`
}

func (h *Handler) GetCodexUsage(c *gin.Context) {
	name := c.Query("name")
	if name == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "name parameter is required"})
		return
	}

	authFilePath := filepath.Join(h.cfg.AuthDir, name)

	data, err := os.ReadFile(authFilePath)
	if err != nil {
		log.WithError(err).Errorf("Failed to read auth file: %s", authFilePath)
		c.JSON(http.StatusNotFound, gin.H{"error": "auth file not found"})
		return
	}

	// 尝试解析为 CodexTokenStorage（扁平结构）
	var tokenStorage codex.CodexTokenStorage
	var accessToken, refreshToken, email, expire, idToken string
	
	if err := json.Unmarshal(data, &tokenStorage); err == nil && tokenStorage.AccessToken != "" {
		// 使用扁平结构
		accessToken = tokenStorage.AccessToken
		refreshToken = tokenStorage.RefreshToken
		email = tokenStorage.Email
		expire = tokenStorage.Expire
		idToken = tokenStorage.IDToken
	} else {
		// 尝试解析为 CodexAuthBundle（嵌套结构）
		var authBundle codex.CodexAuthBundle
		if err := json.Unmarshal(data, &authBundle); err != nil {
			log.WithError(err).Error("Failed to parse auth file")
			c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to parse auth file"})
			return
		}
		accessToken = authBundle.TokenData.AccessToken
		refreshToken = authBundle.TokenData.RefreshToken
		email = authBundle.TokenData.Email
		expire = authBundle.TokenData.Expire
		idToken = authBundle.TokenData.IDToken
	}

	// 检查 token 是否过期，如果过期则刷新
	if expire != "" {
		expireTime, err := time.Parse(time.RFC3339, expire)
		if err == nil && time.Now().After(expireTime) {
			// Token 已过期，尝试刷新
			log.Info("Access token expired, refreshing...")
			codexAuth := codex.NewCodexAuth(h.cfg)
			newTokenData, err := codexAuth.RefreshTokens(context.Background(), refreshToken)
			if err != nil {
				log.WithError(err).Error("Failed to refresh token")
				c.JSON(http.StatusUnauthorized, gin.H{"error": "token expired and refresh failed, please re-login"})
				return
			}

			// 更新 token 并保存到文件（保持原格式）
			if tokenStorage.AccessToken != "" {
				// 更新扁平结构
				tokenStorage.AccessToken = newTokenData.AccessToken
				tokenStorage.RefreshToken = newTokenData.RefreshToken
				tokenStorage.IDToken = newTokenData.IDToken
				tokenStorage.Expire = newTokenData.Expire
				tokenStorage.LastRefresh = time.Now().Format(time.RFC3339)
				
				updatedData, err := json.MarshalIndent(tokenStorage, "", "  ")
				if err == nil {
					if err := os.WriteFile(authFilePath, updatedData, 0600); err != nil {
						log.WithError(err).Warn("Failed to save refreshed token")
					} else {
						log.Info("Token refreshed and saved successfully")
					}
				}
			}
			
			accessToken = newTokenData.AccessToken
			email = newTokenData.Email
			idToken = newTokenData.IDToken
		}
	}

	ctx := context.Background()
	usage, err := fetchCodexUsage(ctx, &h.cfg.SDKConfig, accessToken)
	if err != nil {
		log.WithError(err).Error("Failed to fetch Codex usage")
		c.JSON(http.StatusInternalServerError, gin.H{"error": fmt.Sprintf("failed to fetch usage: %v", err)})
		return
	}

	usage.Email = email
	// 尝试从 ID token 获取计划类型
	if idToken != "" {
		if claims, err := codex.ParseJWTToken(idToken); err == nil {
			if claims.CodexAuthInfo.ChatgptPlanType != "" {
				usage.Plan = claims.CodexAuthInfo.ChatgptPlanType
			}
		}
	}

	c.JSON(http.StatusOK, usage)
}

func fetchCodexUsage(ctx context.Context, cfg *config.SDKConfig, accessToken string) (*CodexUsageResponse, error) {
	httpClient := util.SetProxy(cfg, &http.Client{})

	req, err := http.NewRequestWithContext(ctx, "GET", "https://chatgpt.com/backend-api/wham/usage", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", accessToken))
	req.Header.Set("Accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("usage API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	var apiResp openAIUsageAPIResponse
	if err := json.Unmarshal(respBody, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse usage response: %w", err)
	}

	now := time.Now()
	usage := &CodexUsageResponse{
		UpdatedAt: now.Format(time.RFC3339),
	}

	// 解析 5小时窗口 (primary_window)
	if apiResp.RateLimit.PrimaryWindow.LimitWindowSeconds > 0 {
		usedPercent := apiResp.RateLimit.PrimaryWindow.UsedPercent
		// 假设 100% = 50 次请求（这是一个估算，实际限制可能不同）
		estimatedLimit := 50
		used := int(float64(estimatedLimit) * usedPercent / 100.0)
		remaining := estimatedLimit - used
		
		resetTime := time.Unix(apiResp.RateLimit.PrimaryWindow.ResetAt, 0)
		usage.SessionWindow = &CodexUsageWindow{
			Used:      used,
			Limit:     estimatedLimit,
			Remaining: remaining,
			ResetTime: resetTime.Format(time.RFC3339),
			ResetIn:   int64(time.Until(resetTime).Seconds()),
		}
	}

	// 解析每周窗口 (secondary_window)
	if apiResp.RateLimit.SecondaryWindow.LimitWindowSeconds > 0 {
		usedPercent := apiResp.RateLimit.SecondaryWindow.UsedPercent
		// 假设 100% = 500 次请求（这是一个估算，实际限制可能不同）
		estimatedLimit := 500
		used := int(float64(estimatedLimit) * usedPercent / 100.0)
		remaining := estimatedLimit - used
		
		resetTime := time.Unix(apiResp.RateLimit.SecondaryWindow.ResetAt, 0)
		usage.WeeklyWindow = &CodexUsageWindow{
			Used:      used,
			Limit:     estimatedLimit,
			Remaining: remaining,
			ResetTime: resetTime.Format(time.RFC3339),
			ResetIn:   int64(time.Until(resetTime).Seconds()),
		}
	}

	// 解析积分信息
	balance := 0.0
	if apiResp.Credits.Balance != "" {
		fmt.Sscanf(apiResp.Credits.Balance, "%f", &balance)
	}
	usage.Credits = &CodexCredits{
		Balance:    balance,
		HasCredits: apiResp.Credits.HasCredits,
		Unlimited:  apiResp.Credits.Unlimited,
	}

	return usage, nil
}
