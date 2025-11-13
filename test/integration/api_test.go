package integration_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/xdlhzdh/github-copilot-svcs/internal"
)

var (
	testServer *internal.Server
	baseURL    string
	cleanup    func()
)

// TestMain sets up and tears down the test server for all integration tests
func TestMain(m *testing.M) {
	// Set up test server
	var err error
	testServer, baseURL, cleanup, err = setupTestServer()
	if err != nil {
		fmt.Printf("Failed to setup test server: %v\n", err)
		os.Exit(1)
	}

	// Wait for server to be ready
	if !waitForServer(baseURL, 15*time.Second) {
		cleanup()
		fmt.Println("Server failed to start within timeout")
		os.Exit(1)
	}

	fmt.Printf("Test server ready at %s\n", baseURL)

	// Run tests
	code := m.Run()

	// Cleanup
	cleanup()

	os.Exit(code)
}

func TestHealthEndpoint(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
		expectedFields []string
	}{
		{
			name:           "basic health check",
			endpoint:       "/v1/health",
			expectedStatus: http.StatusOK,
			expectedFields: []string{"status", "timestamp", "version"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(baseURL + tt.endpoint)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}

			// Check response body is valid JSON with expected fields
			var result map[string]interface{}
			if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
				t.Errorf("Failed to decode JSON response: %v", err)
				return
			}

			for _, field := range tt.expectedFields {
				if _, exists := result[field]; !exists {
					t.Errorf("Expected field '%s' not found in response", field)
				}
			}

			// Verify status is "healthy"
			if status, ok := result["status"].(string); !ok || status != "healthy" {
				t.Errorf("Expected status 'healthy', got %v", result["status"])
			}
		})
	}
}

func TestModelsEndpoint(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		endpoint       string
		expectedStatus int
		checkJSON      bool
		expectedFields []string
	}{
		{
			name:           "get models list",
			method:         "GET",
			endpoint:       "/v1/models",
			expectedStatus: http.StatusOK,
			checkJSON:      true,
			expectedFields: []string{"object", "data"},
		},
		{
			name:           "models endpoint with POST method",
			method:         "POST",
			endpoint:       "/v1/models",
			expectedStatus: http.StatusOK, // Models endpoint accepts POST
			checkJSON:      true,
			expectedFields: []string{"object", "data"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, baseURL+tt.endpoint, http.NoBody)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				body, _ := io.ReadAll(resp.Body)
				t.Errorf("Expected status %d, got %d. Response: %s", tt.expectedStatus, resp.StatusCode, string(body))
			}

			if tt.checkJSON {
				var result map[string]interface{}
				if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
					t.Errorf("Failed to decode JSON response: %v", err)
					return
				}

				for _, field := range tt.expectedFields {
					if _, exists := result[field]; !exists {
						t.Errorf("Expected field '%s' not found in response", field)
					}
				}

				// Check that data is an array
				if data, ok := result["data"].([]interface{}); !ok {
					t.Errorf("Expected 'data' to be an array, got %T", result["data"])
				} else if len(data) == 0 {
					t.Log("Note: Models list is empty - this may be expected in test environment")
				}
			}
		})
	}
}

func TestChatCompletionsEndpoint(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		endpoint       string
		body           string
		expectedStatus int
		contentType    string
	}{
		{
			name:           "chat completions with empty body",
			method:         "POST",
			endpoint:       "/v1/chat/completions",
			body:           "",
			expectedStatus: http.StatusBadRequest,
			contentType:    "application/json",
		},
		{
			name:           "chat completions with invalid JSON",
			method:         "POST",
			endpoint:       "/v1/chat/completions?email=test@example.com",
			body:           `{"invalid": json}`,
			expectedStatus: http.StatusBadRequest,
			contentType:    "application/json",
		},
		{
			name:           "chat completions with wrong method",
			method:         "GET",
			endpoint:       "/v1/chat/completions",
			body:           "",
			expectedStatus: http.StatusMethodNotAllowed,
			contentType:    "application/json",
		},
		{
			name:           "chat completions with basic valid request",
			method:         "POST",
			endpoint:       "/v1/chat/completions",
			body:           `{"model":"gpt-4","messages":[{"role":"user","content":"test"}]}`,
			expectedStatus: http.StatusUnauthorized, // Should be 401 if auth is missing
			contentType:    "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}

			req, err := http.NewRequest(tt.method, baseURL+tt.endpoint, body)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}

			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				respBody, _ := io.ReadAll(resp.Body)
				t.Errorf("Expected status %d, got %d. Response: %s", tt.expectedStatus, resp.StatusCode, string(respBody))
			}
		})
	}
}

