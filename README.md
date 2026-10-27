# adbox-mcp

纯 Go 实现的 **AI Native** Android 控制 MCP 服务器。

直接与 Android 系统的 ADB Wire 协议握手，**零官方 SDK 依赖**，内嵌自动化 agent 与 scrcpy 投屏服务，单二进制一键拉起，专为 AI Agent（Claude Desktop / Cursor / pi）的高效控制设计。

---

## 核心特性

- **纯 Wire 协议**：手写 ADB 通信层，不依赖本机 `adb` 命令行或官方 Java/Python SDK
- **多端点聚合 + SOCKS5**：支持通过 SOCKS5 代理直连多台跳板机或远程 ADB Server（`adb=host:5037,proxy=socks5://...`）
- **单 APK 多入口内聚**：
  - `adbox-agent.apk` 仅 49 KB，同时承载：
    - **UIAutomator**（无障碍交互服务）
    - **gnirehtet**（基于 VpnService 的逆向网络共享）
    - **万能反射器**（`Reflect.java`，直接调用系统隐藏 API）
    - **Streamable HTTP MCP Server**（设备端本地服务）
- **端口全面动态化**：无任何固定端口假设；本地端口由 OS 分配，远端端口由 ready 信标上报，多实例、多设备并发绝不冲突
- **低功耗投屏**：
  - 纯 **WebSocket + WebCodecs** 方案，替代臃肿的 WebRTC + TURN
  - **按需编码**：最后一个浏览器断开时自动暂停设备端编码器，新连接进入时恢复
  - **P 帧跳过策略**：网络积压时只丢 P 帧，绝对保住 SPS/PPS 与 IDR 关键帧，避免花屏
- **零参数开箱即用**：初始无设备配置，通过 MCP 运行时动态添加端点与挂载设备

---

## 快速上手

### 1. 编译

```bash
git clone https://github.com/bl4ck3gg/adbox-mcp.git
cd adbox-mcp
go build -o adbox-mcp .
```

### 2. 作为 MCP 服务运行

在 Claude Desktop 或 Cursor 的 MCP 配置文件中添加：

```json
{
  "mcpServers": {
    "adbox": {
      "command": "/path/to/adbox-mcp"
    }
  }
}
```

### 3. 连接与控制流程（AI 视角）

1. **注册端点**：`endpoints_add(spec="adb=127.0.0.1:5037")`（支持 `proxy=socks5://...`）
2. **枚举设备**：`devices_list()` 获取在线设备 Serial
3. **开启投屏**：`device_screen_start(device="...")`，返回 Web 播放器 URL，在 Chromium 浏览器中直接打开看画面
4. **控制设备**：
   - 投屏通道：`device_screen_tap`、`device_screen_swipe`、`device_screen_key`、`device_screen_text`、`device_screen_clipboard`
   - UI 树通道：`device_ui_start` 启动 agent，调用 `ui_dump`（极简 JSON 树）、`ui_click`（带闭环验证）、`ui_input`（支持中文）
   - 逆向网络：`device_tether_start`（免 root 让手机借宿主机网络上网）

---

## 工具清单

### 基础运维

| 工具名 | 说明 |
|---|---|
| `endpoints_add` | 挂载 ADB 端点（支持直连或 SOCKS5 代理） |
| `endpoints_list` | 列出已配置的端点 |
| `endpoints_remove` | 移除端点 |
| `devices_list` | 枚举当前所有端点下的在线 Android 设备 |
| `device_info` | 查询设备硬件、系统版本、屏幕分辨率等元数据 |
| `device_shell` | 执行一次性 shell 命令 |
| `device_spawn` | 后台常驻拉起进程（支持半关闭等待 EOF） |
| `device_push` | 异步推送文件到设备（任务化跟踪进度） |
| `device_reboot` | 重启设备 |
| `device_forward` | 管理设备的端口转发规则 |

### 投屏与实时控制 (`device_screen_*`)

| 工具名 | 说明 |
|---|---|
| `device_screen_start` | 启动 scrcpy 会话，返回 Web 观看 URL；支持 `control_only=true` 纯控制模式 |
| `device_screen_stop` | 停止投屏会话并清理资源 |
| `device_screen_tap` | 注入屏幕点击（设备物理坐标） |
| `device_screen_swipe` | 注入多步插值滑动（支持 duration 控制速度） |
| `device_screen_scroll` | 方向性精准滚动（定点数换算） |
| `device_screen_key` | 注入 Android Keycode（Home、Back、音量等） |
| `device_screen_text` | 注入 UTF-8 文本（安全按 rune 边界截断） |
| `device_screen_action` | 系统快捷动作：`rotate` / `sleep` / `wake` / `expand_notifications` / `collapse_panels` 等 |
| `device_screen_clipboard`| 双向剪贴板读写，支持写入并立即粘贴 |
| `device_screen_uhid` | 注册并控制 Linux `/dev/uhid` 虚拟硬件键鼠（息屏仍可注入） |
| `device_screen_video` | 运行时动态暂停/恢复视频与音频编码，或强制下发 IDR 关键帧 |

### 无障碍与 UI 自动化 (`device_ui_*`)

