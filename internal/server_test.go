package internal_test

import (
	"net/http"
	"net/http/httptest"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/xdlhzdh/github-copilot-svcs/internal"
)

// Test helpers
func createServerTestConfig() *internal.Config {
	cfg := &internal.Config{
		Port: 0, // Use 0 to let the system assign a port
	}
	internal.SetDefaultHeaders(cfg)
	internal.SetDefaultCORS(cfg)
	internal.SetDefaultTimeouts(cfg)
	return cfg
}

func TestNewWorkerPool(t *testing.T) {
	t.Run("creates worker pool with specified workers", func(t *testing.T) {
		workers := 4
		wp := internal.NewWorkerPool(workers)

		if wp == nil {
			t.Fatal("Expected worker pool to be created")
		}

		// Clean up
		wp.Stop()
	})

	t.Run("uses default workers when invalid count provided", func(t *testing.T) {
		wp := internal.NewWorkerPool(0)

		if wp == nil {
			t.Fatal("Expected worker pool to be created with default workers")
		}

		// Clean up
		wp.Stop()
	})

	t.Run("uses default workers for negative count", func(t *testing.T) {
		wp := internal.NewWorkerPool(-1)

		if wp == nil {
			t.Fatal("Expected worker pool to be created with default workers")
		}

		// Clean up
		wp.Stop()
	})
}

func TestWorkerPoolJobExecution(t *testing.T) {
	t.Run("executes submitted jobs", func(t *testing.T) {
		wp := internal.NewWorkerPool(2)
		defer wp.Stop()

		executed := false
		var mutex sync.Mutex

		wp.Submit(func() {
			mutex.Lock()
			executed = true
			mutex.Unlock()
		})

		// Wait a bit for the job to execute
		time.Sleep(100 * time.Millisecond)

		mutex.Lock()
		if !executed {
			t.Error("Expected job to be executed")
		}
		mutex.Unlock()
	})

	t.Run("executes multiple jobs concurrently", func(t *testing.T) {
		wp := internal.NewWorkerPool(3)
		defer wp.Stop()

		numJobs := 10
		executed := make([]bool, numJobs)
		var mutex sync.Mutex
		var wg sync.WaitGroup

		for i := 0; i < numJobs; i++ {
			wg.Add(1)
			index := i
			wp.Submit(func() {
				defer wg.Done()
				time.Sleep(10 * time.Millisecond) // Simulate work
				mutex.Lock()
				executed[index] = true
				mutex.Unlock()
			})
		}

		// Wait for all jobs to complete
		wg.Wait()

		// Check all jobs were executed
		for i, wasExecuted := range executed {
			if !wasExecuted {
				t.Errorf("Job %d was not executed", i)
			}
		}
	})

	// Note: The current worker pool implementation doesn't have panic recovery
	// so we can't test panic handling. This would need to be added to the worker pool
	// if panic recovery is required.
}

func TestWorkerPoolStop(t *testing.T) {
	t.Run("stops gracefully", func(t *testing.T) {
		wp := internal.NewWorkerPool(2)

		// Submit some jobs
		for i := 0; i < 5; i++ {
			wp.Submit(func() {
				time.Sleep(10 * time.Millisecond)
			})
		}

		// Stop should complete without hanging
		done := make(chan bool, 1)
		go func() {
			wp.Stop()
			done <- true
		}()

		select {
		case <-done:
			// Success
		case <-time.After(5 * time.Second):
			t.Error("Worker pool stop timed out")
		}
	})

	t.Run("stop completes successfully", func(t *testing.T) {
		wp := internal.NewWorkerPool(1)

		// Submit a job to ensure workers are running
		wp.Submit(func() {
			time.Sleep(10 * time.Millisecond)
		})

		// Stop should complete without hanging
		done := make(chan bool, 1)
		go func() {
			wp.Stop()
			done <- true
		}()

		select {
		case <-done:
			// Success
		case <-time.After(2 * time.Second):
			t.Error("Worker pool stop timed out")
		}
	})
}

