#!/bin/bash
set -e

echo "Starting GitHub Copilot Services..."

# Warm-up: Pre-establish proxy connections to GitHub
echo "Warming up network connections..."

# 第一次请求（预热）
timeout 10 curl -s -o /dev/null https://github.com 2>&1 || echo "First warmup attempt (expected to be slow or timeout)"

# 第二次请求（验证）
timeout 5 curl -s -o /dev/null https://github.com 2>&1 && echo "Network warmup successful" || echo "Warning: Network warmup failed"

# 启动应用
echo "Starting application..."
exec ./github-copilot-svcs start
