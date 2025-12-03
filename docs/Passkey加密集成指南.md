# Passkey 加密集成指南

本文档旨在指导后端开发人员如何生成兼容 Chihaya Tracker 的加密 Passkey。

## 1. 概述

为了提高安全性，Tracker 支持（并可强制要求）使用加密的 Passkey。
后端站点在生成 Announce URL 时，需将用户的 Passkey 和当前时间戳打包并加密，生成的密文作为 URL 中的 `credential` 参数。

## 2. 加密规范

-   **算法**: AES-256-GCM (Galois/Counter Mode)
-   **密钥长度**: 32 字节 (256 位)
-   **Nonce 长度**: 12 字节 (96 位)，随机生成
-   **编码方式**: Base64 URL Safe (RFC 4648)

### 2.1 数据载荷 (Payload)

加密前的明文数据为一个 JSON 对象：

```json
{
  "pk": "用户Passkey (字符串)",
  "ts": 生成时间戳 (Unix秒, 整数),
  "fd": "是否免费下载 (可选, 任意类型)",
  "pd": "是否部分下载 (可选, 任意类型)"
}
```

**示例**:
```json
{
  "pk": "abcdef0123456789abcdef0123456789",
  "ts": 1701417600
}
```

### 2.2 加密流程

1.  **构造 Payload**: 创建包含 `pk` 和 `ts` 的 JSON 字符串。
2.  **生成 Nonce**: 生成 12 字节的随机数据。
3.  **AES-GCM 加密**: 使用预共享的 32 字节密钥和生成的 Nonce 对 JSON 字符串进行加密。
    -   输入: `Key`, `Nonce`, `Plaintext` (JSON)
    -   输出: `Ciphertext` (包含 Auth Tag)
4.  **拼接**: 将 `Nonce` 拼接到 `Ciphertext` 头部。
    -   `FinalBytes = Nonce + Ciphertext`
5.  **编码**: 对 `FinalBytes` 进行 Base64 URL Safe 编码。
    -   结果即为最终的 `credential` 参数值。

## 3. 客户端 URL 示例

假设生成的加密字符串（Base64 URL Safe）为：
`A1B2C3D4E5F6...` (通常长度约为 80-100 字符)

客户端使用的 Announce URL 格式取决于 Tracker 的路由配置，通常有两种形式：

### 形式 A：查询参数 (推荐)
使用 `credential` 参数传递加密后的值：
```
https://tracker.example.com/announce?credential=A1B2C3D4E5F6...
```

### 形式 B：路径参数
如果您的 Tracker 配置为从路径中提取参数（例如 `/announce/:credential`）：
```
https://tracker.example.com/A1B2C3D4E5F6.../announce
```

**注意**：
-   客户端（如 uTorrent, qBittorrent）**不需要**知道这是加密的，它们只需将其视为一个普通的 URL 参数。
-   站点在生成种子文件（.torrent）时，将上述 URL 写入 `announce` 字段即可。

## 4. 代码示例

### 4.1 PHP 示例

```php
<?php

function encryptPasskey($passkey, $secretKey) {
    // 1. 构造 Payload
    $payload = json_encode([
        'pk' => $passkey,
        'ts' => time()
    ]);

    // 2. 生成 Nonce (12 bytes)
    $nonce = random_bytes(12);

    // 3. AES-256-GCM 加密
    // PHP 的 openssl_encrypt 输出不包含 Tag，需要手动处理
    // 注意：PHP 7.1+ 支持 aes-256-gcm
    $tag = "";
    $ciphertext = openssl_encrypt(
        $payload,
        'aes-256-gcm',
        $secretKey,
        OPENSSL_RAW_DATA,
        $nonce,
        $tag
    );

    // 4. 拼接: Nonce + Ciphertext + Tag
    // 注意：Go 的 GCM Seal 方法通常将 Tag 附加在 Ciphertext 末尾
    // 但 PHP openssl_encrypt 是分开返回的，所以我们需要手动拼接
    $finalBytes = $nonce . $ciphertext . $tag;

    // 5. Base64 URL Safe 编码
    return rtrim(strtr(base64_encode($finalBytes), '+/', '-_'), '=');
}

// 配置
$secretKey = "01234567890123456789012345678901"; // 必须 32 字节
$userPasskey = "mysecretpasskey";

$encrypted = encryptPasskey($userPasskey, $secretKey);
echo "Encrypted Credential: " . $encrypted . "\n";
echo "Announce URL: https://tracker.example.com/announce?credential=" . $encrypted . "\n";
?>
```

### 4.2 Python 示例

```python
import json
import time
import os
import base64
from cryptography.hazmat.primitives.ciphers.aead import AESGCM

def encrypt_passkey(passkey, secret_key):
    # Ensure key is bytes
    if isinstance(secret_key, str):
        secret_key = secret_key.encode('utf-8')
    
    # 1. Construct Payload
    payload = json.dumps({
        "pk": passkey,
        "ts": int(time.time())
    }).encode('utf-8')

    # 2. Generate Nonce
    nonce = os.urandom(12)

    # 3. AES-256-GCM Encrypt
    aesgcm = AESGCM(secret_key)
    ciphertext = aesgcm.encrypt(nonce, payload, None) 
    # Note: cryptography's encrypt method returns Ciphertext + Tag

    # 4. Concatenate: Nonce + (Ciphertext + Tag)
    final_bytes = nonce + ciphertext

    # 5. Base64 URL Safe Encode
    return base64.urlsafe_b64encode(final_bytes).decode('utf-8').rstrip('=')

# Configuration
secret_key = b"01234567890123456789012345678901" # Must be 32 bytes
user_passkey = "mysecretpasskey"

encrypted = encrypt_passkey(user_passkey, secret_key)
print(f"Encrypted Credential: {encrypted}")
```

### 4.3 Go 示例

```go
package main

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"time"
)

type Payload struct {
	Passkey   string `json:"pk"`
	Timestamp int64  `json:"ts"`
}

func EncryptPasskey(passkey string, key string) (string, error) {
	// 1. Construct Payload
	payload := Payload{
		Passkey:   passkey,
		Timestamp: time.Now().Unix(),
	}
	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	// 2. AES-GCM Setup
	block, err := aes.NewCipher([]byte(key))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}

	// 3. Generate Nonce
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}

	// 4. Encrypt and Concatenate (Seal appends ciphertext+tag to nonce)
	ciphertext := gcm.Seal(nonce, nonce, jsonBytes, nil)

	// 5. Base64 URL Safe Encode
	return base64.URLEncoding.EncodeToString(ciphertext), nil
}

func main() {
	key := "01234567890123456789012345678901" // 32 bytes
	passkey := "mysecretpasskey"

	encrypted, err := EncryptPasskey(passkey, key)
	if err != nil {
		panic(err)
	}
	fmt.Printf("Encrypted Credential: %s\n", encrypted)
}
```

## 5. 常见问题

**Q: 为什么解密失败？**
A: 请检查以下几点：
1.  密钥是否完全一致（包括空格等）。
2.  密钥长度是否严格为 32 字节。
3.  Base64 编码是否使用了 URL Safe 模式（`+` 替换为 `-`，`/` 替换为 `_`）。
4.  Nonce 是否正确提取（前 12 字节）。

**Q: URL 是否会过期？**
A: 目前 Tracker 仅记录时间戳，暂未强制校验过期时间，但建议后端保留此能力以便未来升级安全策略。
