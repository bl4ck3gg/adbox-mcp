package adb

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"adbox-mcp/internal/adb/internal/errors"
	"adbox-mcp/internal/adb/wire"
)

// MtimeOfClose should be passed to OpenWrite to set the file modification time to the time the Close
// method is called.
var MtimeOfClose = time.Time{}

// forwardMu 串行化所有 ForwardToFreePort，避免多设备并发 list-forward/Forward 时 adb 响应串线导致端口混用。
var forwardMu sync.Mutex

// Device communicates with a specific Android device.
// To get an instance, call Device() on an Adb.
type Device struct {
	server     server
	descriptor DeviceDescriptor

	// Used to get device info.
	deviceListFunc func() ([]*DeviceInfo, error)
}

func (c *Device) String() string {
	return c.descriptor.String()
}

// get-product is documented, but not implemented, in the server.
// TODO(z): Make product exported if get-product is ever implemented in adb.
func (c *Device) product() (string, error) {
	attr, err := c.getAttribute("get-product")
	return attr, wrapClientError(err, c, "Product")
}

func (c *Device) Serial() (string, error) {
	attr, err := c.getAttribute("get-serialno")
	return attr, wrapClientError(err, c, "Serial")
}

func (c *Device) DevicePath() (string, error) {
	attr, err := c.getAttribute("get-devpath")
	return attr, wrapClientError(err, c, "DevicePath")
}

func (c *Device) State() (DeviceState, error) {
	attr, err := c.getAttribute("get-state")
	if err != nil {
		return StateInvalid, wrapClientError(err, c, "State")
	}
	state, err := parseDeviceState(attr)
	return state, wrapClientError(err, c, "State")
}

var (
	FProtocolTcp        = "tcp"
	FProtocolAbstract   = "localabstract"
	FProtocolReserved   = "localreserved"
	FProtocolFilesystem = "localfilesystem"
)

type ForwardSpec struct {
	Protocol   string
	PortOrName string
}

func (f ForwardSpec) String() string {
	return fmt.Sprintf("%s:%s", f.Protocol, f.PortOrName)
}

func (f ForwardSpec) Port() (int, error) {
	if f.Protocol != FProtocolTcp {
		return 0, fmt.Errorf("protocal is not tcp")
	}
	return strconv.Atoi(f.PortOrName)
}

func (f *ForwardSpec) parseString(s string) error {
	fields := strings.Split(s, ":")
	if len(fields) != 2 {
		return fmt.Errorf("expect string contains only one ':', str = %s", s)
	}
	f.Protocol = fields[0]
	f.PortOrName = fields[1]
	return nil
}

type ForwardPair struct {
	Serial string
	Local  ForwardSpec
	Remote ForwardSpec
}

// ForwardList returns list with struct ForwardPair
// If no device serial specified all devices's forward list will returned
func (c *Device) ForwardList() (fs []ForwardPair, err error) {
	attr, err := c.getAttribute("list-forward")
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(attr)
	if len(fields)%3 != 0 {
		return nil, fmt.Errorf("list forward parse error")
	}
	// 使用 transport-id 时按本机 serial 过滤，避免多设备时 list 含其他设备导致拿到错 port、Dial 错连、读 dummy EOF
	var mySerial string
	if c.descriptor.descriptorType == DeviceTransportID {
		mySerial, _ = c.Serial()
	}
	fs = make([]ForwardPair, 0)
	for i := 0; i < len(fields)/3; i++ {
		var local, remote ForwardSpec
		var serial = fields[i*3]
		if c.descriptor.descriptorType == DeviceSerial && c.descriptor.serial != serial {
			continue
		}
		// transport-id 时只保留本机 forward，避免 findForwardPort 拿到其他设备端口
		if c.descriptor.descriptorType == DeviceTransportID && mySerial != "" && serial != mySerial {
			continue
		}
		if err = local.parseString(fields[i*3+1]); err != nil {
			return nil, err
		}
		if err = remote.parseString(fields[i*3+2]); err != nil {
			return nil, err
		}
		fs = append(fs, ForwardPair{serial, local, remote})
	}
	return fs, nil
}

