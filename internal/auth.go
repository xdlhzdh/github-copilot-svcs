// Package internal provides core authentication, proxy, and service logic for github-copilot-svcs.
package internal

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

const (
	copilotDeviceCodeURL = "https://github.com/login/device/code"
	copilotTokenURL      = "https://github.com/login/oauth/access_token"
	copilotAPIKeyURL     = "https://api.github.com/copilot_internal/v2/token"
	copilotClientID      = "Iv1.b507a08c87ecfe98"
	copilotScope         = "read:user"

	// Retry configuration
	maxRefreshRetries = 3
	baseRetryDelay    = 2 // seconds
)

func getDatabaseURL() string {
	// Check if AUTOREVIEW_UI_HOST is set (for Docker environment)
	if host := os.Getenv("AUTOREVIEW_UI_HOST"); host != "" {
		return fmt.Sprintf("http://%s:3000/api/copilot-auth-status", host)
	}
	// Default to localhost for local development
	return "http://localhost:3000/api/copilot-auth-status"
}

type deviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type tokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error,omitempty"`
	ErrorDesc   string `json:"error_description,omitempty"`
}

type copilotTokenResponse struct {
	Token     string `json:"token"`
	ExpiresAt int64  `json:"expires_at"`
	RefreshIn int64  `json:"refresh_in"`
	Endpoints struct {
		API string `json:"api"`
	} `json:"endpoints"`
}

// AuthService provides authentication operations for GitHub Copilot.
type AuthService struct {
	httpClient *http.Client

	// For testability: override config save path
	configPath string

	// For testability: optional custom token refresh function
	refreshFunc func(cfg *Config) error
}

// NewAuthService creates a new auth service
func NewAuthService(httpClient *http.Client, opts ...func(*AuthService)) *AuthService {
	svc := &AuthService{
		httpClient: httpClient,
	}
	for _, opt := range opts {
		opt(svc)
	}
	return svc
}

// WithConfigPath sets the config path for AuthService.
// WithConfigPath is used for tests.
func WithConfigPath(path string) func(*AuthService) {
	return func(s *AuthService) {
		s.configPath = path
	}
}

// WithRefreshFunc sets a custom refresh function for AuthService.
func WithRefreshFunc(f func(cfg *Config) error) func(*AuthService) {
	return func(s *AuthService) {
		s.refreshFunc = f
	}
}

// DeviceCodeResult contains the device code information for authentication
type DeviceCodeResult struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

// AuthenticateStage1 starts the authentication flow and returns device code info
// This is used by REST API for the first stage of authentication
func (s *AuthService) AuthenticateStage1(cfg *Config) (*DeviceCodeResult, error) {
	// Step 1: Get device code
	dc, err := s.getDeviceCode(cfg)
	if err != nil {
		return nil, fmt.Errorf("failed to get device code: %w", err)
	}

	Info("Device code generated", "user_code", dc.UserCode, "expires_in", dc.ExpiresIn)

	return &DeviceCodeResult{
		DeviceCode:      dc.DeviceCode,
		UserCode:        dc.UserCode,
		VerificationURI: dc.VerificationURI,
		ExpiresIn:       dc.ExpiresIn,
		Interval:        dc.Interval,
	}, nil
}

// AuthenticateStage2 completes the authentication flow using device code
// This is used by REST API for the second stage of authentication
// If pollMode is true, it will poll GitHub for authorization (CLI mode)
// If pollMode is false, it will only check once (frontend polling mode)
func (s *AuthService) AuthenticateStage2(email string, deviceCode string, interval int, expiresIn int, cfg *Config, pollMode bool) error {
	var githubToken string
	var err error

	// Step 2: Get GitHub token (poll or single check based on mode)
	if pollMode {
		// CLI mode: backend polls until authorized or timeout
		Info("Polling for GitHub token", "device_code", deviceCode, "interval", interval, "expires_in", expiresIn)
		githubToken, err = s.pollForGitHubToken(cfg, deviceCode, interval, expiresIn)
		if err != nil {
			return fmt.Errorf("failed to get GitHub token: %w", err)
		}
	} else {
		// Frontend polling mode: check once and return status
		Info("Checking GitHub token once", "device_code", deviceCode)
		githubToken, err = s.checkGitHubTokenOnce(cfg, deviceCode)
		if err != nil {
			return fmt.Errorf("failed to check GitHub token: %w", err)
		}
	}

	cfg.GitHubToken = githubToken

	// Step 3: Exchange GitHub token for Copilot token
	copilotToken, expiresAt, refreshIn, err := s.getCopilotToken(cfg, githubToken)
	if err != nil {
		return fmt.Errorf("failed to get Copilot token: %w", err)
	}

	cfg.CopilotToken = copilotToken
	cfg.ExpiresAt = expiresAt
	cfg.RefreshIn = refreshIn

	// Save to database
	_, err = s.updateTokenInDatabase(email, cfg)
	if err != nil {
		return fmt.Errorf("failed to save token to database: %w", err)
	}

	// Original file-based save (commented out for tracking)
	// var saveErr error
	// if s.configPath != "" {
	// 	saveErr = cfg.SaveConfig(s.configPath)
	// } else {
	// 	saveErr = cfg.SaveConfig()
	// }
	// if saveErr != nil {
	// 	return fmt.Errorf("failed to save config: %w", saveErr)
	// }

	Info("Authentication successful", "email", email)
	return nil
}