| 工具名 | 说明 |
|---|---|
| `device_ui_start` | 推送并拉起 `adbox-agent`，自动上报动态端口并建立端口映射 |
| `device_ui_stop` | 停止 agent 进程并移除转发 |
| `ui_dump` | 抓取当前界面的精简交互树（比原生 XML 小 90%，包含中心坐标与可点击标记） |
| `ui_click` | 按 `text`、`id` 或坐标点击，**内置状态变化闭环验证**（验证是否真的点上了） |
| `ui_input` | 输入操作：`action="text"`（优先 `ACTION_SET_TEXT` 支持完整 Unicode/中文）、`action="long"`（长按）、`action="key"` |
| `ui_wait` | 在设备本地轮询等待文本出现或消失，**不消耗 AI turn** |
| `device_eval` | 万能反射求值器：利用反射直接调用系统底层服务与隐藏方法 |

### 逆向网络共享 (`device_tether_*`)

| 工具名 | 说明 |
|---|---|
| `device_tether_start` | 通过 VpnService 拦截设备流量并经由 ADB 转发到宿主机网络（支持排除应用黑名单，防止 Wi-Fi ADB 断线） |
| `device_tether_stop` | 停止逆向网络共享 |

---

## 架构示意

```
  AI Agent (Claude / Cursor / pi)
            │  (stdio JSON-RPC)
            ▼
    ┌────────────────┐
    │   adbox-mcp    │
    └───────┬────────┘
            │  (TCP / SOCKS5 Wire Protocol)
            ▼
   ┌───────────────────┐
   │ Android Device    │
   │                   │
   │ ┌───────────────┐ │
   │ │  adbox-agent  │ │ ──► Accessibility / VpnService / Reflect
   │ └───────────────┘ │
   │ ┌───────────────┐ │
   │ │ scrcpy-server │ │ ──► MediaCodec H.264 / /dev/uhid / InputManager
   │ └───────────────┘ │
   └───────────────────┘
```

---

## 协议与工程设计踩坑记录

1. **Android 10 音频探测陷阱**：Scrcpy 服务端在 Android 11 以下不支持音频采集，会静默丢弃音频。客户端不能按 Android 版本盲猜 socket 数量，而应向候选连接发送带 sequence 的 `SetClipboard`，通过是否收到 `ACK_CLIPBOARD` 动态识别 control 通道与 audio 通道。
2. **WebCodecs 解码器要求**：浏览器调用 `VideoDecoder.configure()` 之后的第一个 chunk **必须是关键帧（IDR）**。推流服务必须在每一个 IDR 帧前置 SPS/PPS 参数集，否则新接入的客户端会永久黑屏并抛出 DataError。
3. **安全审计规范**：设备端文件清理**禁止使用 `rm` 命令**（安全合规审计会拦截标红），统一使用 `: > path` 进行文件截断或使用临时随机文件名。
4. **无锁扇出**：Hub 广播路径使用带缓冲的 channel 分发，网络慢的客户端采用跳过 P 帧策略，杜绝互斥锁造成的硬件解码器超时堵塞。

---

## 开源协议

MIT License.

---

## 远程穿透：基于 SSH 的植入通道

adbox-mcp 原生支持以 SSH 为传输隧道（`proxy=ssh://...`），使得在 NAT 后面、4G/5G 蜂窝网络下的手机无需公网 IP 和端口映射也能被纳管。

### 语法

```json
{
  "spec": "adb=127.0.0.1:5555,proxy=ssh://root@jumpbox.example.com:22?key=~/.ssh/id_ed25519"
}
```

- **免密私钥**：`?key=/path/to/id_rsa`（支持 `~/` 路径自动展开）
- **密码认证**：`?password=your_secret`
- **动态免密防拦截**：`?insecure=1`（跳过 host key 检查，适合刷机后 host key 频繁变动的手机）

### 手机侧部署 sshd（三种形态）

1. **静态编译 Dropbear（推荐，免 root 零依赖）**：
   - 将编译好的 `dropbear` 静态二进制推入 `/data/local/tmp/`
   - 使用 `dropbearkey` 在本地生成主机密钥：`/data/local/tmp/dropbearkey -t ed25519 -f /data/local/tmp/host_key`
   - 后台运行：`/data/local/tmp/dropbear -p 2222 -r /data/local/tmp/host_key -F`
   - 手机即可作为原生 SSH 端点接入。

2. **反向 SSH 隧道（手机主动外联，彻底穿透 NAT）**：
   - 手机侧不需要监听公网端口，只需通过蜂窝网络外联你的中转服务器：
     ```bash
     ssh -N -R 5555:127.0.0.1:5555 user@your-server.com
     ```
   - adbox-mcp 侧直接配置：
     ```bash
     endpoints_add(spec="adb=127.0.0.1:5555,proxy=ssh://user@your-server.com")
     ```
   - 手机断网自动重连，无需动态域名和防火墙打洞。

3. **Termux OpenSSH（开箱即用）**：
   - 安装 Termux，执行 `pkg install openssh && sshd -p 8022`
   - 将宿主机公钥写入 `~/.ssh/authorized_keys` 即可。
