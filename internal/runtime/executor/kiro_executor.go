package executor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	kiroauth "github.com/router-for-me/CLIProxyAPI/v6/internal/auth/kiro"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/util"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/executor"
	sdktranslator "github.com/router-for-me/CLIProxyAPI/v6/sdk/translator"
	log "github.com/sirupsen/logrus"
	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

// KiroExecutor is a stateless executor for Kiro (AWS CodeWhisperer) API.
type KiroExecutor struct {
	cfg       *config.Config
	constants kiroauth.KiroConstants
}

// NewKiroExecutor creates a new Kiro executor instance.
func NewKiroExecutor(cfg *config.Config) *KiroExecutor {
	return &KiroExecutor{
		cfg:       cfg,
		constants: kiroauth.DefaultConstants(),
	}
}

func (e *KiroExecutor) Identifier() string { return "kiro" }

func (e *KiroExecutor) PrepareRequest(_ *http.Request, _ *cliproxyauth.Auth) error { return nil }

func (e *KiroExecutor) Execute(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (resp cliproxyexecutor.Response, err error) {
	tokenData, region := kiroCredsFromAuth(auth)
	if tokenData == nil || tokenData.AccessToken == "" {
		return resp, fmt.Errorf("kiro executor: no access token available")
	}

	baseURL := e.getBaseURL(region, req.Model)
	reporter := newUsageReporter(ctx, e.Identifier(), req.Model, auth)
	defer reporter.trackFailure(ctx, &err)

	from := opts.SourceFormat
	to := sdktranslator.FromString("claude")
	body := sdktranslator.TranslateRequest(from, to, req.Model, bytes.Clone(req.Payload), true)

	upstreamModel := util.ResolveOriginalModel(req.Model, req.Metadata)
	if upstreamModel == "" {
		upstreamModel = req.Model
	}

	// Build Kiro request
	kiroReq, err := e.buildKiroRequest(body, upstreamModel, tokenData)
	if err != nil {
		return resp, fmt.Errorf("failed to build kiro request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(kiroReq))
	if err != nil {
		return resp, err
	}

	e.applyKiroHeaders(httpReq, tokenData)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       baseURL,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      kiroReq,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		log.Debugf("kiro request error, status: %d, body: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}

		// Handle 403 error: try to refresh token and retry once
		if httpResp.StatusCode == 403 && auth != nil && tokenData.RefreshToken != "" {
			fmt.Println("[Kiro] Received 403. Attempting token refresh and retrying...")
			newAuth, refreshErr := e.Refresh(ctx, auth)
			if refreshErr != nil {
				fmt.Printf("[Kiro] Token refresh failed: %v\n", refreshErr)
				return resp, statusErr{code: httpResp.StatusCode, msg: string(b)}
			}
			// Retry with refreshed auth
			newTokenData, _ := kiroCredsFromAuth(newAuth)
			if newTokenData != nil && newTokenData.AccessToken != "" {
				// Build new request with refreshed token
				kiroReqRetry, _ := e.buildKiroRequest(body, upstreamModel, newTokenData)
				httpReqRetry, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(kiroReqRetry))
				e.applyKiroHeaders(httpReqRetry, newTokenData)
				httpRespRetry, retryErr := httpClient.Do(httpReqRetry)
				if retryErr != nil {
					return resp, retryErr
				}
				if httpRespRetry.StatusCode >= 200 && httpRespRetry.StatusCode < 300 {
					log.Info("[Kiro] Retry after token refresh succeeded")
					decodedBodyRetry, decodeErr := decodeResponseBody(httpRespRetry.Body, httpRespRetry.Header.Get("Content-Encoding"))
					if decodeErr != nil {
						return resp, decodeErr
					}
					defer decodedBodyRetry.Close()
					dataRetry, readErr := io.ReadAll(decodedBodyRetry)
					if readErr != nil {
						return resp, readErr
					}
					appendAPIResponseChunk(ctx, e.cfg, dataRetry)
					inputTokens := estimateInputTokens(body)
					claudeRespRetry := e.parseKiroResponse(dataRetry, req.Model, inputTokens)
					var paramRetry any
					outRetry := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, claudeRespRetry, &paramRetry)
					return cliproxyexecutor.Response{Payload: []byte(outRetry)}, nil
				}
				bRetry, _ := io.ReadAll(httpRespRetry.Body)
				_ = httpRespRetry.Body.Close()
				log.Errorf("[Kiro] Retry after token refresh failed: %d %s", httpRespRetry.StatusCode, string(bRetry))
				return resp, statusErr{code: httpRespRetry.StatusCode, msg: string(bRetry)}
			}
		}

		// Handle 429 (Too Many Requests) - return rate limit error
		if httpResp.StatusCode == 429 {
			log.Warnf("[Kiro] Received 429 (Too Many Requests)")
			return resp, statusErr{code: httpResp.StatusCode, msg: string(b)}
		}

		// Handle 5xx server errors - return server error
		if httpResp.StatusCode >= 500 && httpResp.StatusCode < 600 {
			log.Warnf("[Kiro] Received %d server error", httpResp.StatusCode)
			return resp, statusErr{code: httpResp.StatusCode, msg: string(b)}
		}

		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return resp, err
	}

	decodedBody, err := decodeResponseBody(httpResp.Body, httpResp.Header.Get("Content-Encoding"))
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}
		return resp, err
	}
	defer func() {
		if errClose := decodedBody.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}
	}()

	data, err := io.ReadAll(decodedBody)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return resp, err
	}
	appendAPIResponseChunk(ctx, e.cfg, data)

	// Parse Kiro response and convert to Claude format
	inputTokens := estimateInputTokens(body)
	claudeResp := e.parseKiroResponse(data, req.Model, inputTokens)

	// Record successful request
	reporter.ensurePublished(ctx)

	var param any
	out := sdktranslator.TranslateNonStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, claudeResp, &param)
	resp = cliproxyexecutor.Response{Payload: []byte(out)}
	return resp, nil
}

