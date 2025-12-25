package management

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
)

const (
	// quotaRetryAttempts is the number of retry attempts for quota queries
	quotaRetryAttempts = 3
	// quotaRetryDelay is the delay between retry attempts
	quotaRetryDelay = 500 * time.Millisecond
)

// GetAntigravityQuotas returns quota information for Antigravity auth files
func (h *Handler) GetAntigravityQuotas(c *gin.Context) {
	if h == nil || h.authManager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "auth manager unavailable"})
		return
	}

	ctx := context.Background()
	auths := h.authManager.List()

	result := make(map[string]interface{})

	// Find all Antigravity auth files
	for _, auth := range auths {
		if auth == nil || !strings.EqualFold(auth.Provider, "antigravity") {
			continue
		}

		// Skip disabled or unavailable auths
		if auth.Disabled || auth.Unavailable || auth.Status == coreauth.StatusDisabled {
			continue
		}

		// Create executor and fetch quotas with retry
		exec := executor.NewAntigravityExecutor(h.cfg)
		quotas, err := h.getQuotasWithRetry(ctx, exec, auth, quotaRetryAttempts)

		// Store quotas with auth file name as key
		fileName := auth.FileName
		if fileName == "" {
			fileName = auth.ID
		}

		if err != nil {
			log.WithError(err).Warnf("failed to get quotas for auth %s after %d attempts", auth.ID, quotaRetryAttempts)
			// 即使查询失败，也要显示该认证文件，标记为错误状态
			result[fileName] = map[string]interface{}{
				"error":  err.Error(),
				"status": "failed",
			}
			continue
		}

		result[fileName] = quotas
	}

	c.JSON(http.StatusOK, gin.H{"quotas": result})
}

// getQuotasWithRetry attempts to get quotas with retry logic
func (h *Handler) getQuotasWithRetry(ctx context.Context, exec *executor.AntigravityExecutor, auth *coreauth.Auth, maxAttempts int) (map[string]interface{}, error) {
	var lastErr error

	for attempt := 1; attempt <= maxAttempts; attempt++ {
		quotas, err := exec.GetQuotas(ctx, auth)
		if err == nil {
			return quotas, nil
		}

		lastErr = err

		// Don't wait after the last attempt
		if attempt < maxAttempts {
			time.Sleep(quotaRetryDelay)
		}
	}

	return nil, lastErr
}
