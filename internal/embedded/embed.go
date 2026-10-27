// Package embedded carries the device-side binaries adbox installs on demand.
//
// WHY SHIP THEM INSIDE THE BINARY. A UI driver is not useful on its own; it is
// useful when it is already on the device the moment someone asks for it. Making
// the operator fetch a separate artifact, match its version against the server's,
// and push it by hand turns a two-second operation into a deployment problem, and
// the failure mode -- a stale APK on the device running an old protocol -- is
// silent.
//
// Embedding costs about 47 KB and makes `device_ui_start` self-contained: the
// binary carries the device payload it needs, at exactly the version it was
// tested with.
//
// adbox-agent is MIT licensed; its sources live in this repository under agent/ and
// the matching release tag is recorded in AgentVersion below.
package embedded

import _ "embed"

//go:embed adbox-agent.apk
var AgentApk []byte

// AgentVersion identifies the embedded payload. Bump it whenever the APK changes so
// that a device carrying an older build is refreshed instead of trusted.
const AgentVersion = "0.1.0"

// AgentRemotePath is where the APK lands on the device. /data/local/tmp is the only
// writable location the shell user reliably has, and it survives adb reconnects.
const AgentRemotePath = "/data/local/tmp/adbox-agent.apk"

// AgentMainClass is the app_process entry point.
const AgentMainClass = "bl4ck3gg.adbox.agent.Main"

// scrcpy-server.jar is the screen-capture server, built separately from agent/
// and kept separate on purpose.
//
// WHY NOT ONE APK. The agent is 48 KB of MCP server and reflection; scrcpy is 67 KB
// of MediaCodec, OpenGL, and a control protocol, reached by a different entry point
// (a static main) and pulling in AIDL stubs the agent has no use for. Merging them
// meant the agent's every rebuild carried scrcpy's vendored code, and a scrcpy
// version bump meant re-validating the agent's UI path. Two payloads, two reasons
// to change.
//
//go:embed scrcpy-server.jar
var ScrcpyServer []byte

// ScrcpyVersion identifies the embedded scrcpy build.
const ScrcpyVersion = "2.4"

// ScrcpyRemotePath is where the jar lands. scrcpy's own convention, and the path
// its shell scripts assume.
const ScrcpyRemotePath = "/data/local/tmp/scrcpy-server.jar"

// ScrcpyMainClass is the app_process entry point scrcpy expects.
const ScrcpyMainClass = "mr.robot.agent.scrcpy.Server"