func (e *KiroExecutor) ExecuteStream(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (stream <-chan cliproxyexecutor.StreamChunk, err error) {
	tokenData, region := kiroCredsFromAuth(auth)
	if tokenData == nil || tokenData.AccessToken == "" {
		return nil, fmt.Errorf("kiro executor: no access token available")
	}

	baseURL := e.getBaseURL(region, req.Model)
	reporter := newUsageReporter(ctx, e.Identifier(), req.Model, auth)
	defer reporter.trackFailure(ctx, &err)

	from := opts.SourceFormat
	to := sdktranslator.FromString("claude")
	body := sdktranslator.TranslateRequest(from, to, req.Model, bytes.Clone(req.Payload), true)

	upstreamModel := util.ResolveOriginalModel(req.Model, req.Metadata)
	if upstreamModel == "" {
		upstreamModel = req.Model
	}

	// Build Kiro request
	kiroReq, err := e.buildKiroRequest(body, upstreamModel, tokenData)
	if err != nil {
		return nil, fmt.Errorf("failed to build kiro request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(kiroReq))
	if err != nil {
		return nil, err
	}

	e.applyKiroHeaders(httpReq, tokenData)

	var authID, authLabel, authType, authValue string
	if auth != nil {
		authID = auth.ID
		authLabel = auth.Label
		authType, authValue = auth.AccountInfo()
	}
	recordAPIRequest(ctx, e.cfg, upstreamRequestLog{
		URL:       baseURL,
		Method:    http.MethodPost,
		Headers:   httpReq.Header.Clone(),
		Body:      kiroReq,
		Provider:  e.Identifier(),
		AuthID:    authID,
		AuthLabel: authLabel,
		AuthType:  authType,
		AuthValue: authValue,
	})

	httpClient := newProxyAwareHTTPClient(ctx, e.cfg, auth, 0)
	httpResp, err := httpClient.Do(httpReq)
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		return nil, err
	}
	recordAPIResponseMetadata(ctx, e.cfg, httpResp.StatusCode, httpResp.Header.Clone())

	if httpResp.StatusCode < 200 || httpResp.StatusCode >= 300 {
		b, _ := io.ReadAll(httpResp.Body)
		appendAPIResponseChunk(ctx, e.cfg, b)
		log.Debugf("kiro request error, status: %d, body: %s", httpResp.StatusCode, summarizeErrorBody(httpResp.Header.Get("Content-Type"), b))
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}

		// Handle 403 error in stream: try to refresh token and retry once
		if httpResp.StatusCode == 403 && auth != nil && tokenData.RefreshToken != "" {
			fmt.Println("[Kiro Stream] Received 403. Attempting token refresh and retrying...")
			newAuth, refreshErr := e.Refresh(ctx, auth)
			if refreshErr != nil {
				fmt.Printf("[Kiro Stream] Token refresh failed: %v\n", refreshErr)
				return nil, statusErr{code: httpResp.StatusCode, msg: string(b)}
			}
			newTokenData, _ := kiroCredsFromAuth(newAuth)
			if newTokenData != nil && newTokenData.AccessToken != "" {
				fmt.Println("[Kiro Stream] Token refreshed, retrying request...")
				kiroReqRetry, _ := e.buildKiroRequest(body, upstreamModel, newTokenData)
				httpReqRetry, _ := http.NewRequestWithContext(ctx, http.MethodPost, baseURL, bytes.NewReader(kiroReqRetry))
				e.applyKiroHeaders(httpReqRetry, newTokenData)
				httpRespRetry, retryErr := httpClient.Do(httpReqRetry)
				if retryErr != nil {
					return nil, retryErr
				}
				if httpRespRetry.StatusCode >= 200 && httpRespRetry.StatusCode < 300 {
					fmt.Println("[Kiro Stream] Retry after token refresh succeeded")
					// Continue with the retried response
					httpResp = httpRespRetry
					goto processStream
				}
				bRetry, _ := io.ReadAll(httpRespRetry.Body)
				_ = httpRespRetry.Body.Close()
				fmt.Printf("[Kiro Stream] Retry failed: %d %s\n", httpRespRetry.StatusCode, string(bRetry))
				return nil, statusErr{code: httpRespRetry.StatusCode, msg: string(bRetry)}
			}
		}

		err = statusErr{code: httpResp.StatusCode, msg: string(b)}
		return nil, err
	}

processStream:
	decodedBody, err := decodeResponseBody(httpResp.Body, httpResp.Header.Get("Content-Encoding"))
	if err != nil {
		recordAPIResponseError(ctx, e.cfg, err)
		if errClose := httpResp.Body.Close(); errClose != nil {
			log.Errorf("response body close error: %v", errClose)
		}
		return nil, err
	}

	out := make(chan cliproxyexecutor.StreamChunk)
	stream = out

	go func() {
		defer close(out)
		defer func() {
			if errClose := decodedBody.Close(); errClose != nil {
				log.Errorf("response body close error: %v", errClose)
			}
		}()

		messageID := uuid.New().String()
		var param any

		// Calculate input tokens
		inputTokens := estimateInputTokens(body)

		// Send message_start event
		startEvent := e.buildClaudeMessageStart(messageID, req.Model, inputTokens)
		startChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, startEvent, &param)
		for _, chunk := range startChunks {
			out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
		}

		// Send content_block_start event
		blockStartEvent := e.buildClaudeContentBlockStart(0)
		blockStartChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, blockStartEvent, &param)
		for _, chunk := range blockStartChunks {
			out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
		}

		// Read all stream data into buffer and parse events
		var buffer bytes.Buffer
		readBuf := make([]byte, 4096)
		var totalContent strings.Builder
		var lastContent string // 用于去重连续相同的 content
		blockIndex := 0
		var currentToolUse *kiroToolUse
		var toolInputBuilder strings.Builder

		for {
			n, readErr := decodedBody.Read(readBuf)
			if n > 0 {
				buffer.Write(readBuf[:n])
				appendAPIResponseChunk(ctx, e.cfg, readBuf[:n])

				// Parse all complete events from buffer
				bufStr := buffer.String()
				events, remaining := e.parseKiroStreamBuffer(bufStr)
				buffer.Reset()
				buffer.WriteString(remaining)

				for _, event := range events {
					if event.Type == "content" && event.Content != "" {
						// 跳过连续相同的 content (Kiro API 有时会重复发送)
						if event.Content == lastContent {
							continue
						}
						lastContent = event.Content
						totalContent.WriteString(event.Content)
						deltaEvent := e.buildClaudeContentBlockDelta(blockIndex, event.Content)
						deltaChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, deltaEvent, &param)
						for _, chunk := range deltaChunks {
							out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
						}
					} else if event.Type == "toolUse" && event.ToolUse != nil {
						// 照搬 AIClient-2-API 的逻辑：toolUse 事件可能带有 input
						if currentToolUse == nil {
							// 新的工具调用开始
							// Send content_block_stop for previous text block if any
							if blockIndex == 0 && totalContent.Len() > 0 {
								blockStopEvent := e.buildClaudeContentBlockStop(blockIndex)
								blockStopChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, blockStopEvent, &param)
								for _, chunk := range blockStopChunks {
									out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
								}
								blockIndex++
							}
							// Start new tool_use block
							currentToolUse = event.ToolUse
							toolInputBuilder.Reset()
							toolStartEvent := e.buildClaudeToolUseStart(blockIndex, event.ToolUse.ToolUseId, event.ToolUse.Name)
							toolStartChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, toolStartEvent, &param)
							for _, chunk := range toolStartChunks {
								out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
							}
						}
						// 如果有 input，发送 input delta
						if event.ToolUse.Input != "" {
							toolInputBuilder.WriteString(event.ToolUse.Input)
							inputDeltaEvent := e.buildClaudeToolInputDelta(blockIndex, event.ToolUse.Input)
							inputDeltaChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, inputDeltaEvent, &param)
							for _, chunk := range inputDeltaChunks {
								out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
							}
						}
						// 如果有 stop，结束工具调用
						if event.ToolStop {
							blockStopEvent := e.buildClaudeContentBlockStop(blockIndex)
							blockStopChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, blockStopEvent, &param)
							for _, chunk := range blockStopChunks {
								out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
							}
							blockIndex++
							currentToolUse = nil
						}
					} else if event.Type == "toolUseInput" && event.ToolInput != "" {
						toolInputBuilder.WriteString(event.ToolInput)
						// Send input delta
						inputDeltaEvent := e.buildClaudeToolInputDelta(blockIndex, event.ToolInput)
						inputDeltaChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, inputDeltaEvent, &param)
						for _, chunk := range inputDeltaChunks {
							out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
						}
					} else if event.Type == "toolUseStop" && event.ToolStop {
						// End tool_use block
						if currentToolUse != nil {
							blockStopEvent := e.buildClaudeContentBlockStop(blockIndex)
							blockStopChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, blockStopEvent, &param)
							for _, chunk := range blockStopChunks {
								out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
							}
							blockIndex++
							currentToolUse = nil
						}
					}
				}
			}
			if readErr != nil {
				if readErr != io.EOF {
					recordAPIResponseError(ctx, e.cfg, readErr)
					reporter.publishFailure(ctx)
					out <- cliproxyexecutor.StreamChunk{Err: readErr}
					return
				}
				break
			}
		}

		// Process any remaining buffer content
		if buffer.Len() > 0 {
			events, _ := e.parseKiroStreamBuffer(buffer.String())
			for _, event := range events {
				if event.Type == "content" && event.Content != "" && event.Content != lastContent {
					totalContent.WriteString(event.Content)
					deltaEvent := e.buildClaudeContentBlockDelta(blockIndex, event.Content)
					deltaChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, deltaEvent, &param)
					for _, chunk := range deltaChunks {
						out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
					}
				}
			}
		}

		// 如果还有未关闭的工具调用，关闭它
		if currentToolUse != nil {
			blockStopEvent := e.buildClaudeContentBlockStop(blockIndex)
			blockStopChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, blockStopEvent, &param)
			for _, chunk := range blockStopChunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
			}
			blockIndex++
			currentToolUse = nil
		}

		// Send content_block_stop event for text block (only if blockIndex is still 0)
		if blockIndex == 0 {
			blockStopEvent := e.buildClaudeContentBlockStop(0)
			blockStopChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, blockStopEvent, &param)
			for _, chunk := range blockStopChunks {
				out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
			}
		}

		// Send message_delta event with stop_reason based on whether tool was used
		stopReason := "end_turn"
		if blockIndex > 0 {
			stopReason = "tool_use"
		}
		outputTokens := estimateOutputTokens(totalContent.String() + toolInputBuilder.String())
		deltaEvent := e.buildClaudeMessageDelta(stopReason, outputTokens)
		deltaChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, deltaEvent, &param)
		for _, chunk := range deltaChunks {
			out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
		}

		// Send message_stop event
		stopEvent := []byte(`event: message_stop` + "\n" + `data: {"type":"message_stop"}` + "\n\n")
		stopChunks := sdktranslator.TranslateStream(ctx, to, from, req.Model, bytes.Clone(opts.OriginalRequest), body, stopEvent, &param)
		for _, chunk := range stopChunks {
			out <- cliproxyexecutor.StreamChunk{Payload: []byte(chunk)}
		}

		// Record successful request
		reporter.ensurePublished(ctx)
	}()

	return stream, nil
}