// Authenticate performs the full GitHub Copilot authentication flow (for CLI)
// This method combines Stage1 and Stage2 for interactive CLI usage
func (s *AuthService) Authenticate(email string, cfg *Config) error {
	now := time.Now().Unix()
	if cfg.CopilotToken != "" && cfg.ExpiresAt > now+60 {
		Info("Token still valid", "expires_in", cfg.ExpiresAt-now)
		return nil // Already authenticated
	}

	if cfg.CopilotToken != "" {
		Info("Token expired or expiring soon, triggering re-auth", "expires_in", cfg.ExpiresAt-now)
	} else {
		Info("No token found, starting authentication flow")
	}

	// Stage 1: Get device code
	dcResult, err := s.AuthenticateStage1(cfg)
	if err != nil {
		return err
	}

	fmt.Printf("\nTo authenticate, visit: %s\nEnter code: %s\n", dcResult.VerificationURI, dcResult.UserCode)

	// Stage 2: Complete authentication with polling enabled (CLI mode)
	err = s.AuthenticateStage2(email, dcResult.DeviceCode, dcResult.Interval, dcResult.ExpiresIn, cfg, true)
	if err != nil {
		return err
	}

	fmt.Println("Authentication successful!")
	return nil
}

// RefreshToken refreshes the Copilot token using the stored GitHub token
func (s *AuthService) RefreshToken(email string, cfg *Config) error {
	return s.RefreshTokenWithContext(context.Background(), email, cfg)
}

// RefreshTokenWithContext refreshes the Copilot token using the provided context and config.
func (s *AuthService) RefreshTokenWithContext(ctx context.Context, email string, cfg *Config) error {
	if s.refreshFunc != nil {
		// Use injected refresh function for tests
		return s.refreshFunc(cfg)

		// Original file-based save (commented out)
		// if s.configPath != "" {
		// 	return cfg.SaveConfig(s.configPath)
		// }
		// return cfg.SaveConfig()
	}

	if cfg.GitHubToken == "" {
		Warn("Cannot refresh token: no GitHub token available")
		return NewAuthError("no GitHub token available for refresh", nil)
	}

	// Retry with exponential backoff
	for attempt := 1; attempt <= maxRefreshRetries; attempt++ {
		Info("Attempting to refresh Copilot token", "attempt", attempt, "max_attempts", maxRefreshRetries)

		copilotToken, expiresAt, refreshIn, err := s.getCopilotToken(cfg, cfg.GitHubToken)
		if err != nil {
			if attempt == maxRefreshRetries {
				Error("Token refresh failed after max attempts", "attempts", maxRefreshRetries, "error", err)
				return err
			}

			// Wait before retry with exponential backoff
			waitTime := time.Duration(baseRetryDelay*attempt*attempt) * time.Second
			Warn("Token refresh failed, retrying", "attempt", attempt, "wait_time", waitTime, "error", err)

			// Use context-aware sleep
			select {
			case <-time.After(waitTime):
				continue
			case <-ctx.Done():
				return ctx.Err()
			}
		}

		Info("Token refresh successful", "expires_in", expiresAt-time.Now().Unix())
		cfg.CopilotToken = copilotToken
		cfg.ExpiresAt = expiresAt
		cfg.RefreshIn = refreshIn

		// Update to database instead of file
		_, err = s.updateTokenInDatabase(email, cfg)
		if err != nil {
			return fmt.Errorf("failed to update token in database: %w", err)
		}
		return nil

		// Original file-based save (commented out for tracking)
		// if s.configPath != "" {
		// 	return cfg.SaveConfig(s.configPath)
		// }
		// return cfg.SaveConfig()
	}

	return NewAuthError("maximum retry attempts exceeded", nil)
}

// EnsureValidToken ensures we have a valid token, refreshing if necessary
func (s *AuthService) EnsureValidToken(email string, baseConfig *Config) (*Config, error) {
	// Fetch token status from database
	cfg, err := s.fetchTokenFromDatabase(email)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch token from database: %w", err)
	}

	// Merge baseConfig settings into cfg (preserve tokens, update other settings from baseConfig)
	if baseConfig != nil {
		cfg.Port = baseConfig.Port
		cfg.AllowedModels = baseConfig.AllowedModels
		cfg.Headers = baseConfig.Headers
		cfg.CORS = baseConfig.CORS
		cfg.Timeouts = baseConfig.Timeouts
	}

	return s.EnsureValidTokenWithConfig(email, cfg)
}

