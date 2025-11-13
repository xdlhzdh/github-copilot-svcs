package internal

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// AuthAPIService provides authentication API endpoints
type AuthAPIService struct {
	authService *AuthService
	config      *Config
}

// NewAuthAPIService creates a new authentication API service
func NewAuthAPIService(authService *AuthService, config *Config) *AuthAPIService {
	return &AuthAPIService{
		authService: authService,
		config:      config,
	}
}

// Stage1Request represents the request body for stage 1 (device code generation)
type Stage1Request struct {
	Email string `json:"email"`
}

// Stage1Response represents the response for stage 1
type Stage1Response struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Data    *struct {
		DeviceCode      string `json:"device_code"`
		UserCode        string `json:"user_code"`
		VerificationURI string `json:"verification_uri"`
		ExpiresIn       int    `json:"expires_in"`
		Interval        int    `json:"interval"`
	} `json:"data,omitempty"`
}

// Stage2Request represents the request body for stage 2 (token completion)
type Stage2Request struct {
	Email      string `json:"email"`
	DeviceCode string `json:"device_code"`
	Interval   int    `json:"interval"`
	ExpiresIn  int    `json:"expires_in"`
	PollMode   bool   `json:"poll_mode"` // true for CLI (backend polls), false for frontend polling (single check)
}

// Stage2Response represents the response for stage 2
type Stage2Response struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Data    *struct {
		Email        string `json:"email"`
		CopilotToken string `json:"copilot_token,omitempty"`
		ExpiresAt    int64  `json:"expires_at,omitempty"`
		RefreshIn    int64  `json:"refresh_in,omitempty"`
	} `json:"data,omitempty"`
}

// AuthenticateRequest represents the request body for full authentication (deprecated)
type AuthenticateRequest struct {
	Email string `json:"email"`
}

// AuthenticateResponse represents the response for full authentication (deprecated)
type AuthenticateResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Error   string `json:"error,omitempty"`
	Data    *struct {
		Email        string `json:"email"`
		CopilotToken string `json:"copilot_token,omitempty"`
		ExpiresAt    int64  `json:"expires_at,omitempty"`
		RefreshIn    int64  `json:"refresh_in,omitempty"`
	} `json:"data,omitempty"`
}

