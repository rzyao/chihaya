#!/bin/bash

# Chihaya Tracker 部署脚本
# 用法：以 root 身份运行此脚本

set -e

# 配置
PROJECT_DIR="/opt/chihaya"
CONTAINER_NAME="chihaya"
IMAGE_NAME="chihaya:latest"
CONFIG_VOLUME="/usr/local/etc/chihaya"

# 检查是否为 root 用户
if [ "$(id -u)" -ne 0 ]; then
    echo "错误：此脚本必须以 root 身份运行。"
    exit 1
fi

echo "开始部署..."

# 1. 进入项目目录
if [ ! -d "$PROJECT_DIR" ]; then
    echo "错误：项目目录 $PROJECT_DIR 不存在。"
    exit 1
fi
echo "进入 $PROJECT_DIR..."
cd "$PROJECT_DIR"

# 2. 更新代码库
echo "从 git 拉取最新代码..."
git fetch --all
git reset --hard origin/main
git pull origin main

# 3. 拉取依赖并打包（本地 Go - 可选）
# 如果宿主机安装了 Go，我们按要求本地拉取依赖并构建。
# 如果没有，我们依赖 Docker 构建来处理依赖。
if command -v go >/dev/null 2>&1; then
    echo "检测到宿主机上的 Go 环境。"
    echo "拉取依赖 (go mod tidy)..."
    go env -w GOPROXY=https://goproxy.io,direct
    go mod tidy
    
    echo "打包二进制文件 (go build)..."
    mkdir -p bin
    # 因为在 Linux 上，所以构建 Linux 版本
    GOOS=linux go build -o bin/chihaya ./cmd/chihaya
    echo "本地构建完成。"
else
    echo "宿主机未找到 Go。跳过本地依赖拉取和打包。"
    echo "依赖和构建将在 Docker 镜像内处理。"
fi

# 4. 检查并移除现有容器
if [ "$(docker ps -aq -f name=^/${CONTAINER_NAME}$)" ]; then
    echo "发现现有容器 '$CONTAINER_NAME'。正在停止并移除..."
    docker rm -f "$CONTAINER_NAME"
else
    echo "未发现名为 '$CONTAINER_NAME' 的现有容器。"
fi

# 5. 构建 Docker 镜像
# 这将更新 'chihaya:latest' 标签为新镜像。
echo "正在构建 Docker 镜像 '$IMAGE_NAME'..."
docker build -t "$IMAGE_NAME" .

# 可选：清理悬空的镜像层
echo "清理悬空镜像..."
docker image prune -f || true

# 6. 启动容器
echo "正在启动容器..."
# 确保主机上存在配置卷目录
if [ ! -d "$CONFIG_VOLUME" ]; then
    echo "创建配置目录：$CONFIG_VOLUME"
    mkdir -p "$CONFIG_VOLUME"
fi

# 根据部署文档自定义配置运行命令
# 使用 host 网络模式
docker run -d \
  --name "$CONTAINER_NAME" \
  --network=host \
  -v "$CONFIG_VOLUME":/dist \
  --restart=always \
  "$IMAGE_NAME"

# 输出绿色字体
echo -e "\033[0;32m部署成功完成。\033[0m"
echo -e "\033[0;32m容器 '$CONTAINER_NAME' 正在运行。\033[0m"
echo -e "\033[0;32m指标地址 http://localhost:6880/metrics\033[0m"