func (e *KiroExecutor) CountTokens(ctx context.Context, auth *cliproxyauth.Auth, req cliproxyexecutor.Request, opts cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	// Kiro doesn't support count_tokens endpoint, return empty response
	return cliproxyexecutor.Response{}, fmt.Errorf("kiro executor: count_tokens not supported")
}

func (e *KiroExecutor) Refresh(ctx context.Context, auth *cliproxyauth.Auth) (*cliproxyauth.Auth, error) {
	log.Debugf("kiro executor: refresh called")
	if auth == nil {
		return nil, fmt.Errorf("kiro executor: auth is nil")
	}

	tokenData, _ := kiroCredsFromAuth(auth)
	if tokenData == nil || tokenData.RefreshToken == "" {
		return auth, nil
	}

	svc := kiroauth.NewKiroAuth(e.cfg)
	newTokenData, err := svc.RefreshTokens(ctx, tokenData)
	if err != nil {
		return nil, err
	}

	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["accessToken"] = newTokenData.AccessToken
	auth.Metadata["refreshToken"] = newTokenData.RefreshToken
	auth.Metadata["expiresAt"] = newTokenData.ExpiresAt
	if newTokenData.ProfileArn != "" {
		auth.Metadata["profileArn"] = newTokenData.ProfileArn
	}
	auth.Metadata["type"] = "kiro"
	auth.Metadata["last_refresh"] = time.Now().Format(time.RFC3339)

	return auth, nil
}

