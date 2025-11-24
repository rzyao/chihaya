# --- 第一阶段: 构建环境 (build-env) ---
# 使用官方的 golang:alpine 镜像作为构建环境。
# Alpine 是一个轻量级的 Linux 发行版，适合作为基础镜像。
FROM golang:alpine AS build-env

# 设置元数据标签，指明维护者信息。
LABEL maintainer "Jimmy Zelinskie <jimmyzelinskie+git@gmail.com>"

# 安装操作系统级别的依赖项。
# --no-cache: 不缓存包列表，减小镜像体积。
# curl, git: 构建过程中可能需要的工具，例如获取依赖。
RUN apk add --no-cache curl git

# 在容器内设置 Go 源码的工作目录。
WORKDIR /chihaya

# 将本地代码复制到容器的工作目录中。
COPY . /chihaya

# 安装 Go 依赖并编译 Go 二进制文件。
# CGO_ENABLED=0: 禁用 CGO，确保生成的二进制文件是静态链接的，不依赖系统 C 库。
# go install ./cmd/chihaya: 编译并安装 chihaya 程序到 $GOPATH/bin (即 /go/bin)。
RUN CGO_ENABLED=0 go install ./cmd/chihaya

# --- 第二阶段: 最终运行环境 ---
# 使用更小的 alpine:latest 镜像作为最终运行环境，这有助于减小最终镜像的大小。
FROM alpine:latest
# 安装 SSL 证书，这是 Go 程序在进行 HTTPS 请求时必需的。
RUN apk add --no-cache ca-certificates
# 从第一阶段 (build-env) 复制已编译好的 chihaya 二进制文件到根目录 /。
COPY --from=build-env /go/bin/chihaya /chihaya

# 创建一个名为 'chihaya' 的非特权用户。
# -D: 不创建家目录，适用于服务账号。
RUN adduser -D chihaya

# 暴露服务所需的网络端口。
# 6880: HTTP/HTTPS tracker 接口。
# 6969: UDP tracker 接口。
EXPOSE 6880 6969

# 降级到非 root 用户 (chihaya) 运行，提高安全性。
USER chihaya

# 设置容器启动时执行的命令，即运行 chihaya 程序。
ENTRYPOINT ["/chihaya"]
# 默认配置文件路径，可在运行时通过 -config 标志覆盖。
CMD ["-config", "/dist/config.yaml"]