// TestCompletionsEndpoint mirrors TestChatCompletionsEndpoint but for /v1/completions
func TestCompletionsEndpoint(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		endpoint       string
		body           string
		expectedStatus int
		contentType    string
	}{
		{
			name:           "completions with empty body",
			method:         "POST",
			endpoint:       "/v1/completions",
			body:           "",
			expectedStatus: http.StatusBadRequest,
			contentType:    "application/json",
		},
		{
			name:           "completions with invalid JSON",
			method:         "POST",
			endpoint:       "/v1/completions?email=test@example.com",
			body:           `{"invalid": json}`,
			expectedStatus: http.StatusBadRequest,
			contentType:    "application/json",
		},
		{
			name:           "completions with wrong method",
			method:         "GET",
			endpoint:       "/v1/completions",
			body:           "",
			expectedStatus: http.StatusMethodNotAllowed,
			contentType:    "application/json",
		},
		{
			name:           "completions with basic valid request",
			method:         "POST",
			endpoint:       "/v1/completions",
			body:           `{"model":"gpt-4","prompt":"test"}`,
			expectedStatus: http.StatusUnauthorized, // Should be 401 if auth is missing
			contentType:    "application/json",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var body io.Reader
			if tt.body != "" {
				body = strings.NewReader(tt.body)
			}

			req, err := http.NewRequest(tt.method, baseURL+tt.endpoint, body)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}

			if tt.contentType != "" {
				req.Header.Set("Content-Type", tt.contentType)
			}

			client := &http.Client{Timeout: 10 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				respBody, _ := io.ReadAll(resp.Body)
				t.Errorf("Expected status %d, got %d. Response: %s", tt.expectedStatus, resp.StatusCode, string(respBody))
			}
		})
	}
}

func TestCORSHeaders(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       string
		origin         string
		method         string
		expectedStatus int
		checkCORS      bool
	}{
		{
			name:           "CORS preflight request",
			endpoint:       "/v1/models",
			origin:         "http://localhost:3000",
			method:         "OPTIONS",
			expectedStatus: http.StatusOK,
			checkCORS:      true,
		},
		{
			name:           "CORS actual request",
			endpoint:       "/v1/health",
			origin:         "http://localhost:3000",
			method:         "GET",
			expectedStatus: http.StatusOK,
			checkCORS:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, err := http.NewRequest(tt.method, baseURL+tt.endpoint, http.NoBody)
			if err != nil {
				t.Fatalf("Failed to create request: %v", err)
			}

			if tt.origin != "" {
				req.Header.Set("Origin", tt.origin)
			}

			if tt.method == "OPTIONS" {
				req.Header.Set("Access-Control-Request-Method", "GET")
				req.Header.Set("Access-Control-Request-Headers", "Content-Type")
			}

			client := &http.Client{Timeout: 5 * time.Second}
			resp, err := client.Do(req)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}

			if tt.checkCORS {
				// Check for CORS headers
				allowOrigin := resp.Header.Get("Access-Control-Allow-Origin")
				if allowOrigin == "" {
					t.Error("Expected Access-Control-Allow-Origin header")
				}

				if tt.method == "OPTIONS" {
					allowMethods := resp.Header.Get("Access-Control-Allow-Methods")
					if allowMethods == "" {
						t.Error("Expected Access-Control-Allow-Methods header for preflight")
					}
				}
			}
		})
	}
}

