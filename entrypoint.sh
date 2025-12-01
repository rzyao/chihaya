#!/bin/sh
set -e

# 如果 /dist/config.yaml 不存在，则从 /defaults/config.yaml 复制
if [ ! -f "/dist/config.yaml" ]; then
    echo "Config file not found in /dist, copying from defaults..."
    cp /defaults/config.yaml /dist/config.yaml
    echo "Config file created."
fi

# 确保 chihaya 用户对 /dist 目录有读写权限
# 这一步很重要，因为挂载的卷可能属于 root
chown -R chihaya:chihaya /dist

# 使用 su-exec 降权运行 chihaya
# "$@" 包含了 CMD 传递的参数
exec su-exec chihaya "$@"