// Helper functions

func kiroCredsFromAuth(a *cliproxyauth.Auth) (*kiroauth.KiroTokenData, string) {
	if a == nil {
		return nil, ""
	}

	tokenData := &kiroauth.KiroTokenData{}
	region := "us-east-1"

	if a.Metadata != nil {
		if v, ok := a.Metadata["accessToken"].(string); ok {
			tokenData.AccessToken = v
		}
		if v, ok := a.Metadata["refreshToken"].(string); ok {
			tokenData.RefreshToken = v
		}
		if v, ok := a.Metadata["clientId"].(string); ok {
			tokenData.ClientID = v
		}
		if v, ok := a.Metadata["clientSecret"].(string); ok {
			tokenData.ClientSecret = v
		}
		if v, ok := a.Metadata["authMethod"].(string); ok {
			tokenData.AuthMethod = v
		}
		if v, ok := a.Metadata["expiresAt"].(string); ok {
			tokenData.ExpiresAt = v
		}
		if v, ok := a.Metadata["profileArn"].(string); ok {
			tokenData.ProfileArn = v
		}
		if v, ok := a.Metadata["region"].(string); ok && v != "" {
			tokenData.Region = v
			region = v
		}
	}

	if a.Attributes != nil {
		if v := a.Attributes["region"]; v != "" {
			region = v
			tokenData.Region = v
		}
	}

	return tokenData, region
}

func (e *KiroExecutor) getBaseURL(region, model string) string {
	if region == "" {
		region = "us-east-1"
	}
	if strings.HasPrefix(model, "amazonq") {
		return strings.ReplaceAll(e.constants.AmazonQURL, "{{region}}", region)
	}
	return strings.ReplaceAll(e.constants.BaseURL, "{{region}}", region)
}

func (e *KiroExecutor) applyKiroHeaders(r *http.Request, tokenData *kiroauth.KiroTokenData) {
	macHash := getMacAddressSha256()
	version := e.constants.KiroVersion

	r.Header.Set("Authorization", "Bearer "+tokenData.AccessToken)
	r.Header.Set("Content-Type", "application/json")
	r.Header.Set("Accept", "application/json")
	r.Header.Set("amz-sdk-request", "attempt=1; max=1")
	r.Header.Set("x-amzn-kiro-agent-mode", "vibe")
	r.Header.Set("x-amz-user-agent", fmt.Sprintf("aws-sdk-js/1.0.0 KiroIDE-%s-%s", version, macHash))
	r.Header.Set("User-Agent", fmt.Sprintf("aws-sdk-js/1.0.0 ua/2.1 os/darwin lang/js md/nodejs#22.21.1 api/codewhispererruntime#1.0.0 m/N,E KiroIDE-%s-%s", version, macHash))
	r.Header.Set("amz-sdk-invocation-id", uuid.New().String())
}

func getMacAddressSha256() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return sha256String("00:00:00:00:00:00")
	}

	for _, iface := range interfaces {
		if iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		mac := iface.HardwareAddr.String()
		if mac != "" && mac != "00:00:00:00:00:00" {
			return sha256String(mac)
		}
	}

	return sha256String("00:00:00:00:00:00")
}

func sha256String(s string) string {
	hash := sha256.Sum256([]byte(s))
	return hex.EncodeToString(hash[:])
}

// Kiro model mapping
var kiroModelMapping = map[string]string{
	"claude-opus-4-5":            "claude-opus-4.5",
	"claude-opus-4-5-20251101":   "claude-opus-4.5",
	"claude-haiku-4-5":           "claude-haiku-4.5",
	"claude-sonnet-4-5":          "CLAUDE_SONNET_4_5_20250929_V1_0",
	"claude-sonnet-4-5-20250929": "CLAUDE_SONNET_4_5_20250929_V1_0",
	"claude-sonnet-4-20250514":   "CLAUDE_SONNET_4_20250514_V1_0",
	"claude-3-7-sonnet-20250219": "CLAUDE_3_7_SONNET_20250219_V1_0",
}

