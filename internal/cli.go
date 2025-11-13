package internal

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"regexp"
	"time"

	"github.com/xdlhzdh/github-copilot-svcs/pkg/transform"
)

// Command constants to avoid goconst errors
const (
	cmdAuth    = "auth"
	cmdRun     = "run"
	cmdStart   = "start"
	cmdModels  = "models"
	cmdConfig  = "config"
	cmdStatus  = "status"
	cmdRefresh = "refresh"

	// Constants to avoid magic numbers
	defaultRefreshThreshold = 300 // 5 minutes minimum refresh threshold
	secondsInMinute         = 60
	refreshPercentThreshold = 5 // 20% = 1/5
)

// emailRegex is a simple regex pattern for validating email addresses
var emailRegex = regexp.MustCompile(`^[a-zA-Z0-9._%+\-]+@[a-zA-Z0-9.\-]+\.[a-zA-Z]{2,}$`)

// isValidEmail checks if the provided string is a valid email address
func isValidEmail(email string) bool {
	return emailRegex.MatchString(email)
}

// PrintUsage prints the command usage information
func PrintUsage() {
	fmt.Printf(`GitHub Copilot SVCS Proxy

A reverse proxy for GitHub Copilot providing OpenAI-compatible endpoints.

Usage:
  %s [command] [options]

Commands:
  start    Start the proxy server (default)
  auth     Authenticate with GitHub Copilot using device flow (requires email)
  status   Show detailed authentication and token status
  config   Display current configuration details
  models   List all available AI models
  refresh  Manually force token refresh (requires email)
  help     Show this help message
  version  Show version information

Examples:
  %s auth user@example.com      # Authenticate with GitHub using email
  %s run --port 8080            # Run server on port 8080
  %s status --json              # Show status in JSON format
  %s refresh user@example.com   # Force refresh token for specific user

Environment Variables:
  COPILOT_PORT      Server port (default: 8081)
  GITHUB_TOKEN      GitHub OAuth token
  COPILOT_TOKEN     GitHub Copilot API token
  LOG_LEVEL         Log level (debug, info, warn, error)

Options:
`, os.Args[0], os.Args[0], os.Args[0], os.Args[0], os.Args[0])
	flag.PrintDefaults()
}

// RunCommand executes the specified command with arguments
func RunCommand(command string, args []string, version string) error {
	// Check for flags
	jsonOutput := len(args) >= 1 && args[0] == "--json"

	switch command {
	case cmdAuth:
		// Validate that exactly one argument is provided and it's a valid email
		if len(args) != 1 {
			return fmt.Errorf("auth command requires exactly one argument (email address), got %d arguments", len(args))
		}
		email := args[0]
		if !isValidEmail(email) {
			return fmt.Errorf("invalid email format: %s", email)
		}
		return handleAuth(email)
	case cmdRun, cmdStart:
		return handleRun()
	case cmdModels:
		return handleModels()
	case cmdConfig:
		return handleConfig()
	case cmdStatus:
		return handleStatusWithFormat(jsonOutput)
	case cmdRefresh:
		// Validate that exactly one argument is provided and it's a valid email
		if len(args) != 1 {
			return fmt.Errorf("refresh command requires exactly one argument (email address), got %d arguments", len(args))
		}
		email := args[0]
		if !isValidEmail(email) {
			return fmt.Errorf("invalid email format: %s", email)
		}
		return handleRefresh(email)
	case "version":
		fmt.Printf("github-copilot-svcs version %s\n", version)
		return nil
	case "help", "--help", "-h":
		PrintUsage()
		return nil
	default:
		logger.Error("Unknown command", "command", command)
		PrintUsage()
		return fmt.Errorf("unknown command: %s", command)
	}
}