// ForwardRemove specified forward
func (c *Device) ForwardRemove(local ForwardSpec) error {
	err := roundTripSingleNoResponse(c.server,
		fmt.Sprintf("%s:killforward:%v", c.descriptor.getHostPrefix(), local))
	return wrapClientError(err, c, "ForwardRemove")
}

// ForwardRemoveAll cancel all exists forwards
func (c *Device) ForwardRemoveAll() error {
	err := roundTripSingleNoResponse(c.server,
		fmt.Sprintf("%s:killforward-all", c.descriptor.getHostPrefix()))
	return wrapClientError(err, c, "ForwardRemoveAll")
}

// Reverse forwards device connections to host (reverse of Forward).
// Uses same connection flow as official adb: host:transport:<serial> then reverse:forward:<device>;<host>.
// deviceSpec: socket on device (e.g. localabstract:scrcpy)
// hostSpec: socket on host (e.g. tcp:8886)
func (c *Device) Reverse(deviceSpec, hostSpec ForwardSpec) error {
	transportReq := "host:" + c.descriptor.getTransportDescriptor()
	payloadReq := fmt.Sprintf("reverse:forward:%v;%v", deviceSpec, hostSpec)
	err := roundTripTransportThenNoResponse(c.server, transportReq, payloadReq)
	return wrapClientError(err, c, "Reverse")
}

// ReverseRemove removes a specific reverse forward.
// spec is the device-side address (e.g. localabstract:scrcpy). Protocol: reverse:killforward:<device-address>
func (c *Device) ReverseRemove(deviceSpec ForwardSpec) error {
	transportReq := "host:" + c.descriptor.getTransportDescriptor()
	payloadReq := fmt.Sprintf("reverse:killforward:%v", deviceSpec)
	err := roundTripTransportThenNoResponse(c.server, transportReq, payloadReq)
	return wrapClientError(err, c, "ReverseRemove")
}

// ReverseRemoveAll removes all reverse forwards
func (c *Device) ReverseRemoveAll() error {
	transportReq := "host:" + c.descriptor.getTransportDescriptor()
	err := roundTripTransportThenNoResponse(c.server, transportReq, "reverse:killforward-all")
	return wrapClientError(err, c, "ReverseRemoveAll")
}

// Forward remote connection to local
func (c *Device) Forward(local, remote ForwardSpec) error {
	err := roundTripSingleNoResponse(c.server,
		fmt.Sprintf("%s:forward:%v;%v", c.descriptor.getHostPrefix(), local, remote))
	return wrapClientError(err, c, "Forward")
}

// findForwardPort 查找指定 remote 的 forward 端口，不存在返回 0, false
func (c *Device) findForwardPort(remote ForwardSpec) (port int, found bool, err error) {
	fws, err := c.ForwardList()
	if err != nil {
		return 0, false, err
	}
	for _, fw := range fws {
		if fw.Remote == remote {
			port, err := fw.Local.Port()
			return port, true, err
		}
	}
	return 0, false, nil
}

// ForwardToFreePort 为指定 remote 建立新 forward，返回主机端口。
// 若本设备上已有同 remote 的 forward 会先移除再新建，避免多设备时复用错端口或卡住。
// 内部加锁串行化，避免多设备并发时 adb list-forward/Forward 响应串线。
func (c *Device) ForwardToFreePort(remote ForwardSpec) (port int, err error) {
	forwardMu.Lock()
	defer forwardMu.Unlock()
	fws, err := c.ForwardList()
	if err != nil {
		return 0, err
	}
	for _, fw := range fws {
		if fw.Remote == remote {
			_ = c.ForwardRemove(fw.Local)
		}
	}
	// 使用 tcp:0 让 adb server 自动分配端口（避免端口冲突，且端口在 adb server 所在机器上）
	if err = c.Forward(ForwardSpec{FProtocolTcp, "0"}, remote); err != nil {
		return 0, err
	}
	// Forward 后重新查询获取实际分配的端口
	port, found, err := c.findForwardPort(remote)
	if err != nil {
		return 0, err
	}
	if !found {
		return 0, fmt.Errorf("forward created but port not found in list")
	}
	return port, nil
}

