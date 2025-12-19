package management

import (
	"context"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	log "github.com/sirupsen/logrus"
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
		
		// Create executor and fetch quotas
		exec := executor.NewAntigravityExecutor(h.cfg)
		quotas, err := exec.GetQuotas(ctx, auth)
		if err != nil {
			log.WithError(err).Warnf("failed to get quotas for auth %s", auth.ID)
			continue
		}
		
		// Store quotas with auth file name as key
		fileName := auth.FileName
		if fileName == "" {
			fileName = auth.ID
		}
		result[fileName] = quotas
	}
	
	c.JSON(http.StatusOK, gin.H{"quotas": result})
}
