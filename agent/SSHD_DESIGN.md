# 纯 Java 微型 SSHD 设计（Android, 无 root, app_process）

目标：在 Android 设备上跑一个 SSH 服务端，**零 native 二进制**，并且能主动反联到 SSH 壁垒。
来源：与用户 2026-10-27 的架构讨论（见 `daily/2026-10-27.md`）。

---

## 为什么是 Java

| | dropbear 静态二进制 | 纯 Java SSHD |
|---|---|---|
| 需要下发的文件 | 4 种 ABI 各一份（arm64/armv7/x86/x86_64），每份 ~1 MB | **0 份**，代码已在 APK 里 |
| root | 通常需要（chmod +x、绑定低端口） | **不需要** |
| ABI 兼容 | bionic / glibc / musl 差异 | **不存在**，跑在 ART VM 上 |
| 动态链接 | 需要静态编译 | 无 |
| 体积 | 4 MB（四架构） | 代码增加，APK 增量可控 |
| 更新 | 重新 push 二进制 | 重新 push APK 即可 |

现有 `adbox-agent.apk` 已经证明这套模式可行：49 KB，跑 `app_process`，无 root。

---

## 需要实现的 RFC 子集

只实现 SSH 传输层 + 认证 + 单路 session channel，不做 SFTP、不做端口转发（后续再加）。

### 传输层 (RFC 4253)

| 组件 | 必选算法（Android JCE 原生支持） |
|---|---|
| 密钥交换 | `ecdh-sha2-nistp256`（JCE `ECDH` + P-256）<br>`curve25519-sha256`（API 33+ 有，老版本用 ECDH） |
| 主机密钥 | `ecdsa-sha2-nistp256`（JCE `SHA256withECDSA`）<br>`ssh-ed25519`（BouncyCastle 备用） |
| 对称加密 | `aes128-ctr`（JCE `AES/CTR/NoPadding`）<br>`aes256-gcm@openssh.com`（JCE `AES/GCM/NoPadding`） |
| MAC | `hmac-sha2-256`（JCE `HmacSHA256`） |
| 压缩 | `none`（不做 zlib，省体积） |

**关键**：全部可以用 `javax.crypto` 和 `java.security` 实现，不需要 BouncyCastle（除非要 ed25519）。

### 认证 (RFC 4252)

只需要 `publickey`：
```
SSH_MSG_USERAUTH_REQUEST (50)
  user, "ssh-connection", "publickey", TRUE/FALSE, "ecdsa-sha2-nistp256", <blob>, <signature>
```
校验签名用 `java.security.Signature` 即可。

### 连接层 (RFC 4254)

只处理 `session` channel：
```
SSH_MSG_CHANNEL_OPEN (90)        type="session"
SSH_MSG_CHANNEL_REQUEST (98)     "pty-req" / "shell" / "exec" / "window-change"
SSH_MSG_CHANNEL_DATA (94)        双向字符流
SSH_MSG_CHANNEL_EOF/CLOSE (96/97)
```

`exec` 直接 `Runtime.exec()`；`shell` 起 `/system/bin/sh` 并用 pty（Android 无 pty 则退化到 pipe）。

---

## 体积估算

| 模块 | 预计体积 |
|---|---|
| 二进制包构建（编解码） | ~8 KB |
| 算法协商 + 密钥交换 | ~12 KB |
| 认证 | ~4 KB |
| Channel 管理 | ~6 KB |
| pty / exec 桥接 | ~5 KB |
| **合计** | **~35 KB**（dex 压缩后） |

APK 从 49 KB → ~85 KB。仍然远小于任何 native 方案的 4 MB。

---

## 反联（reverse）流程

```
1. 手机 SSHD 启动，监听 127.0.0.1:可变端口（不对外）
2. 手机同时起一个 TCP 客户端，主动连壁垒 tcp://bastion:PORT
3. 壁垒接受后，回一句 "ATTACH <token>"，手机用 token 注册自己
4. 壁垒把这台设备记入临时主机表，并通知 termcp
5. termcp 要连这台手机时：
   - 壁垒开一条新 channel，把 termcp 的 SSH 客户端字节流转发给手机
   - 手机侧的 SSHD 处理这次握手
```

**手机只需一条出站 TCP**，不需要监听任何外部可达端口。

---

## 与现有 ReverseClient 的关系

`ReverseClient.java` 已经实现了「主动外联 + 指数退避 + newline JSON-RPC」。
它解决的是 **MCP 控制面**；新的 SSHD 解决的是 **termcp 数据面**（真正的终端 + 端口转发）。

两者可以并存：
- `--connect tcp:HOST:PORT` → MCP 控制面（现有）
- `--bastion tcp:HOST:PORT` → SSH 反联（新增）

---

## 参考

- RFC 4253 (Transport Layer)
- RFC 4252 (Authentication)
- RFC 4254 (Connection Protocol)
- OpenSSH `PROTOCOL.chacha20poly1305`（如果将来要加 chacha）