// Stage1Handler returns an HTTP handler for stage 1 (device code generation)
func (s *AuthAPIService) Stage1Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST method
		if r.Method != http.MethodPost {
			s.sendStage1ErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Read request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			Error("Failed to read request body", "error", err)
			s.sendStage1ErrorResponse(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		defer func() {
			if err := r.Body.Close(); err != nil {
				Warn("Error closing request body", "error", err)
			}
		}()

		// Parse request
		var req Stage1Request
		if err := json.Unmarshal(body, &req); err != nil {
			Error("Failed to parse request JSON", "error", err)
			s.sendStage1ErrorResponse(w, http.StatusBadRequest, "invalid JSON format")
			return
		}

		// Validate email
		if req.Email == "" {
			s.sendStage1ErrorResponse(w, http.StatusBadRequest, "email is required")
			return
		}

		if !isValidEmail(req.Email) {
			s.sendStage1ErrorResponse(w, http.StatusBadRequest, "invalid email format")
			return
		}

		Info("Starting authentication stage 1 for user", "email", req.Email)

		// Call AuthenticateStage1
		dcResult, err := s.authService.AuthenticateStage1(s.config)
		if err != nil {
			Error("Stage 1 authentication failed", "email", req.Email, "error", err)
			s.sendStage1ErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		Info("Stage 1 authentication successful", "email", req.Email, "user_code", dcResult.UserCode)

		// Send success response
		response := Stage1Response{
			Success: true,
			Message: "device code generated successfully",
			Data: &struct {
				DeviceCode      string `json:"device_code"`
				UserCode        string `json:"user_code"`
				VerificationURI string `json:"verification_uri"`
				ExpiresIn       int    `json:"expires_in"`
				Interval        int    `json:"interval"`
			}{
				DeviceCode:      dcResult.DeviceCode,
				UserCode:        dcResult.UserCode,
				VerificationURI: dcResult.VerificationURI,
				ExpiresIn:       dcResult.ExpiresIn,
				Interval:        dcResult.Interval,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			Error("Failed to encode response", "error", err)
		}
	}
}

// Stage2Handler returns an HTTP handler for stage 2 (token completion)
func (s *AuthAPIService) Stage2Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST method
		if r.Method != http.MethodPost {
			s.sendStage2ErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Read request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			Error("Failed to read request body", "error", err)
			s.sendStage2ErrorResponse(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		defer func() {
			if err := r.Body.Close(); err != nil {
				Warn("Error closing request body", "error", err)
			}
		}()

		// Parse request
		var req Stage2Request
		if err := json.Unmarshal(body, &req); err != nil {
			Error("Failed to parse request JSON", "error", err)
			s.sendStage2ErrorResponse(w, http.StatusBadRequest, "invalid JSON format")
			return
		}

		// Validate fields
		if req.Email == "" {
			s.sendStage2ErrorResponse(w, http.StatusBadRequest, "email is required")
			return
		}

		if !isValidEmail(req.Email) {
			s.sendStage2ErrorResponse(w, http.StatusBadRequest, "invalid email format")
			return
		}

		if req.DeviceCode == "" {
			s.sendStage2ErrorResponse(w, http.StatusBadRequest, "device_code is required")
			return
		}

		if req.Interval <= 0 {
			s.sendStage2ErrorResponse(w, http.StatusBadRequest, "interval must be positive")
			return
		}

		Info("Starting authentication stage 2 for user", "email", req.Email, "poll_mode", req.PollMode)

		// Call AuthenticateStage2 with poll mode
		// If poll_mode is false (frontend polling), only check once and return authorization_pending if not ready
		err = s.authService.AuthenticateStage2(req.Email, req.DeviceCode, req.Interval, req.ExpiresIn, s.config, req.PollMode)
		if err != nil {
			// If it's authorization_pending error and frontend polling mode, return 202 Accepted
			// GitHub returns "authorization_pending" when user hasn't completed authorization yet
			if !req.PollMode && strings.Contains(err.Error(), "authorization_pending") {
				Info("Stage 2 authentication pending", "email", req.Email)
				s.sendStage2PendingResponse(w)
			} else {
				// Other errors: return 500 Internal Server Error
				Error("Stage 2 authentication failed", "email", req.Email, "error", err)
				s.sendStage2ErrorResponse(w, http.StatusInternalServerError, err.Error())
			}
			return
		}

		// Fetch the updated token info from database
		cfg, err := s.authService.fetchTokenFromDatabase(req.Email)
		if err != nil {
			Error("Failed to fetch token after authentication", "email", req.Email, "error", err)
			s.sendStage2ErrorResponse(w, http.StatusInternalServerError, "authentication succeeded but failed to retrieve token info")
			return
		}

		Info("Stage 2 authentication successful", "email", req.Email)

		// Send success response
		response := Stage2Response{
			Success: true,
			Message: "authentication completed successfully",
			Data: &struct {
				Email        string `json:"email"`
				CopilotToken string `json:"copilot_token,omitempty"`
				ExpiresAt    int64  `json:"expires_at,omitempty"`
				RefreshIn    int64  `json:"refresh_in,omitempty"`
			}{
				Email:        req.Email,
				CopilotToken: cfg.CopilotToken,
				ExpiresAt:    cfg.ExpiresAt,
				RefreshIn:    cfg.RefreshIn,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			Error("Failed to encode response", "error", err)
		}
	}
}

// Handler returns an HTTP handler for the full authentication endpoint (deprecated, for backward compatibility)
func (s *AuthAPIService) Handler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Only allow POST method
		if r.Method != http.MethodPost {
			s.sendErrorResponse(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Read request body
		body, err := io.ReadAll(r.Body)
		if err != nil {
			Error("Failed to read request body", "error", err)
			s.sendErrorResponse(w, http.StatusBadRequest, "failed to read request body")
			return
		}
		defer func() {
			if err := r.Body.Close(); err != nil {
				Warn("Error closing request body", "error", err)
			}
		}()

		// Parse request
		var req AuthenticateRequest
		if err := json.Unmarshal(body, &req); err != nil {
			Error("Failed to parse request JSON", "error", err)
			s.sendErrorResponse(w, http.StatusBadRequest, "invalid JSON format")
			return
		}

		// Validate email
		if req.Email == "" {
			s.sendErrorResponse(w, http.StatusBadRequest, "email is required")
			return
		}

		if !isValidEmail(req.Email) {
			s.sendErrorResponse(w, http.StatusBadRequest, "invalid email format")
			return
		}

		Info("Starting authentication for user", "email", req.Email)

		// Call Authenticate function
		err = s.authService.Authenticate(req.Email, s.config)
		if err != nil {
			Error("Authentication failed", "email", req.Email, "error", err)
			s.sendErrorResponse(w, http.StatusInternalServerError, err.Error())
			return
		}

		// Fetch the updated token info from database
		cfg, err := s.authService.fetchTokenFromDatabase(req.Email)
		if err != nil {
			Error("Failed to fetch token after authentication", "email", req.Email, "error", err)
			s.sendErrorResponse(w, http.StatusInternalServerError, "authentication succeeded but failed to retrieve token info")
			return
		}

		Info("Authentication successful", "email", req.Email)

		// Send success response
		response := AuthenticateResponse{
			Success: true,
			Message: "authentication successful",
			Data: &struct {
				Email        string `json:"email"`
				CopilotToken string `json:"copilot_token,omitempty"`
				ExpiresAt    int64  `json:"expires_at,omitempty"`
				RefreshIn    int64  `json:"refresh_in,omitempty"`
			}{
				Email:        req.Email,
				CopilotToken: cfg.CopilotToken,
				ExpiresAt:    cfg.ExpiresAt,
				RefreshIn:    cfg.RefreshIn,
			},
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		if err := json.NewEncoder(w).Encode(response); err != nil {
			Error("Failed to encode response", "error", err)
		}
	}
}

func (s *AuthAPIService) sendErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := AuthenticateResponse{
		Success: false,
		Error:   message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		Error("Failed to encode error response", "error", err)
	}
}

func (s *AuthAPIService) sendStage1ErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := Stage1Response{
		Success: false,
		Error:   message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		Error("Failed to encode error response", "error", err)
	}
}

func (s *AuthAPIService) sendStage2ErrorResponse(w http.ResponseWriter, statusCode int, message string) {
	response := Stage2Response{
		Success: false,
		Error:   message,
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(statusCode)
	if err := json.NewEncoder(w).Encode(response); err != nil {
		Error("Failed to encode error response", "error", err)
	}
}

func (s *AuthAPIService) sendStage2PendingResponse(w http.ResponseWriter) {
	response := Stage2Response{
		Success: false,
		Error:   "authorization_pending",
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted) // 202 Accepted
	if err := json.NewEncoder(w).Encode(response); err != nil {
		Error("Failed to encode pending response", "error", err)
	}
}