func (c *Device) DeviceInfo() (*DeviceInfo, error) {
	// Adb doesn't actually provide a way to get this for an individual device,
	// so we have to just list devices and find ourselves.

	serial, err := c.Serial()
	if err != nil {
		return nil, wrapClientError(err, c, "GetDeviceInfo(GetSerial)")
	}

	devices, err := c.deviceListFunc()
	if err != nil {
		return nil, wrapClientError(err, c, "DeviceInfo(ListDevices)")
	}

	for _, deviceInfo := range devices {
		if deviceInfo.Serial == serial {
			return deviceInfo, nil
		}
	}

	err = errors.Errorf(errors.DeviceNotFound, "device list doesn't contain serial %s", serial)
	return nil, wrapClientError(err, c, "DeviceInfo")
}

/*
RunCommand runs the specified commands on a shell on the device.

From the Android docs:

	Run 'command arg1 arg2 ...' in a shell on the device, and return
	its output and error streams. Note that arguments must be separated
	by spaces. If an argument contains a space, it must be quoted with
	double-quotes. Arguments cannot contain double quotes or things
	will go very wrong.

	Note that this is the non-interactive version of "adb shell"

Source: https://android.googlesource.com/platform/system/core/+/master/adb/SERVICES.TXT

This method quotes the arguments for you, and will return an error if any of them
contain double quotes.

Because the adb shell converts all "\n" into "\r\n",
so here we convert it back (maybe not good for binary output)
*/
func (c *Device) RunCommand(cmd string, args ...string) (string, error) {
	conn, err := c.OpenCommand(cmd, args...)
	if err != nil {
		return "", err
	}

	defer conn.Close()
	resp, err := conn.ReadUntilEof()
	if err != nil {
		adbDebugf("[adb] << shell read err: %v", err)
		return "", wrapClientError(err, c, "RunCommand")
	}
	adbDebugf("[adb] << shell %d bytes", len(resp))
	outStr := strings.Replace(string(resp), "\r\n", "\n", -1)
	return outStr, nil
}

// OpenInteractiveShell 打开 adb 协议 shell:（等价无参 adb shell）。部分 adbd 会分配 PTY。
func (c *Device) OpenInteractiveShell() (conn *wire.Conn, err error) {
	conn, err = c.dialDevice()
	if err != nil {
		return nil, wrapClientError(err, c, "OpenInteractiveShell")
	}
	defer func() {
		if err != nil && conn != nil {
			conn.Close()
		}
	}()
	req := "shell:"
	if err = conn.SendMessage([]byte(req)); err != nil {
		return nil, wrapClientError(err, c, "OpenInteractiveShell")
	}
	if _, err = conn.ReadStatus(req); err != nil {
		return nil, wrapClientError(err, c, "OpenInteractiveShell")
	}
	return conn, nil
}

// RunInteractiveShellLinesFireAndClose 顺序写入各行后 CloseWrite 并断开，不 ReadUntilEof。
// 用于 nohup/daemon bootstrap：不依赖设备 shell 执行 exit 后关连接（后台 job 会导致永不 EOF）。
const drainTimeout = 3 * time.Second

func (c *Device) RunInteractiveShellLinesFireAndClose(lines []string) error {
	conn, err := c.OpenInteractiveShell()
	if err != nil {
		return wrapClientError(err, c, "RunInteractiveShellLinesFireAndClose(OpenInteractiveShell)")
	}
	defer conn.Close()
	for _, line := range lines {
		if _, err := io.WriteString(conn, line+"\n"); err != nil {
			return wrapClientError(err, c, "RunInteractiveShellLinesFireAndClose(WriteLine)")
		}
	}
	if err := conn.CloseWrite(); err != nil {
		return wrapClientError(err, c, "RunInteractiveShellLinesFireAndClose(CloseWrite)")
	}
	// Drain until the device shell exits, instead of closing on top of it.
	//
	// Half-closing tells the shell "no more input"; closing the socket straight
	// away tells adbd "the session is gone", and adbd reacts by killing the
	// session's process group -- taking the daemon we just launched with it. The
	// shell needs a moment to read EOF, exit normally, and let the child be
	// reparented to init. Reading until EOF is that moment, and it costs the time
	// the shell needs anyway.
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		_, _ = io.Copy(io.Discard, conn)
	}()
	select {
	case <-drained:
	case <-time.After(drainTimeout):
	}
	if adbDebugEnabled() {
		adbDebugf("[adb] RunInteractiveShellLinesFireAndClose device=%s: 已写入 %d 行并 CloseWrite，主机侧关闭会话", c.String(), len(lines))
	}
	return nil
}