func (e *KiroExecutor) buildKiroRequest(claudeBody []byte, model string, tokenData *kiroauth.KiroTokenData) ([]byte, error) {
	// Map model name
	kiroModel := model
	if mapped, ok := kiroModelMapping[model]; ok {
		kiroModel = mapped
	}

	conversationID := uuid.New().String()

	// Extract messages and tools from Claude format
	messages := gjson.GetBytes(claudeBody, "messages")
	system := gjson.GetBytes(claudeBody, "system")
	tools := gjson.GetBytes(claudeBody, "tools")

	// Extract system prompt
	systemPrompt := ""
	if system.Exists() {
		if system.IsArray() {
			var parts []string
			system.ForEach(func(_, value gjson.Result) bool {
				if value.Get("type").String() == "text" {
					parts = append(parts, value.Get("text").String())
				}
				return true
			})
			systemPrompt = strings.Join(parts, "\n")
		} else {
			systemPrompt = system.String()
		}
	}

	// Build tools context
	var toolsContext []map[string]interface{}
	if tools.Exists() && tools.IsArray() {
		tools.ForEach(func(_, tool gjson.Result) bool {
			toolSpec := map[string]interface{}{
				"name":        tool.Get("name").String(),
				"description": tool.Get("description").String(),
			}
			if tool.Get("input_schema").Exists() {
				var inputSchema map[string]interface{}
				json.Unmarshal([]byte(tool.Get("input_schema").Raw), &inputSchema)
				toolSpec["inputSchema"] = map[string]interface{}{"json": inputSchema}
			}
			toolsContext = append(toolsContext, map[string]interface{}{
				"toolSpecification": toolSpec,
			})
			return true
		})
	}

	// Process and merge messages
	type processedMsg struct {
		Role        string
		Content     string
		ToolUses    []map[string]interface{}
		ToolResults []map[string]interface{}
		Images      []map[string]interface{}
	}
	var processedMessages []processedMsg

	if messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			role := msg.Get("role").String()
			content := msg.Get("content")

			pm := processedMsg{Role: role}

			if content.IsArray() {
				content.ForEach(func(_, part gjson.Result) bool {
					partType := part.Get("type").String()
					switch partType {
					case "text":
						pm.Content += part.Get("text").String()
					case "tool_use":
						// Get input as raw JSON object
						inputRaw := part.Get("input")
						var inputValue interface{}
						if inputRaw.Exists() && inputRaw.Type == gjson.JSON {
							// Parse as map
							var inputMap map[string]interface{}
							if err := json.Unmarshal([]byte(inputRaw.Raw), &inputMap); err == nil {
								inputValue = inputMap
							} else {
								inputValue = inputRaw.Value()
							}
						} else if inputRaw.Exists() {
							inputValue = inputRaw.Value()
						} else {
							inputValue = map[string]interface{}{}
						}
						pm.ToolUses = append(pm.ToolUses, map[string]interface{}{
							"name":      part.Get("name").String(),
							"toolUseId": part.Get("id").String(),
							"input":     inputValue,
						})
					case "tool_result":
						resultContent := part.Get("content")
						resultText := ""
						if resultContent.IsArray() {
							resultContent.ForEach(func(_, rc gjson.Result) bool {
								if rc.Get("type").String() == "text" {
									resultText += rc.Get("text").String()
								}
								return true
							})
						} else {
							resultText = resultContent.String()
						}
						pm.ToolResults = append(pm.ToolResults, map[string]interface{}{
							"content":   []map[string]interface{}{{"text": resultText}},
							"status":    "success",
							"toolUseId": part.Get("tool_use_id").String(),
						})
					case "image":
						mediaType := part.Get("source.media_type").String()
						format := "png"
						if idx := strings.Index(mediaType, "/"); idx >= 0 {
							format = mediaType[idx+1:]
						}
						pm.Images = append(pm.Images, map[string]interface{}{
							"format": format,
							"source": map[string]interface{}{
								"bytes": part.Get("source.data").String(),
							},
						})
					}
					return true
				})
			} else {
				pm.Content = content.String()
			}

			processedMessages = append(processedMessages, pm)
			return true
		})
	}

	// Remove last assistant message with only "{"
	if len(processedMessages) > 0 {
		last := processedMessages[len(processedMessages)-1]
		if last.Role == "assistant" && strings.TrimSpace(last.Content) == "{" {
			processedMessages = processedMessages[:len(processedMessages)-1]
		}
	}

	// Merge adjacent messages with same role
	var mergedMessages []processedMsg
	for _, msg := range processedMessages {
		if len(mergedMessages) == 0 {
			mergedMessages = append(mergedMessages, msg)
		} else {
			lastIdx := len(mergedMessages) - 1
			if mergedMessages[lastIdx].Role == msg.Role {
				mergedMessages[lastIdx].Content += "\n" + msg.Content
				mergedMessages[lastIdx].ToolUses = append(mergedMessages[lastIdx].ToolUses, msg.ToolUses...)
				mergedMessages[lastIdx].ToolResults = append(mergedMessages[lastIdx].ToolResults, msg.ToolResults...)
				mergedMessages[lastIdx].Images = append(mergedMessages[lastIdx].Images, msg.Images...)
			} else {
				mergedMessages = append(mergedMessages, msg)
			}
		}
	}

	// Build history
	history := make([]map[string]interface{}, 0)
	startIndex := 0

	// Handle system prompt
	if systemPrompt != "" && len(mergedMessages) > 0 && mergedMessages[0].Role == "user" {
		firstContent := systemPrompt + "\n\n" + mergedMessages[0].Content
		userMsg := map[string]interface{}{
			"content": firstContent,
			"modelId": kiroModel,
			"origin":  kiroauth.OriginAIEditor,
		}
		if len(mergedMessages[0].Images) > 0 {
			userMsg["images"] = mergedMessages[0].Images
		}
		if len(mergedMessages[0].ToolResults) > 0 {
			userMsg["userInputMessageContext"] = map[string]interface{}{
				"toolResults": dedupeToolResults(mergedMessages[0].ToolResults),
			}
		}
		history = append(history, map[string]interface{}{"userInputMessage": userMsg})
		startIndex = 1
	} else if systemPrompt != "" {
		history = append(history, map[string]interface{}{
			"userInputMessage": map[string]interface{}{
				"content": systemPrompt,
				"modelId": kiroModel,
				"origin":  kiroauth.OriginAIEditor,
			},
		})
	}

	// Add messages to history (except last one)
	for i := startIndex; i < len(mergedMessages)-1; i++ {
		msg := mergedMessages[i]
		if msg.Role == "user" {
			userMsg := map[string]interface{}{
				"content": msg.Content,
				"modelId": kiroModel,
				"origin":  kiroauth.OriginAIEditor,
			}
			if len(msg.Images) > 0 {
				userMsg["images"] = msg.Images
			}
			if len(msg.ToolResults) > 0 {
				userMsg["userInputMessageContext"] = map[string]interface{}{
					"toolResults": dedupeToolResults(msg.ToolResults),
				}
			}
			history = append(history, map[string]interface{}{"userInputMessage": userMsg})
		} else if msg.Role == "assistant" {
			assistantMsg := map[string]interface{}{
				"content": msg.Content,
			}
			if len(msg.ToolUses) > 0 {
				assistantMsg["toolUses"] = msg.ToolUses
			}
			history = append(history, map[string]interface{}{"assistantResponseMessage": assistantMsg})
		}
	}

	// Build current message
	var currentContent string
	var currentToolResults []map[string]interface{}
	var currentImages []map[string]interface{}

	if len(mergedMessages) > 0 {
		lastMsg := mergedMessages[len(mergedMessages)-1]

		// If last message is assistant, move to history and create "Continue" user message
		if lastMsg.Role == "assistant" {
			assistantMsg := map[string]interface{}{
				"content": lastMsg.Content,
			}
			if len(lastMsg.ToolUses) > 0 {
				assistantMsg["toolUses"] = lastMsg.ToolUses
			}
			history = append(history, map[string]interface{}{"assistantResponseMessage": assistantMsg})
			currentContent = "Continue"
		} else {
			currentContent = lastMsg.Content
			currentToolResults = lastMsg.ToolResults
			currentImages = lastMsg.Images
		}
	}

	// Kiro API requires content not empty
	if currentContent == "" {
		if len(currentToolResults) > 0 {
			currentContent = "Tool results provided."
		} else {
			currentContent = "Continue"
		}
	}

	// Build request
	userInputMessage := map[string]interface{}{
		"content": currentContent,
		"modelId": kiroModel,
		"origin":  kiroauth.OriginAIEditor,
	}

	if len(currentImages) > 0 {
		userInputMessage["images"] = currentImages
	}

	// Build userInputMessageContext
	userInputMessageContext := make(map[string]interface{})
	if len(currentToolResults) > 0 {
		userInputMessageContext["toolResults"] = dedupeToolResults(currentToolResults)
	}
	if len(toolsContext) > 0 {
		userInputMessageContext["tools"] = toolsContext
	}
	if len(userInputMessageContext) > 0 {
		userInputMessage["userInputMessageContext"] = userInputMessageContext
	}

	request := map[string]interface{}{
		"conversationState": map[string]interface{}{
			"chatTriggerType": kiroauth.ChatTriggerTypeManual,
			"conversationId":  conversationID,
			"currentMessage": map[string]interface{}{
				"userInputMessage": userInputMessage,
			},
		},
	}

	if len(history) > 0 {
		request["conversationState"].(map[string]interface{})["history"] = history
	}

	if tokenData.AuthMethod == kiroauth.AuthMethodSocial && tokenData.ProfileArn != "" {
		request["profileArn"] = tokenData.ProfileArn
	}

	return json.Marshal(request)
}