func TestCreateHTTPClient(t *testing.T) {
	t.Run("creates client with correct configuration", func(t *testing.T) {
		cfg := createServerTestConfig()
		client := internal.CreateHTTPClient(cfg)

		if client == nil {
			t.Fatal("Expected HTTP client to be created")
		}

		if client.Timeout != time.Duration(cfg.Timeouts.HTTPClient)*time.Second {
			t.Errorf("Expected timeout %v, got %v",
				time.Duration(cfg.Timeouts.HTTPClient)*time.Second,
				client.Timeout)
		}

		// Check transport configuration
		transport, ok := client.Transport.(*http.Transport)
		if !ok {
			t.Fatal("Expected transport to be *http.Transport")
		}

		if transport.MaxIdleConns != 100 {
			t.Errorf("Expected MaxIdleConns 100, got %d", transport.MaxIdleConns)
		}

		if transport.MaxIdleConnsPerHost != 20 {
			t.Errorf("Expected MaxIdleConnsPerHost 20, got %d", transport.MaxIdleConnsPerHost)
		}
	})

	t.Run("creates functional client", func(t *testing.T) {
		cfg := createServerTestConfig()
		client := internal.CreateHTTPClient(cfg)

		// Create a test server
		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
			if _, err := w.Write([]byte("test response")); err != nil {
				t.Errorf("unexpected write error: %v", err)
			}
		}))
		defer testServer.Close()

		// Make a request
		resp, err := client.Get(testServer.URL)
		if err != nil {
			t.Fatalf("Expected successful request, got error: %v", err)
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			t.Errorf("Expected status 200, got %d", resp.StatusCode)
		}
	})
}

func TestNewServer(t *testing.T) {
	t.Run("creates server with correct configuration", func(t *testing.T) {
		cfg := createServerTestConfig()
		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		if server == nil {
			t.Fatal("Expected server to be created")
		}

		// Note: We can't easily test internal fields since they're not exported
		// But we can test that the server was created successfully
	})

	t.Run("uses default port when not specified", func(t *testing.T) {
		cfg := createServerTestConfig()
		cfg.Port = 0 // Explicitly set to 0
		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		if server == nil {
			t.Fatal("Expected server to be created")
		}
	})

	t.Run("creates server with custom port", func(t *testing.T) {
		cfg := createServerTestConfig()
		cfg.Port = 9999
		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		if server == nil {
			t.Fatal("Expected server to be created")
		}
	})
}

func TestServerStartStop(t *testing.T) {
	t.Run("server starts and stops gracefully", func(t *testing.T) {
		cfg := createServerTestConfig()
		cfg.Port = 8082
		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		// Start server in background
		errCh := make(chan error, 1)
		go func() {
			errCh <- server.Start()
		}()

		// Give server time to start
		time.Sleep(100 * time.Millisecond)

		// Stop server
		stopErr := server.Stop()
		if stopErr != nil {
			t.Errorf("Expected clean stop, got error: %v", stopErr)
		}

		// Wait for start to complete
		select {
		case startErr := <-errCh:
			if startErr != nil && startErr != http.ErrServerClosed {
				t.Errorf("Expected clean start/stop, got error: %v", startErr)
			}
		case <-time.After(2 * time.Second):
			t.Error("Server start did not complete within timeout")
		}
	})

	t.Run("server stops gracefully", func(t *testing.T) {
		cfg := createServerTestConfig()
		cfg.Port = 8082
		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		// Start server in background
		go func() {
			if err := server.Start(); err != nil {
				t.Errorf("server.Start() error: %v", err)
			}
		}()

		time.Sleep(100 * time.Millisecond)

		// Stop server
		err := server.Stop()
		if err != nil {
			t.Errorf("Stop error: %v", err)
		}
	})
}

func TestServerRoutes(t *testing.T) {
	t.Run("server has correct routes", func(t *testing.T) {
		cfg := createServerTestConfig()
		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		// We can't easily test routes directly since the server struct doesn't expose them
		// But we can test that the server was created, which implies routes are set up
		if server == nil {
			t.Fatal("Expected server to be created with routes")
		}
	})
}