// RunInteractiveShellLines 在 shell: 会话中按顺序发送若干行（每行一条命令），最后发送 exit 并读尽输出。
// 需要读取 shell 汇总输出时用；daemon bootstrap 请用 RunInteractiveShellLinesFireAndClose。
// Web 里远程终端走 agent（mr.shell.interactive），与本函数无关。
func (c *Device) RunInteractiveShellLines(lines []string) (string, error) {
	if adbDebugEnabled() {
		adbDebugf("[adb] RunInteractiveShellLines device=%s line_count=%d", c.String(), len(lines))
		for i, line := range lines {
			adbDebugf("[adb]   shell:%d %s", i+1, line)
		}
		adbDebugf("[adb]   shell:%d exit", len(lines)+1)
	}
	conn, err := c.OpenInteractiveShell()
	if err != nil {
		return "", wrapClientError(err, c, "RunInteractiveShellLines(OpenInteractiveShell)")
	}
	defer conn.Close()
	if adbDebugEnabled() {
		adbDebugf("[adb] RunInteractiveShellLines device=%s: shell: 已打开，开始写入 %d 行", c.String(), len(lines))
	}
	for i, line := range lines {
		if _, err := io.WriteString(conn, line+"\n"); err != nil {
			return "", wrapClientError(err, c, "RunInteractiveShellLines(WriteLine)")
		}
		if adbDebugEnabled() {
			adbDebugf("[adb] RunInteractiveShellLines device=%s: 已写入 shell:%d", c.String(), i+1)
		}
	}
	if _, err := io.WriteString(conn, "exit\n"); err != nil {
		return "", wrapClientError(err, c, "RunInteractiveShellLines(WriteExit)")
	}
	if adbDebugEnabled() {
		adbDebugf("[adb] RunInteractiveShellLines device=%s: 已写入 exit，等待 shell 关闭(ReadUntilEof)…", c.String())
	}
	resp, err := conn.ReadUntilEof()
	if err != nil {
		return "", wrapClientError(err, c, "RunInteractiveShellLines(ReadUntilEof)")
	}
	if adbDebugEnabled() {
		adbDebugf("[adb] RunInteractiveShellLines device=%s: ReadUntilEof 完成，输出 %d 字节", c.String(), len(resp))
	}
	return strings.Replace(string(resp), "\r\n", "\n", -1), nil
}

func (c *Device) OpenCommand(cmd string, args ...string) (conn *wire.Conn, err error) {
	cmd, err = prepareCommandLine(cmd, args...)
	if err != nil {
		return nil, wrapClientError(err, c, "RunCommand")
	}
	conn, err = c.dialDevice()
	if err != nil {
		return nil, wrapClientError(err, c, "RunCommand")
	}
	defer func() {
		if err != nil && conn != nil {
			conn.Close()
		}
	}()

	req := fmt.Sprintf("shell:%s", cmd)
	adbDebugf("[adb] >> %s", req)

	// Shell responses are special, they don't include a length header.
	// We read until the stream is closed.
	// So, we can't use conn.RoundTripSingleResponse.
	if err = conn.SendMessage([]byte(req)); err != nil {
		adbDebugf("[adb] << shell err: %v", err)
		return nil, wrapClientError(err, c, "Command")
	}
	if _, err = conn.ReadStatus(req); err != nil {
		adbDebugf("[adb] << shell status err: %v", err)
		return nil, wrapClientError(err, c, "Command")
	}
	adbDebugf("[adb] << shell ok")
	return conn, nil
}

func (c *Device) RunCommandInContext(conn *wire.Conn, cmd string, args ...string) (*wire.Conn, string, error) {
	conn, err := c.OpenCmdInContext(conn, cmd, args...)
	if err != nil {
		return nil, "", err
	}

	resp, err := conn.ReadUntilEof()
	if err != nil {
		return nil, "", wrapClientError(err, c, "RunCommand")
	}
	outStr := strings.Replace(string(resp), "\r\n", "\n", -1)
	return conn, outStr, nil
}