// dedupeToolResults removes duplicate toolResults by toolUseId
func dedupeToolResults(results []map[string]interface{}) []map[string]interface{} {
	seen := make(map[string]bool)
	var unique []map[string]interface{}
	for _, r := range results {
		if id, ok := r["toolUseId"].(string); ok {
			if !seen[id] {
				seen[id] = true
				unique = append(unique, r)
			}
		} else {
			unique = append(unique, r)
		}
	}
	return unique
}

type kiroStreamEvent struct {
	Type      string
	Content   string
	ToolUse   *kiroToolUse
	ToolInput string
	ToolStop  bool
}

type kiroToolUse struct {
	Name      string `json:"name"`
	ToolUseId string `json:"toolUseId"`
	Input     string `json:"input"`
}

// parseKiroStreamBuffer parses AWS Event Stream format buffer and extracts JSON events
// Returns parsed events and remaining unparsed buffer
func (e *KiroExecutor) parseKiroStreamBuffer(buffer string) ([]kiroStreamEvent, string) {
	events := make([]kiroStreamEvent, 0)
	remaining := buffer
	searchStart := 0

	for {
		// Search for all possible JSON payload patterns
		contentStart := strings.Index(remaining[searchStart:], `{"content":`)
		nameStart := strings.Index(remaining[searchStart:], `{"name":`)
		followupStart := strings.Index(remaining[searchStart:], `{"followupPrompt":`)
		inputStart := strings.Index(remaining[searchStart:], `{"input":`)
		stopStart := strings.Index(remaining[searchStart:], `{"stop":`)

		// Adjust indices to be relative to remaining
		if contentStart >= 0 {
			contentStart += searchStart
		}
		if nameStart >= 0 {
			nameStart += searchStart
		}
		if followupStart >= 0 {
			followupStart += searchStart
		}
		if inputStart >= 0 {
			inputStart += searchStart
		}
		if stopStart >= 0 {
			stopStart += searchStart
		}

		// Find earliest valid JSON pattern
		candidates := []int{}
		for _, pos := range []int{contentStart, nameStart, followupStart, inputStart, stopStart} {
			if pos >= 0 {
				candidates = append(candidates, pos)
			}
		}
		if len(candidates) == 0 {
			break
		}

		jsonStart := candidates[0]
		for _, c := range candidates {
			if c < jsonStart {
				jsonStart = c
			}
		}

		// Find matching closing brace using bracket counting
		braceCount := 0
		jsonEnd := -1
		inString := false
		escapeNext := false

		for i := jsonStart; i < len(remaining); i++ {
			ch := remaining[i]

			if escapeNext {
				escapeNext = false
				continue
			}
			if ch == '\\' {
				escapeNext = true
				continue
			}
			if ch == '"' {
				inString = !inString
				continue
			}
			if !inString {
				if ch == '{' {
					braceCount++
				} else if ch == '}' {
					braceCount--
					if braceCount == 0 {
						jsonEnd = i
						break
					}
				}
			}
		}

		if jsonEnd < 0 {
			// Incomplete JSON, keep in buffer for more data
			remaining = remaining[jsonStart:]
			break
		}

		jsonStr := remaining[jsonStart : jsonEnd+1]
		var parsed map[string]interface{}
		if err := json.Unmarshal([]byte(jsonStr), &parsed); err == nil {
			// 完全照搬 AIClient-2-API parseAwsEventStreamBuffer 的逻辑
			
			// 1. 处理 content 事件
			if content, ok := parsed["content"].(string); ok {
				if _, hasFollowup := parsed["followupPrompt"]; !hasFollowup {
					events = append(events, kiroStreamEvent{
						Type:    "content",
						Content: content,
					})
				}
			} else if name, hasName := parsed["name"].(string); hasName {
				// 2. 处理结构化工具调用事件 - 包含 name 和 toolUseId
				if toolUseId, hasId := parsed["toolUseId"].(string); hasId {
					inputStr := ""
					if input, ok := parsed["input"].(string); ok {
						inputStr = input
					}
					stopVal := false
					if stop, ok := parsed["stop"].(bool); ok {
						stopVal = stop
					}
					events = append(events, kiroStreamEvent{
						Type: "toolUse",
						ToolUse: &kiroToolUse{
							Name:      name,
							ToolUseId: toolUseId,
							Input:     inputStr,
						},
						ToolStop: stopVal,
					})
				}
			} else if input, hasInput := parsed["input"]; hasInput && parsed["name"] == nil {
				// 3. 处理工具调用的 input 续传事件（只有 input 字段，没有 name）
				if inputStr, ok := input.(string); ok {
					events = append(events, kiroStreamEvent{
						Type:      "toolUseInput",
						ToolInput: inputStr,
					})
				}
			} else if stop, hasStop := parsed["stop"]; hasStop && parsed["name"] == nil {
				// 4. 处理工具调用的结束事件（只有 stop 字段）
				if stopBool, ok := stop.(bool); ok && stopBool {
					events = append(events, kiroStreamEvent{
						Type:     "toolUseStop",
						ToolStop: true,
					})
				}
			}
		}

		// Move search position past this JSON
		searchStart = jsonEnd + 1
		if searchStart >= len(remaining) {
			remaining = ""
			break
		}
	}

	if searchStart > 0 && searchStart < len(remaining) {
		remaining = remaining[searchStart:]
	} else if searchStart >= len(remaining) {
		remaining = ""
	}

	return events, remaining
}

