#!/bin/bash
set -e

echo "Starting GitHub Copilot Services..."

# Warm-up: Pre-establish proxy connections to GitHub
echo "Warming up network connections..."

# 第一次请求（预热 DNS 解析和代理握手）
timeout 10 curl -s -o /dev/null --connect-timeout 5 https://github.com/login/device/code 2>&1 || echo "First warmup attempt (expected to be slow or timeout)"

# 第二次请求（验证网络路径已建立）
timeout 5 curl -s -o /dev/null --connect-timeout 3 https://github.com/login/device/code 2>&1 && echo "Network warmup successful" || echo "Warning: Network warmup failed"

# 预热其他关键端点
timeout 5 curl -s -o /dev/null --connect-timeout 3 https://api.github.com 2>&1 || echo "GitHub API warmup attempted"
timeout 5 curl -s -o /dev/null --connect-timeout 3 https://api.githubcopilot.com 2>&1 || echo "GitHub Copilot API warmup attempted"

# 启动应用
echo "Starting application..."
exec ./github-copilot-svcs start