func (c *Device) OpenCmdInContext(conn *wire.Conn, cmd string, args ...string) (*wire.Conn, error) {
	cmd, err := prepareCommandLine(cmd, args...)
	if err != nil {
		return nil, wrapClientError(err, c, "RunCommand")
	}
	if conn == nil {
		conn, err = c.dialDevice()
	}
	if err != nil {
		return nil, wrapClientError(err, c, "RunCommand")
	}

	req := fmt.Sprintf("shell:%s", cmd)
	adbDebugf("[adb] >> %s", req)

	// Shell responses are special, they don't include a length header.
	// We read until the stream is closed.
	// So, we can't use conn.RoundTripSingleResponse.
	if err = conn.SendMessage([]byte(req)); err != nil {
		adbDebugf("[adb] << shell err: %v", err)
		return nil, wrapClientError(err, c, "Command")
	}
	if _, err = conn.ReadStatus(req); err != nil {
		adbDebugf("[adb] << shell status err: %v", err)
		return nil, wrapClientError(err, c, "Command")
	}
	adbDebugf("[adb] << shell ok")

	return conn, nil
}

/*
Remount, from the official adb command’s docs:

	Ask adbd to remount the device's filesystem in read-write mode,
	instead of read-only. This is usually necessary before performing
	an "adb sync" or "adb push" request.
	This request may not succeed on certain builds which do not allow
	that.

Source: https://android.googlesource.com/platform/system/core/+/master/adb/SERVICES.TXT
*/
func (c *Device) Remount() (string, error) {
	conn, err := c.dialDevice()
	if err != nil {
		return "", wrapClientError(err, c, "Remount")
	}
	defer conn.Close()

	resp, err := conn.RoundTripSingleResponse([]byte("remount"))
	return string(resp), wrapClientError(err, c, "Remount")
}

func (c *Device) ListDirEntries(path string) (*DirEntries, error) {
	conn, err := c.getSyncConn()
	if err != nil {
		return nil, wrapClientError(err, c, "ListDirEntries(%s)", path)
	}

	entries, err := listDirEntries(conn, path)
	return entries, wrapClientError(err, c, "ListDirEntries(%s)", path)
}

func (c *Device) Stat(path string) (*DirEntry, error) {
	conn, err := c.getSyncConn()
	if err != nil {
		return nil, wrapClientError(err, c, "Stat(%s)", path)
	}
	defer conn.Close()

	entry, err := stat(conn, path)
	return entry, wrapClientError(err, c, "Stat(%s)", path)
}

func (c *Device) OpenRead(path string) (io.ReadCloser, error) {
	conn, err := c.getSyncConn()
	if err != nil {
		return nil, wrapClientError(err, c, "OpenRead(%s)", path)
	}

	reader, err := receiveFile(conn, path)
	return reader, wrapClientError(err, c, "OpenRead(%s)", path)
}

// OpenWrite opens the file at path on the device, creating it with the permissions specified
// by perms if necessary, and returns a writer that writes to the file.
// The files modification time will be set to mtime when the WriterCloser is closed. The zero value
// is TimeOfClose, which will use the time the Close method is called as the modification time.
func (c *Device) OpenWrite(path string, perms os.FileMode, mtime time.Time) (io.WriteCloser, error) {
	conn, err := c.getSyncConn()
	if err != nil {
		return nil, wrapClientError(err, c, "OpenWrite(%s)", path)
	}

	writer, err := sendFile(conn, path, perms, mtime)
	return writer, wrapClientError(err, c, "OpenWrite(%s)", path)
}

// getAttribute returns the first message returned by the server by running
// <host-prefix>:<attr>, where host-prefix is determined from the DeviceDescriptor.
func (c *Device) getAttribute(attr string) (string, error) {
	resp, err := roundTripSingleResponse(c.server,
		fmt.Sprintf("%s:%s", c.descriptor.getHostPrefix(), attr))
	if err != nil {
		return "", err
	}
	return string(resp), nil
}

func (c *Device) getSyncConn() (*wire.SyncConn, error) {
	conn, err := c.dialDevice()
	if err != nil {
		return nil, err
	}

	// Switch the connection to sync mode.
	if err := wire.SendMessageString(conn, "sync:"); err != nil {
		return nil, err
	}
	if _, err := conn.ReadStatus("sync"); err != nil {
		return nil, err
	}

	return conn.NewSyncConn(), nil
}