func (e *KiroExecutor) parseKiroStreamEvents(line []byte) []kiroStreamEvent {
	events, _ := e.parseKiroStreamBuffer(string(line))
	return events
}

func (e *KiroExecutor) parseKiroResponse(data []byte, model string, inputTokens int) []byte {
	var fullContent strings.Builder

	// Parse all content from response
	events := e.parseKiroStreamEvents(data)
	for _, event := range events {
		if event.Type == "content" {
			fullContent.WriteString(event.Content)
		}
	}

	// Calculate output tokens
	outputTokens := estimateOutputTokens(fullContent.String())

	// Build Claude-compatible response
	response := map[string]interface{}{
		"id":            "msg_" + uuid.New().String(),
		"type":          "message",
		"role":          "assistant",
		"model":         model,
		"stop_reason":   "end_turn",
		"stop_sequence": nil,
		"usage": map[string]int{
			"input_tokens":  inputTokens,
			"output_tokens": outputTokens,
		},
		"content": []map[string]interface{}{
			{
				"type": "text",
				"text": fullContent.String(),
			},
		},
	}

	result, _ := json.Marshal(response)
	return result
}

func (e *KiroExecutor) buildClaudeMessageStart(messageID, model string, inputTokens int) []byte {
	event := map[string]interface{}{
		"type": "message_start",
		"message": map[string]interface{}{
			"id":    messageID,
			"type":  "message",
			"role":  "assistant",
			"model": model,
			"usage": map[string]int{
				"input_tokens":  inputTokens,
				"output_tokens": 0,
			},
			"content": []interface{}{},
		},
	}
	data, _ := json.Marshal(event)
	return []byte(fmt.Sprintf("event: message_start\ndata: %s\n\n", string(data)))
}

func (e *KiroExecutor) buildClaudeContentBlockStart(index int) []byte {
	event := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type": "text",
			"text": "",
		},
	}
	data, _ := json.Marshal(event)
	return []byte(fmt.Sprintf("event: content_block_start\ndata: %s\n\n", string(data)))
}

func (e *KiroExecutor) buildClaudeContentBlockDelta(index int, text string) []byte {
	event := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type": "text_delta",
			"text": text,
		},
	}
	data, _ := json.Marshal(event)
	return []byte(fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", string(data)))
}