func handleAuth(email string) error {
	cfg, err := LoadConfig(true)
	if err != nil {
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Create HTTP client with timeouts
	httpClient := CreateHTTPClient(cfg)
	authService := NewAuthService(httpClient)

	fmt.Println("Starting GitHub Copilot authentication...")
	// Use the provided email for authentication
	if err := authService.Authenticate(email, cfg); err != nil {
		return fmt.Errorf("authentication failed: %v", err)
	}

	fmt.Println("Authentication successful!")
	return nil
}

func handleStatusWithFormat(jsonOutput bool) error {
	cfg, err := LoadConfig(true)
	if err != nil {
		if errors.Is(err, ErrMissingTokens) {
			fmt.Println("Not authenticated. Run 'auth <email>' to authenticate.")
			return nil
		}
		return fmt.Errorf("failed to load config: %v", err)
	}

	if jsonOutput {
		return printStatusJSON(cfg)
	}
	return printStatusText(cfg)
}

func printStatusJSON(cfg *Config) error {
	path, _ := GetConfigPath()
	now := getCurrentTime()

	status := map[string]interface{}{
		"config_file":      path,
		"port":             cfg.Port,
		"authenticated":    cfg.CopilotToken != "",
		"has_github_token": cfg.GitHubToken != "",
		"refresh_interval": cfg.RefreshIn,
	}

	if cfg.CopilotToken != "" {
		timeUntilExpiry := cfg.ExpiresAt - now
		status["token_expires_at"] = cfg.ExpiresAt
		status["token_expires_in_seconds"] = timeUntilExpiry

		if timeUntilExpiry > 0 {
			refreshThreshold := cfg.RefreshIn / refreshPercentThreshold
			if refreshThreshold < defaultRefreshThreshold {
				refreshThreshold = defaultRefreshThreshold
			}

			if timeUntilExpiry <= refreshThreshold {
				status["status"] = "token_will_refresh_soon"
			} else {
				status["status"] = "healthy"
			}
		} else {
			status["status"] = "token_expired"
		}
	} else {
		status["status"] = "not_authenticated"
	}

	if err := json.NewEncoder(os.Stdout).Encode(status); err != nil {
		return fmt.Errorf("failed to encode status as JSON: %w", err)
	}
	return nil
}

func printStatusText(cfg *Config) error {
	path, _ := GetConfigPath()
	fmt.Printf("Configuration file: %s\n", path)
	fmt.Printf("Port: %d\n", cfg.Port)

	now := getCurrentTime()
	if cfg.CopilotToken != "" {
		fmt.Printf("Authentication: ✓ Authenticated\n")

		timeUntilExpiry := cfg.ExpiresAt - now
		if timeUntilExpiry > 0 {
			minutes := timeUntilExpiry / secondsInMinute
			seconds := timeUntilExpiry % secondsInMinute
			fmt.Printf("Token expires: in %dm %ds (%d seconds)\n", minutes, seconds, timeUntilExpiry)

			// Show refresh timing
			if cfg.RefreshIn > 0 {
				refreshThreshold := cfg.RefreshIn / refreshPercentThreshold // 20%
				if refreshThreshold < defaultRefreshThreshold {
					refreshThreshold = defaultRefreshThreshold // minimum 5 minutes
				}
				if timeUntilExpiry <= refreshThreshold {
					fmt.Printf("Status: ⚠️  Token will be refreshed soon (threshold: %d seconds)\n", refreshThreshold)
				} else {
					fmt.Printf("Status: ✅ Token is healthy\n")
				}
			}
		} else {
			fmt.Printf("Token expires: ⚠️  EXPIRED (%d seconds ago)\n", -timeUntilExpiry)
			fmt.Printf("Status: ❌ Token needs refresh\n")
		}

		fmt.Printf("Has GitHub token: %t\n", cfg.GitHubToken != "")
		if cfg.RefreshIn > 0 {
			fmt.Printf("Refresh interval: %d seconds\n", cfg.RefreshIn)
		}
	} else {
		fmt.Printf("Authentication: ✗ Not authenticated\n")
		fmt.Printf("Run '%s auth' to authenticate\n", os.Args[0])
	}

	return nil
}

func handleConfig() error {
	cfg, err := LoadConfig(true)
	if err != nil {
		if errors.Is(err, ErrMissingTokens) {
			fmt.Println("Not authenticated. Run 'auth <email>' to authenticate.")
			return nil
		}
		return fmt.Errorf("failed to load config: %v", err)
	}

	path, _ := GetConfigPath()
	fmt.Printf("Configuration file: %s\n", path)
	fmt.Printf("Port: %d\n", cfg.Port)
	fmt.Printf("Has GitHub token: %t\n", cfg.GitHubToken != "")
	fmt.Printf("Has Copilot token: %t\n", cfg.CopilotToken != "")
	if cfg.ExpiresAt > 0 {
		fmt.Printf("Token expires at: %d\n", cfg.ExpiresAt)
	}

	fmt.Printf("\nHTTP Headers:\n")
	fmt.Printf("  User-Agent: %s\n", cfg.Headers.UserAgent)
	fmt.Printf("  Editor-Version: %s\n", cfg.Headers.EditorVersion)
	fmt.Printf("  Editor-Plugin-Version: %s\n", cfg.Headers.EditorPluginVersion)
	fmt.Printf("  Copilot-Integration-Id: %s\n", cfg.Headers.CopilotIntegrationID)
	fmt.Printf("  Openai-Intent: %s\n", cfg.Headers.OpenaiIntent)
	fmt.Printf("  X-Initiator: %s\n", cfg.Headers.XInitiator)

	return nil
}

func getCurrentTime() int64 {
	return time.Now().Unix()
}

func handleRun() error {
	cfg, err := LoadConfig(true)
	if err != nil {
		if errors.Is(err, ErrMissingTokens) {
			fmt.Println("Not authenticated. Run 'auth <email>' to authenticate.")
			return fmt.Errorf("authentication required: %v", err)
		} else {
			return fmt.Errorf("failed to load config: %v", err)
		}
	}

	// Create HTTP client and start server
	httpClient := CreateHTTPClient(cfg)

	// 原有逻辑：在启动前确保有效的 token 存在
	// 现在的逻辑：不再需要在启动前确保有效的 token 存在，服务器会在运行时根据请求中的 email 自动刷新 token
	// authService := NewAuthService(httpClient)
	// if err := authService.EnsureValidToken(email); err != nil {
	//     return fmt.Errorf("authentication failed: %v", err)
	// }

	// Create and start server
	srv := NewServer(cfg, httpClient)
	return srv.Start()
}

func handleModels() error {
	cfg, err := LoadConfig(true)
	if err != nil {
		if errors.Is(err, ErrMissingTokens) {
			fmt.Println("Not authenticated. Run 'auth <email>' to authenticate.")
			return nil
		}
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Create HTTP client and auth service
	httpClient := CreateHTTPClient(cfg)

	// 原有逻辑：在获取模型列表前确保有效的 token 存在
	// 现在的逻辑：不再需要在启动前确保有效的 token 存在，但由于这是一个独立的命令操作，
	// 暂时保留原有逻辑（需要后续优化以支持 email 参数）
	// authService := NewAuthService(httpClient)
	// if authErr := authService.EnsureValidToken(email); authErr != nil {
	//     return fmt.Errorf("authentication failed: %v", authErr)
	// }

	// Fetch models
	modelList, err := FetchFromModelsDev(httpClient)
	if err != nil {
		fmt.Printf("Failed to fetch models from models.dev: %v\n", err)
		fmt.Println("Using default models:")
		defaultModels := GetDefault()
		for _, model := range defaultModels {
			fmt.Printf("  - %s (%s)\n", model.ID, model.OwnedBy)
		}
		return nil
	}

	filtered := modelList.Data
	var unknown []string
	filteredMsg := ""
	if len(cfg.AllowedModels) > 0 {
		allowedSet := make(map[string]struct{}, len(cfg.AllowedModels))
		for _, name := range cfg.AllowedModels {
			allowedSet[name] = struct{}{}
		}
		var tmp []transform.Model
		foundSet := make(map[string]struct{})
		for _, model := range filtered {
			if _, ok := allowedSet[model.ID]; ok {
				tmp = append(tmp, model)
				foundSet[model.ID] = struct{}{}
			}
		}
		for k := range allowedSet {
			if _, ok := foundSet[k]; !ok {
				unknown = append(unknown, k)
			}
		}
		filtered = tmp
		filteredMsg = "NOTE: The model list is filtered by allowed_models in config."
		if len(unknown) > 0 {
			fmt.Printf("WARNING: The following allowed_models were not found and are ignored: %v\n", unknown)
		}
	}
	fmt.Printf("Available models (%d shown):\n", len(filtered))
	for _, model := range filtered {
		fmt.Printf("  - %s (%s)\n", model.ID, model.OwnedBy)
	}
	if filteredMsg != "" {
		fmt.Println(filteredMsg)
	}
	return nil
}

func handleRefresh(email string) error {
	cfg, err := LoadConfig(true)
	if err != nil {
		if errors.Is(err, ErrMissingTokens) {
			fmt.Println("Not authenticated. Run 'auth <email>' to authenticate.")
			return nil
		}
		return fmt.Errorf("failed to load config: %v", err)
	}

	// Create HTTP client and auth service
	httpClient := CreateHTTPClient(cfg)
	authService := NewAuthService(httpClient)

	fmt.Println("Forcing token refresh...")
	// Use the provided email for token refresh
	if err := authService.RefreshToken(email, cfg); err != nil {
		return fmt.Errorf("token refresh failed: %v", err)
	}

	fmt.Printf("✅ Token refresh successful!\n")

	// Show new expiration time
	now := getCurrentTime()
	timeUntilExpiry := cfg.ExpiresAt - now
	minutes := timeUntilExpiry / secondsInMinute
	seconds := timeUntilExpiry % secondsInMinute
	fmt.Printf("New token expires in: %dm %ds\n", minutes, seconds)

	return nil
}