// EnsureValidTokenWithConfig validates and refreshes token for a given config
// This method is useful for testing where config is provided directly
func (s *AuthService) EnsureValidTokenWithConfig(email string, cfg *Config) (*Config, error) {
	now := time.Now().Unix()
	if cfg.CopilotToken == "" {
		return nil, NewAuthError("no token available - authentication required", nil)
	}

	// Check if token needs refresh (within 5 minutes of expiry or already expired)
	if cfg.ExpiresAt <= now+300 {
		err := s.RefreshToken(email, cfg)
		if err != nil {
			return nil, err
		}
	}

	return cfg, nil
}

// fetchTokenFromDatabase fetches CopilotUser data from database
func (s *AuthService) fetchTokenFromDatabase(email string) (*Config, error) {
	return s.fetchTokenFromDatabaseWithContext(context.Background(), email)
}

// fetchTokenFromDatabaseWithContext fetches CopilotUser data from database with context
func (s *AuthService) fetchTokenFromDatabaseWithContext(ctx context.Context, email string) (*Config, error) {
	// Create request with context and timeout
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	url := fmt.Sprintf("%s?email=%s", getDatabaseURL(), email)
	req, err := http.NewRequestWithContext(reqCtx, "GET", url, http.NoBody)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			Warn("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode == http.StatusNotFound {
		return nil, NewAuthError("user not found in database", nil)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, NewNetworkError("fetchTokenFromDatabase", url, fmt.Sprintf("HTTP %d response", resp.StatusCode), nil)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Email        string `json:"email"`
			GithubToken  string `json:"githubToken"`
			CopilotToken string `json:"copilotToken"`
			ExpiresAt    int64  `json:"expiresAt,string"`
			RefreshIn    int64  `json:"refreshIn,string"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	if !result.Success {
		return nil, NewAuthError("failed to fetch token from database", nil)
	}

	// Create a new Config with only token-related fields from database
	// Other settings (Headers, CORS, Timeouts) will be merged from baseConfig in EnsureValidToken
	cfg := &Config{
		GitHubToken:  result.Data.GithubToken,
		CopilotToken: result.Data.CopilotToken,
		ExpiresAt:    result.Data.ExpiresAt,
		RefreshIn:    result.Data.RefreshIn,
	}

	return cfg, nil
}

// updateTokenInDatabase updates CopilotUser data in database
func (s *AuthService) updateTokenInDatabase(email string, cfg *Config) (bool, error) {
	return s.updateTokenInDatabaseWithContext(context.Background(), email, cfg)
}

// updateTokenInDatabaseWithContext updates CopilotUser data in database with context
func (s *AuthService) updateTokenInDatabaseWithContext(ctx context.Context, email string, cfg *Config) (bool, error) {
	// Create request with context and timeout
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()

	// Prepare request body
	requestBody := map[string]interface{}{
		"email":        email,
		"githubToken":  cfg.GitHubToken,
		"copilotToken": cfg.CopilotToken,
		"expiresAt":    cfg.ExpiresAt,
		"refreshIn":    cfg.RefreshIn,
	}

	jsonData, err := json.Marshal(requestBody)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request body: %w", err)
	}

	databaseURL := getDatabaseURL()
	req, err := http.NewRequestWithContext(reqCtx, "POST", databaseURL, strings.NewReader(string(jsonData)))
	if err != nil {
		return false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return false, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			Warn("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		return false, NewNetworkError("updateTokenInDatabase", databaseURL, fmt.Sprintf("HTTP %d response", resp.StatusCode), nil)
	}

	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Email        string `json:"email"`
			GithubToken  string `json:"githubToken"`
			CopilotToken string `json:"copilotToken"`
			ExpiresAt    string `json:"expiresAt"`
			RefreshIn    string `json:"refreshIn"`
		} `json:"data"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, err
	}

	if !result.Success {
		return false, NewAuthError("failed to update token in database", nil)
	}

	Info("Token updated in database successfully", "email", email)
	return true, nil
}

func (s *AuthService) getDeviceCode(cfg *Config) (*deviceCodeResponse, error) {
	body := fmt.Sprintf(`{"client_id":%q,"scope":%q}`, copilotClientID, copilotScope)
	req, err := http.NewRequest("POST", copilotDeviceCodeURL, strings.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", cfg.Headers.UserAgent)

	Info("Sending device code request", "url", copilotDeviceCodeURL)
	resp, err := s.httpClient.Do(req)
	if err != nil {
		Error("Device code request failed", "error", err)
		return nil, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			Warn("Error closing response body", "error", err)
		}
	}()

	var dc deviceCodeResponse
	if err := json.NewDecoder(resp.Body).Decode(&dc); err != nil {
		return nil, err
	}

	return &dc, nil
}

