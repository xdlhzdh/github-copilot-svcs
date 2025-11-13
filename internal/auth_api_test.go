package internal_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/xdlhzdh/github-copilot-svcs/internal"
)

func TestAuthAPIService_Handler_InvalidMethod(t *testing.T) {
	cfg := createAuthTestConfig()
	httpClient := &http.Client{}
	authService := internal.NewAuthService(httpClient)
	authAPIService := internal.NewAuthAPIService(authService, cfg)

	req := httptest.NewRequest("GET", "/v1/auth/github", nil)
	rr := httptest.NewRecorder()

	handler := authAPIService.Handler()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusMethodNotAllowed {
		t.Errorf("Handler returned wrong status code: got %v want %v",
			status, http.StatusMethodNotAllowed)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["success"] != false {
		t.Errorf("Expected success=false, got %v", response["success"])
	}
}

func TestAuthAPIService_Handler_EmptyBody(t *testing.T) {
	cfg := createAuthTestConfig()
	httpClient := &http.Client{}
	authService := internal.NewAuthService(httpClient)
	authAPIService := internal.NewAuthAPIService(authService, cfg)

	req := httptest.NewRequest("POST", "/v1/auth/github", bytes.NewReader([]byte("")))
	rr := httptest.NewRecorder()

	handler := authAPIService.Handler()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Handler returned wrong status code: got %v want %v",
			status, http.StatusBadRequest)
	}
}

func TestAuthAPIService_Handler_InvalidJSON(t *testing.T) {
	cfg := createAuthTestConfig()
	httpClient := &http.Client{}
	authService := internal.NewAuthService(httpClient)
	authAPIService := internal.NewAuthAPIService(authService, cfg)

	req := httptest.NewRequest("POST", "/v1/auth/github", bytes.NewReader([]byte("invalid json")))
	rr := httptest.NewRecorder()

	handler := authAPIService.Handler()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Handler returned wrong status code: got %v want %v",
			status, http.StatusBadRequest)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["success"] != false {
		t.Errorf("Expected success=false, got %v", response["success"])
	}

	if response["error"] != "invalid JSON format" {
		t.Errorf("Expected error about invalid JSON, got %v", response["error"])
	}
}

func TestAuthAPIService_Handler_MissingEmail(t *testing.T) {
	cfg := createAuthTestConfig()
	httpClient := &http.Client{}
	authService := internal.NewAuthService(httpClient)
	authAPIService := internal.NewAuthAPIService(authService, cfg)

	reqBody := map[string]string{}
	jsonData, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/auth/github", bytes.NewReader(jsonData))
	rr := httptest.NewRecorder()

	handler := authAPIService.Handler()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Handler returned wrong status code: got %v want %v",
			status, http.StatusBadRequest)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["success"] != false {
		t.Errorf("Expected success=false, got %v", response["success"])
	}

	if response["error"] != "email is required" {
		t.Errorf("Expected error about missing email, got %v", response["error"])
	}
}

func TestAuthAPIService_Handler_InvalidEmail(t *testing.T) {
	cfg := createAuthTestConfig()
	httpClient := &http.Client{}
	authService := internal.NewAuthService(httpClient)
	authAPIService := internal.NewAuthAPIService(authService, cfg)

	reqBody := map[string]string{
		"email": "invalid-email",
	}
	jsonData, _ := json.Marshal(reqBody)

	req := httptest.NewRequest("POST", "/v1/auth/github", bytes.NewReader(jsonData))
	rr := httptest.NewRecorder()

	handler := authAPIService.Handler()
	handler.ServeHTTP(rr, req)

	if status := rr.Code; status != http.StatusBadRequest {
		t.Errorf("Handler returned wrong status code: got %v want %v",
			status, http.StatusBadRequest)
	}

	var response map[string]interface{}
	if err := json.NewDecoder(rr.Body).Decode(&response); err != nil {
		t.Fatalf("Failed to decode response: %v", err)
	}

	if response["success"] != false {
		t.Errorf("Expected success=false, got %v", response["success"])
	}

	if response["error"] != "invalid email format" {
		t.Errorf("Expected error about invalid email format, got %v", response["error"])
	}
}

func TestAuthAPIService_Handler_ValidRequest(t *testing.T) {
	// This test requires mocking the authentication flow
	// For now, we just test the request validation
	t.Skip("Skipping integration test - requires mock GitHub API")
}
