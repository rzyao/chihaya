# Docker 部署指南

## 1. 环境准备

在部署 Chihaya Tracker 之前，您需要确保您的系统上安装了 Docker 和 Docker Compose。

### 1.1 安装 Docker

Docker 是一个开源的应用容器引擎，让开发者可以打包他们的应用以及依赖包到一个可移植的容器中。

*   **Windows/macOS**: 推荐安装 [Docker Desktop](https://www.docker.com/products/docker-desktop)，它包含了 Docker Engine、Docker CLI、Docker Compose 等所有必要的工具。
*   **Linux**: 请根据您的 Linux 发行版，参考 Docker 官方文档进行安装：
    *   [CentOS](https://docs.docker.com/engine/install/centos/)
    *   [Debian](https://docs.docker.com/engine/install/debian/)
    *   [Ubuntu](https://docs.docker.com/engine/install/ubuntu/)

安装完成后，您可以通过运行以下命令验证 Docker 是否正确安装：

```bash
docker --version
docker compose version
```

如果命令返回 Docker 和 Docker Compose 的版本信息，则表示安装成功。

## 2. 获取项目代码

您可以通过 Git 克隆 Chihaya Tracker 的项目仓库。如果您尚未安装 Git，请先安装它。

```bash
git clone https://github.com/chihaya/chihaya.git
cd chihaya
```

这将把 Chihaya 项目克隆到您的本地目录，并进入项目根目录。

## 3. 配置项目

Chihaya Tracker 的配置主要通过 `config.yaml` 文件进行管理。在项目根目录下，您会找到一个 `config.example.yaml` 文件，您可以将其复制并重命名为 `config.yaml`，然后根据您的需求进行修改。

```bash
cp config.example.yaml config.yaml
```

**重要配置项示例：**

*   **`port`**: Tracker 监听的端口，默认为 6881。
*   **`private`**: 是否启用私有 Tracker 模式。如果设置为 `true`，则只有注册的用户才能访问。
*   **`storage`**: 配置后端存储，例如 Redis 或 PostgreSQL。您需要根据您的选择配置相应的连接信息。

请根据您的实际部署环境和需求，仔细阅读 `config.yaml` 文件中的注释，并修改相应的配置项。

## 4. 构建 Docker 镜像

在项目根目录下，您会找到 `Dockerfile`，它定义了如何构建 Chihaya Tracker 的 Docker 镜像。执行以下命令来构建镜像：

```bash
docker build -t chihaya/chihaya .
```

*   `-t chihaya/chihaya`: 为您的镜像指定一个名称和标签。`chihaya/chihaya` 是镜像名称，`latest` 是默认标签（如果您不指定）。您可以根据需要更改它。
*   `.`: 表示 Dockerfile 位于当前目录。

构建过程可能需要一些时间，具体取决于您的网络速度和系统性能。

## 5. 运行 Docker 容器

在项目根目录下，我们已经创建了一个 `docker-compose.yml` 文件，它定义了 Chihaya Tracker 服务。使用 Docker Compose 可以方便地管理和运行多容器 Docker 应用。

执行以下命令来启动 Chihaya Tracker 服务：

```bash
docker compose up -d
```

*   `up`: 启动 `docker-compose.yml` 中定义的所有服务。
*   `-d`: 以“分离”模式（在后台）运行容器，这样您就可以关闭终端而服务仍然运行。

如果这是您第一次运行，Docker Compose 会拉取所需的镜像（如果本地不存在）并创建容器。您可以通过以下命令查看正在运行的容器：

```bash
docker compose ps
```

## 6. 验证部署

服务启动后，您可以通过以下方式验证 Chihaya Tracker 是否正常运行：

### 6.1 检查容器日志

```bash
docker compose logs chihaya
```

查看日志输出，确保没有错误信息，并且 Tracker 正在正常监听。

### 6.2 访问 Tracker 端口

如果您的 Tracker 配置为监听 HTTP 端口（例如 6881），您可以使用 `curl` 或浏览器访问它：

```bash
curl http://localhost:6881/announce
```

您应该会收到一个来自 Tracker 的响应，通常是 `d14:failure reason20:invalid info_hash` 或类似的错误信息，这表明 Tracker 正在运行并等待有效的请求。

## 7. 管理服务

您可以使用 `docker compose` 命令来管理 Chihaya Tracker 服务。

### 7.1 停止服务

要停止所有正在运行的服务，但保留容器和数据卷：

```bash
docker compose stop
```

### 7.2 启动已停止的服务

要启动已停止的服务：

```bash
docker compose start
```

### 7.3 重启服务

要重启服务：

```bash
docker compose restart
```

### 7.4 删除服务

要停止并删除所有容器、网络、卷和镜像（如果它们没有被其他服务使用）：

```bash
docker compose down
```

*   `-v`: 如果您想同时删除数据卷，请添加此选项。

## 8. 部署原理与优势

通过 Docker 部署 Chihaya Tracker 带来了多项优势，主要体现在以下几个方面：

### 8.1 部署原理

*   **环境隔离**: Docker 将应用程序及其所有依赖项打包到一个独立的容器中。这意味着 Chihaya Tracker 可以在任何安装了 Docker 的机器上运行，而无需担心操作系统版本、库依赖等环境差异。
*   **一致性**: 无论是在开发、测试还是生产环境，Docker 容器都提供了一致的运行环境，大大减少了“在我机器上可以运行”的问题。
*   **可移植性**: Docker 镜像包含了运行应用程序所需的一切，可以轻松地在不同环境之间迁移。
*   **资源利用率**: 容器比虚拟机更轻量级，启动更快，并且可以更有效地利用系统资源。

### 8.2 各步骤原因解释

*   **1. 环境准备**: 安装 Docker 和 Docker Compose 是使用容器化技术的基础。Docker 负责容器的创建和管理，而 Docker Compose 则用于定义和运行多容器 Docker 应用程序，简化了复杂应用的部署。
*   **2. 获取项目代码**: 这是所有部署的起点，确保您拥有最新或指定版本的 Chihaya Tracker 源代码。
*   **3. 配置项目**: 应用程序的配置决定了其行为。通过修改 `config.yaml`，您可以定制 Tracker 的端口、存储后端、私有模式等，以适应您的具体需求。
*   **4. 构建 Docker 镜像**: Docker 镜像是一个轻量级、独立、可执行的软件包，包含运行应用程序所需的一切（代码、运行时、系统工具、系统库等）。构建镜像的目的是将 Chihaya Tracker 应用程序及其依赖打包成一个可部署的单元。
*   **5. 运行 Docker 容器**: 使用 `docker compose up -d` 命令，Docker Compose 会根据 `docker-compose.yml` 文件中的定义，创建并启动 Chihaya Tracker 容器。`-d` 参数确保服务在后台运行，不会阻塞您的终端。
*   **6. 验证部署**: 部署完成后，验证服务是否正常运行至关重要。检查日志可以发现潜在的启动错误，而访问 Tracker 端口则能确认服务是否可达并响应请求。
*   **7. 管理服务**: Docker Compose 提供了一套简单命令来管理容器的生命周期，如停止、启动、重启和删除，这使得维护和更新服务变得非常方便。

通过遵循这些步骤，您可以高效、可靠地部署和管理 Chihaya Tracker。