// dialDevice switches the connection to communicate directly with the device
// by requesting the transport defined by the DeviceDescriptor.
func (c *Device) dialDevice() (*wire.Conn, error) {
	conn, err := c.server.Dial()
	if err != nil {
		return nil, err
	}

	req := fmt.Sprintf("host:%s", c.descriptor.getTransportDescriptor())
	adbDebugf("[adb] >> %s", req)
	if err = wire.SendMessageString(conn, req); err != nil {
		conn.Close()
		return nil, errors.WrapErrf(err, "error connecting to device '%s'", c.descriptor)
	}

	if _, err = conn.ReadStatus(req); err != nil {
		conn.Close()
		return nil, err
	}

	return conn, nil
}

// prepareCommandLine validates the command and argument strings, quotes
// arguments if required, and joins them into a valid adb command string.
func prepareCommandLine(cmd string, args ...string) (string, error) {
	if isBlank(cmd) {
		return "", errors.AssertionErrorf("command cannot be empty")
	}

	for i, arg := range args {
		if strings.ContainsRune(arg, '"') {
			return "", errors.Errorf(errors.ParseError, "arg at index %d contains an invalid double quote: %s", i, arg)
		}
		if containsWhitespace(arg) {
			args[i] = fmt.Sprintf("\"%s\"", arg)
		}
	}

	// Prepend the command to the args array.
	if len(args) > 0 {
		cmd = fmt.Sprintf("%s %s", cmd, strings.Join(args, " "))
	}

	return cmd, nil
}

func (c *Device) Push(localPath, remotePath string) int64 {
	if remotePath == "" {
		return 1
	}

	var (
		localFile io.ReadCloser
		size      int
		perms     os.FileMode
		mtime     time.Time
	)
	if localPath == "" {
		localFile = os.Stdin
		// 0 size will hide the progress bar.
		perms = os.FileMode(0660)
		mtime = MtimeOfClose
	} else {
		var err error
		localFile, err = os.Open(localPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error opening local file %s: %s\n", localPath, err)
			return 1
		}
		info, err := os.Stat(localPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "error reading local file %s: %s\n", localPath, err)
			return 1
		}
		size = int(info.Size())
		perms = info.Mode().Perm()
		mtime = info.ModTime()
	}
	defer localFile.Close()

	client := c
	writer, err := client.OpenWrite(remotePath, perms, mtime)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening remote file %s: %s\n", remotePath, err)
		return 1
	}
	defer writer.Close()

	n, err := io.Copy(writer, localFile)
	/*if err := copyWithProgressAndStats(writer, localFile, size, showProgress); err != nil {
		fmt.Fprintln(os.Stderr, "error pushing file:", err)
		return 1
	}*/
	if n == int64(size) {
		return 0
	} else {
		return n
	}
}

func (c *Device) Pull(remotePath, localPath string) (int64, error) {
	if remotePath == "" {
		fmt.Fprintln(os.Stderr, "error: must specify remote file")
		return 0, fmt.Errorf("error: must specify remote file")
	}

	if localPath == "" {
		localPath = filepath.Base(remotePath)
	}

	client := c
	// client := client.Device(device)

	info, err := client.Stat(remotePath)
	if HasErrCode(err, ErrCode(FileNoExistError)) {
		// fmt.Fprintf(os.Stderr, "file %v does not exist on device ", remotePath)
		return 0, err
	} else if err != nil {
		// fmt.Fprintf(os.Stderr, "error reading remote file %s: %s\n", remotePath, err)
		return 0, err
	}

	remoteFile, err := client.OpenRead(remotePath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening remote file %s: %s\n", remotePath, ErrorWithCauseChain(err))
		return 0, err
	}
	defer remoteFile.Close()

	var localFile io.WriteCloser
	localFile, err = os.Create(localPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error opening local file %s: %s\n", localPath, err)
		return 0, err
	}
	defer localFile.Close()

	n, err := io.Copy(localFile, remoteFile)
	if err != nil {
		fmt.Fprintln(os.Stderr, "error pulling file:", err)
		return 0, err
	}

	if n == int64(info.Size) {
		return int64(info.Size), nil
	} else {
		return n, fmt.Errorf("error: file size: %v, pull size: %v", info.Size, n)
	}
}