func (s *AuthService) pollForGitHubToken(cfg *Config, deviceCode string, interval int, expiresIn int) (string, error) {
	return s.pollForGitHubTokenWithContext(context.Background(), cfg, deviceCode, interval, expiresIn)
}

func (s *AuthService) pollForGitHubTokenWithContext(ctx context.Context, cfg *Config, deviceCode string, interval int, expiresIn int) (string, error) {
	// Calculate max iterations based on expiresIn and interval
	// Add a small buffer to account for network delays
	maxIterations := (expiresIn / interval) + 1

	for range maxIterations {
		// Use context-aware sleep
		select {
		case <-time.After(time.Duration(interval) * time.Second):
			// Continue with polling
		case <-ctx.Done():
			return "", ctx.Err()
		}

		body := fmt.Sprintf(`{"client_id":%q,"device_code":%q,"grant_type":"urn:ietf:params:oauth:grant-type:device_code"}`,
			copilotClientID, deviceCode)
		req, err := http.NewRequest("POST", copilotTokenURL, strings.NewReader(body))
		if err != nil {
			return "", err
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", cfg.Headers.UserAgent)

		resp, err := s.httpClient.Do(req)
		if err != nil {
			continue
		}

		var tr tokenResponse
		if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
			if err := resp.Body.Close(); err != nil {
				Warn("Error closing response body", "error", err)
			}
			continue
		}
		if err := resp.Body.Close(); err != nil {
			Warn("Error closing response body", "error", err)
		}

		if tr.Error != "" {
			if tr.Error == "authorization_pending" {
				continue
			}
			return "", NewAuthError(fmt.Sprintf("authorization failed: %s - %s", tr.Error, tr.ErrorDesc), nil)
		}

		if tr.AccessToken != "" {
			return tr.AccessToken, nil
		}
	}

	return "", NewAuthError("authentication timed out", nil)
}

// checkGitHubTokenOnce checks GitHub authorization status once without polling
// Returns authorization_pending error if user hasn't authorized yet
func (s *AuthService) checkGitHubTokenOnce(cfg *Config, deviceCode string) (string, error) {
	body := fmt.Sprintf(`{"client_id":%q,"device_code":%q,"grant_type":"urn:ietf:params:oauth:grant-type:device_code"}`,
		copilotClientID, deviceCode)
	req, err := http.NewRequest("POST", copilotTokenURL, strings.NewReader(body))
	if err != nil {
		return "", err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", cfg.Headers.UserAgent)

	Debug("Checking GitHub token once", "header", req.Header)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			Warn("Error closing response body", "error", err)
		}
	}()

	var tr tokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&tr); err != nil {
		return "", err
	}

	if tr.Error != "" {
		// Return the error as-is so caller can handle authorization_pending
		return "", NewAuthError(tr.Error, nil)
	}

	if tr.AccessToken != "" {
		return tr.AccessToken, nil
	}

	return "", NewAuthError("no access token in response", nil)
}

func (s *AuthService) getCopilotToken(cfg *Config, githubToken string) (token string, expiresAt, refreshIn int64, err error) {
	req, err := http.NewRequest("GET", copilotAPIKeyURL, http.NoBody)
	if err != nil {
		return "", 0, 0, err
	}
	req.Header.Set("Authorization", "token "+githubToken)
	req.Header.Set("User-Agent", cfg.Headers.UserAgent)

	Debug("Requesting Copilot token",
		"url", copilotAPIKeyURL,
		"method", "GET",
		"user_agent", cfg.Headers.UserAgent,
		"github_token_prefix", githubToken[:10]+"...")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		Error("Failed to request Copilot token", "error", err)
		return "", 0, 0, err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			Warn("Error closing response body", "error", err)
		}
	}()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		var errMsg error
		if readErr == nil && len(bodyBytes) > 0 {
			errMsg = fmt.Errorf("response body: %s", string(bodyBytes))
		}
		Error("Copilot token request failed",
			"status_code", resp.StatusCode,
			"status", resp.Status,
			"response_body", string(bodyBytes),
			"content_type", resp.Header.Get("Content-Type"))
		return "", 0, 0, NewNetworkError("get_copilot_token", copilotAPIKeyURL, fmt.Sprintf("HTTP %d response", resp.StatusCode), errMsg)
	}

	Info("Copilot token response received",
		"status_code", resp.StatusCode,
		"content_type", resp.Header.Get("Content-Type"))

	var ctr copilotTokenResponse
	if err := json.NewDecoder(resp.Body).Decode(&ctr); err != nil {
		return "", 0, 0, err
	}

	return ctr.Token, ctr.ExpiresAt, ctr.RefreshIn, nil
}