func TestErrorConditions(t *testing.T) {
	tests := []struct {
		name           string
		endpoint       string
		expectedStatus int
	}{
		{
			name:           "nonexistent endpoint",
			endpoint:       "/nonexistent",
			expectedStatus: http.StatusNotFound,
		},
		{
			name:           "invalid path",
			endpoint:       "/v1/invalid",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := http.Get(baseURL + tt.endpoint)
			if err != nil {
				t.Fatalf("Failed to make request: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}
		})
	}
}

func TestSecurityHeaders(t *testing.T) {
	resp, err := http.Get(baseURL + "/v1/health")
	if err != nil {
		t.Fatalf("Failed to make request: %v", err)
	}
	defer resp.Body.Close()

	expectedHeaders := map[string]string{
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"X-XSS-Protection":       "1; mode=block",
	}

	for header, expectedValue := range expectedHeaders {
		actualValue := resp.Header.Get(header)
		if actualValue != expectedValue {
			t.Errorf("Expected header %s to be '%s', got '%s'", header, expectedValue, actualValue)
		}
	}
}

func TestServerShutdown(t *testing.T) {
	// This test verifies that the server can be gracefully shut down
	// We'll create a separate server instance for this test
	server, serverURL, shutdownFunc, err := setupTestServer()
	if err != nil {
		t.Fatalf("Failed to setup test server: %v", err)
	}

	// Wait for server to be ready
	if !waitForServer(serverURL, 5*time.Second) {
		shutdownFunc()
		t.Fatal("Server failed to start within timeout")
	}

	// Make a request to ensure server is working
	resp, err := http.Get(serverURL + "/v1/health")
	if err != nil {
		shutdownFunc()
		t.Fatalf("Failed to make request: %v", err)
	}
	resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status %d, got %d", http.StatusOK, resp.StatusCode)
	}

	// Shutdown the server
	shutdownFunc()

	// Verify server is no longer responding
	time.Sleep(200 * time.Millisecond) // Give time for shutdown
	resp2, err := http.Get(serverURL + "/v1/health")
	if resp2 != nil {
		defer resp2.Body.Close()
	}
	if err == nil {
		t.Error("Expected server to be shut down, but it's still responding")
	}

	_ = server // Use server variable to avoid unused warning
}

// setupTestServer creates a test server instance and returns cleanup function
func setupTestServer() (server *internal.Server, baseURL string, cleanup func(), err error) {
	// Find an available port
	listener, err := net.Listen("tcp", ":0")
	if err != nil {
		return nil, "", nil, fmt.Errorf("failed to find available port: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	listener.Close()

	// Create test configuration with proper defaults
	cfg := &internal.Config{
		Port: port,
	}

	// Set default headers to prevent validation errors
	internal.SetDefaultHeaders(cfg)
	internal.SetDefaultCORS(cfg)
	internal.SetDefaultTimeouts(cfg)

	// Create HTTP client for the server
	httpClient := &http.Client{
		Timeout: 30 * time.Second,
	}

	// Create server instance
	server = internal.NewServer(cfg, httpClient)
	baseURL = fmt.Sprintf("http://localhost:%d", port)

	// Start server in background goroutine
	serverErrCh := make(chan error, 1)

	go func() {
		// For testing, we'll just call Start() which blocks
		if err := server.Start(); err != nil && err != http.ErrServerClosed {
			serverErrCh <- err
		}
	}()

	cleanup = func() {
		if server != nil {
			if err := server.Stop(); err != nil {
				fmt.Printf("Error stopping server: %v\n", err)
			}
		}
		// Give server time to shutdown gracefully
		time.Sleep(200 * time.Millisecond)
	}

	// Check for immediate startup errors
	select {
	case err := <-serverErrCh:
		cleanup()
		return nil, "", nil, fmt.Errorf("server failed to start: %w", err)
	case <-time.After(1 * time.Second):
		// Server seems to be starting OK
	}

	return server, baseURL, cleanup, nil
}

// waitForServer waits for the server to be ready to accept connections
func waitForServer(baseURL string, timeout time.Duration) bool {
	client := &http.Client{Timeout: 1 * time.Second}
	deadline := time.Now().Add(timeout)

	for time.Now().Before(deadline) {
		resp, err := client.Get(baseURL + "/v1/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return true
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return false
}