func (e *KiroExecutor) buildClaudeContentBlockStop(index int) []byte {
	event := map[string]interface{}{
		"type":  "content_block_stop",
		"index": index,
	}
	data, _ := json.Marshal(event)
	return []byte(fmt.Sprintf("event: content_block_stop\ndata: %s\n\n", string(data)))
}

func (e *KiroExecutor) buildClaudeToolUseStart(index int, toolUseId, name string) []byte {
	event := map[string]interface{}{
		"type":  "content_block_start",
		"index": index,
		"content_block": map[string]interface{}{
			"type":  "tool_use",
			"id":    toolUseId,
			"name":  name,
			"input": map[string]interface{}{},
		},
	}
	data, _ := json.Marshal(event)
	return []byte(fmt.Sprintf("event: content_block_start\ndata: %s\n\n", string(data)))
}

func (e *KiroExecutor) buildClaudeToolInputDelta(index int, input string) []byte {
	event := map[string]interface{}{
		"type":  "content_block_delta",
		"index": index,
		"delta": map[string]interface{}{
			"type":          "input_json_delta",
			"partial_json": input,
		},
	}
	data, _ := json.Marshal(event)
	return []byte(fmt.Sprintf("event: content_block_delta\ndata: %s\n\n", string(data)))
}

func (e *KiroExecutor) buildClaudeMessageDelta(stopReason string, outputTokens int) []byte {
	event := map[string]interface{}{
		"type": "message_delta",
		"delta": map[string]interface{}{
			"stop_reason":   stopReason,
			"stop_sequence": nil,
		},
		"usage": map[string]int{
			"output_tokens": outputTokens,
		},
	}
	data, _ := json.Marshal(event)
	return []byte(fmt.Sprintf("event: message_delta\ndata: %s\n\n", string(data)))
}

// Unused imports placeholder to avoid compile errors
var _ = sjson.Set

// Token counting functions (ported from AIClient-2-API)

// countTextTokens estimates token count for text using character-based estimation
// Since Go doesn't have @anthropic-ai/tokenizer, we use character/4 estimation
func countTextTokens(text string) int {
	if text == "" {
		return 0
	}
	// Approximate: 1 token ≈ 4 characters for English, less for CJK
	return (len(text) + 3) / 4
}

// estimateInputTokens calculates input tokens from Claude request body
func estimateInputTokens(claudeBody []byte) int {
	totalTokens := 0

	// Base request overhead
	const baseRequestOverhead = 4
	totalTokens += baseRequestOverhead

	// Count system prompt tokens
	system := gjson.GetBytes(claudeBody, "system")
	if system.Exists() {
		var systemText string
		if system.IsArray() {
			system.ForEach(func(_, value gjson.Result) bool {
				if value.Get("type").String() == "text" {
					systemText += value.Get("text").String()
				}
				return true
			})
		} else {
			systemText = system.String()
		}
		totalTokens += countTextTokens(systemText)
		totalTokens += 2 // System prompt overhead
	}

	// Count all messages tokens
	messages := gjson.GetBytes(claudeBody, "messages")
	if messages.IsArray() {
		messages.ForEach(func(_, msg gjson.Result) bool {
			// Message structure overhead
			const messageOverhead = 4
			totalTokens += messageOverhead
			totalTokens += 1 // role field

			content := msg.Get("content")
			totalTokens += estimateContentTokens(content)
			return true
		})
	}

	// Count tools definitions tokens
	tools := gjson.GetBytes(claudeBody, "tools")
	if tools.IsArray() {
		toolCount := len(tools.Array())

		// Tool base overhead
		var baseToolsOverhead, perToolOverhead int
		if toolCount == 1 {
			baseToolsOverhead = 0
			perToolOverhead = 50
		} else if toolCount <= 5 {
			baseToolsOverhead = 100
			perToolOverhead = 30
		} else {
			baseToolsOverhead = 180
			perToolOverhead = 20
		}

		totalTokens += baseToolsOverhead

		tools.ForEach(func(_, tool gjson.Result) bool {
			totalTokens += countTextTokens(tool.Get("name").String())
			totalTokens += countTextTokens(tool.Get("description").String())
			if tool.Get("input_schema").Exists() {
				totalTokens += countTextTokens(tool.Get("input_schema").Raw)
			}
			totalTokens += perToolOverhead
			return true
		})
	}

	return totalTokens
}

// estimateContentTokens estimates tokens for message content
func estimateContentTokens(content gjson.Result) int {
	const imageTokens = 1500 // Fixed estimate for images

	if !content.Exists() {
		return 0
	}

	// String content
	if content.Type == gjson.String {
		return countTextTokens(content.String())
	}

	// Array content
	if content.IsArray() {
		totalTokens := 0
		content.ForEach(func(_, block gjson.Result) bool {
			blockType := block.Get("type").String()
			switch blockType {
			case "text":
				totalTokens += countTextTokens(block.Get("text").String())
			case "image", "image_url":
				totalTokens += imageTokens
			case "tool_use":
				totalTokens += 4 // Structure overhead
				totalTokens += countTextTokens(block.Get("name").String())
				input := block.Get("input")
				if input.Exists() {
					totalTokens += countTextTokens(input.Raw)
				}
			case "tool_result":
				totalTokens += 4 // Structure overhead
				totalTokens += countTextTokens(block.Get("tool_use_id").String())
				resultContent := block.Get("content")
				if resultContent.Exists() {
					totalTokens += estimateContentTokens(resultContent)
				}
			case "thinking":
				totalTokens += countTextTokens(block.Get("thinking").String())
			default:
				// Unknown type, estimate from raw
				totalTokens += countTextTokens(block.Raw)
			}
			return true
		})
		return totalTokens
	}

	// Object content (single block)
	if content.IsObject() {
		return countTextTokens(content.Raw)
	}

	return 0
}

// estimateOutputTokens estimates output tokens from response content
func estimateOutputTokens(content string) int {
	return countTextTokens(content)
}
