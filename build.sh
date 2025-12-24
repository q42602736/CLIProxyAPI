#!/bin/bash

# 构建脚本 - 同时编译 macOS 和 Windows 版本

set -e

echo "🔨 开始编译..."

# 编译 macOS 版本
echo "📦 编译 macOS 版本..."
go build -o cliproxy ./cmd/server
echo "✅ macOS 版本编译完成: cliproxy"

# 编译 Windows amd64 版本
echo "📦 编译 Windows amd64 版本..."
GOOS=windows GOARCH=amd64 go build -o cliproxy-windows-amd64.exe ./cmd/server
echo "✅ Windows 版本编译完成: cliproxy-windows-amd64.exe"

echo ""
echo "🎉 所有版本编译完成！"
echo ""
ls -lliproxy-windows-amd64.exe