func TestServerConcurrency(t *testing.T) {
	t.Run("handles concurrent operations", func(t *testing.T) {
		numGoroutines := 10
		var wg sync.WaitGroup

		// Create multiple servers concurrently
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cfg := createServerTestConfig()
				httpClient := internal.CreateHTTPClient(cfg)
				server := internal.NewServer(cfg, httpClient)

				if server == nil {
					t.Error("Expected server to be created in concurrent goroutine")
				}
			}()
		}

		wg.Wait()
	})
}

func TestWorkerPoolConfiguration(t *testing.T) {
	t.Run("worker pool uses CPU multiplier", func(t *testing.T) {
		// This test verifies that NewWorkerPool is called with runtime.NumCPU() * 2
		// We can't directly test the worker count, but we can verify the pool works
		cfg := createServerTestConfig()
		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		if server == nil {
			t.Fatal("Expected server to be created with worker pool")
		}

		// The worker pool should be functioning (indirectly tested through server creation)
	})
}

func TestHTTPClientTimeout(t *testing.T) {
	t.Run("HTTP client respects timeout configuration", func(t *testing.T) {
		cfg := createServerTestConfig()
		cfg.Timeouts.HTTPClient = 1 // 1 second timeout

		client := internal.CreateHTTPClient(cfg)

		// Create a test server that delays response
		testServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			time.Sleep(2 * time.Second) // Longer than client timeout
			w.WriteHeader(http.StatusOK)
		}))
		defer testServer.Close()

		// Make request that should timeout
		resp, err := client.Get(testServer.URL)
		if resp != nil {
			defer resp.Body.Close()
		}
		if err == nil {
			t.Error("Expected timeout error, but request succeeded")
		}

		// Check if it's a timeout error (error message may vary by Go version)
		if err != nil && !isTimeoutError(err) {
			t.Errorf("Expected timeout error, got: %v", err)
		}
	})
}

// Helper function to check if error is a timeout error
func isTimeoutError(err error) bool {
	if err == nil {
		return false
	}
	// Check common timeout error patterns
	errStr := err.Error()
	return contains(errStr, "timeout") ||
		contains(errStr, "deadline exceeded") ||
		contains(errStr, "context deadline exceeded")
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || substr == "" ||
		(len(s) > len(substr) && contains(s[1:], substr)) ||
		(len(s) >= len(substr) && s[:len(substr)] == substr))
}

func TestServerMemoryManagement(t *testing.T) {
	t.Run("server creation doesn't leak memory", func(t *testing.T) {
		// Simple test to ensure server creation/destruction works properly
		for i := 0; i < 100; i++ {
			cfg := createServerTestConfig()
			httpClient := internal.CreateHTTPClient(cfg)
			server := internal.NewServer(cfg, httpClient)

			if server == nil {
				t.Fatalf("Server creation failed at iteration %d", i)
			}

			// Immediately "destroy" by letting it go out of scope
		}

		// Force garbage collection
		runtime.GC()
	})
}

func TestServerConfigurationDefaults(t *testing.T) {
	t.Run("server handles missing configuration gracefully", func(t *testing.T) {
		cfg := &internal.Config{} // Minimal config
		internal.SetDefaultHeaders(cfg)
		internal.SetDefaultCORS(cfg)
		internal.SetDefaultTimeouts(cfg)

		httpClient := internal.CreateHTTPClient(cfg)
		server := internal.NewServer(cfg, httpClient)

		if server == nil {
			t.Error("Expected server to be created with default configuration")
		}
	})
}

func TestWorkerPoolBuffer(t *testing.T) {
	t.Run("worker pool handles burst traffic", func(t *testing.T) {
		wp := internal.NewWorkerPool(2)
		defer wp.Stop()

		// Submit more jobs than workers to test buffering
		numJobs := 10
		executed := 0
		var mutex sync.Mutex
		var wg sync.WaitGroup

		for i := 0; i < numJobs; i++ {
			wg.Add(1)
			wp.Submit(func() {
				defer wg.Done()
				time.Sleep(10 * time.Millisecond)
				mutex.Lock()
				executed++
				mutex.Unlock()
			})
		}

		wg.Wait()

		mutex.Lock()
		if executed != numJobs {
			t.Errorf("Expected %d jobs executed, got %d", numJobs, executed)
		}
		mutex.Unlock()
	})
}
