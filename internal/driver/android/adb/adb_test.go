package adb

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer speaks enough of the adb server protocol to test the client.
type fakeServer struct {
	t       *testing.T
	ln      net.Listener
	devices string

	mu       sync.Mutex
	files    map[string][]byte
	modes    map[string]uint32
	forwards []Forward
	requests []string
}

func newFakeServer(t *testing.T) (*fakeServer, *Client) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	s := &fakeServer{t: t, ln: ln, files: map[string][]byte{}, modes: map[string]uint32{},
		devices: "3e8be48e               device usb:1048576X product:lineage_cmi model:Mi_10_Pro device:cmi transport_id:12607\n" +
			"FA6C20303860           unauthorized usb:18874368X transport_id:12608\n"}
	go s.serve()
	t.Cleanup(func() { ln.Close() })
	return s, &Client{Addr: ln.Addr().String()}
}

func (s *fakeServer) serve() {
	for {
		c, err := s.ln.Accept()
		if err != nil {
			return
		}
		go s.handle(c)
	}
}

func readReq(r io.Reader) (string, error) {
	return readString(r)
}

func okay(w io.Writer)               { io.WriteString(w, "OKAY") }
func fail(w io.Writer, msg string)   { fmt.Fprintf(w, "FAIL%04x%s", len(msg), msg) }
func writeStr(w io.Writer, s string) { fmt.Fprintf(w, "%04x%s", len(s), s) }
func (s *fakeServer) log(req string) {
	s.mu.Lock()
	s.requests = append(s.requests, req)
	s.mu.Unlock()
}

func (s *fakeServer) handle(c net.Conn) {
	defer c.Close()
	r := bufio.NewReader(c)
	req, err := readReq(r)
	if err != nil {
		return
	}
	s.log(req)
	switch {
	case req == "host:version":
		okay(c)
		writeStr(c, "0029")
	case req == "host:devices-l":
		okay(c)
		writeStr(c, s.devices)
	case req == "host:track-devices-l":
		okay(c)
		writeStr(c, s.devices)
		writeStr(c, "")
	case req == "host:list-forward":
		okay(c)
		s.mu.Lock()
		var b strings.Builder
		for _, f := range s.forwards {
			fmt.Fprintf(&b, "%s %s %s\n", f.Serial, f.Local, f.Remote)
		}
		s.mu.Unlock()
		writeStr(c, b.String())
	case strings.HasPrefix(req, "host-serial:"):
		rest := strings.TrimPrefix(req, "host-serial:")
		serial, svc, _ := strings.Cut(rest, ":")
		switch {
		case strings.HasPrefix(svc, "forward:"):
			spec := strings.TrimPrefix(strings.TrimPrefix(svc, "forward:"), "norebind:")
			local, remote, _ := strings.Cut(spec, ";")
			okay(c)
			okay(c)
			if local == "tcp:0" {
				local = "tcp:43210"
				writeStr(c, "43210")
			}
			s.mu.Lock()
			s.forwards = append(s.forwards, Forward{serial, local, remote})
			s.mu.Unlock()
		case strings.HasPrefix(svc, "killforward:"):
			local := strings.TrimPrefix(svc, "killforward:")
			s.mu.Lock()
			found := false
			for i, f := range s.forwards {
				if f.Local == local {
					s.forwards = append(s.forwards[:i], s.forwards[i+1:]...)
					found = true
					break
				}
			}
			s.mu.Unlock()
			okay(c)
			if !found {
				fail(c, "listener '"+local+"' not found")
				return
			}
			okay(c)
		default:
			fail(c, "unknown host service")
		}
	case strings.HasPrefix(req, "host:transport:"):
		serial := strings.TrimPrefix(req, "host:transport:")
		switch serial {
		case "3e8be48e":
		case "FA6C20303860":
			fail(c, "device unauthorized.\nThis adb server's $ADB_VENDOR_KEYS is not set")
			return
		default:
			fail(c, "device '"+serial+"' not found")
			return
		}
		okay(c)
		svc, err := readReq(r)
		if err != nil {
			return
		}
		s.log(svc)
		s.device(c, r, svc)
	default:
		fail(c, "unknown host service")
	}
}

func (s *fakeServer) device(c net.Conn, r *bufio.Reader, svc string) {
	switch {
	case strings.HasPrefix(svc, "shell,v2,raw:"):
		okay(c)
		cmd := strings.TrimPrefix(svc, "shell,v2,raw:")
		pkt := func(id byte, data []byte) {
			var hdr [5]byte
			hdr[0] = id
			binary.LittleEndian.PutUint32(hdr[1:], uint32(len(data)))
			c.Write(append(hdr[:], data...))
		}
		pkt(shellStdout, []byte("out:"+cmd))
		pkt(shellStderr, []byte("err"))
		pkt(9, []byte("ignored"))
		pkt(shellExit, []byte{3})
	case strings.HasPrefix(svc, "shell:"):
		okay(c)
		cmd := strings.TrimPrefix(svc, "shell:")
		if cmd == "getprop" {
			io.WriteString(c, "[ro.product.model]: [Mi 10 Pro]\n[ro.build.version.release]: [17]\n[multi.line]: [a\nb]\n[empty]: []\n")
			return
		}
		if cmd == "sleep" {
			time.Sleep(5 * time.Second)
			return
		}
		io.WriteString(c, "shell:"+cmd)
	case strings.HasPrefix(svc, "exec:"):
		okay(c)
		c.Write([]byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'})
	case strings.HasPrefix(svc, "localabstract:"):
		okay(c)
		io.Copy(c, r)
	case svc == "sync:":
		okay(c)
		s.sync(c, r)
	default:
		fail(c, "closed")
	}
}

func (s *fakeServer) sync(c net.Conn, r *bufio.Reader) {
	readHdr := func() (string, uint32, bool) {
		var hdr [8]byte
		if _, err := io.ReadFull(r, hdr[:]); err != nil {
			return "", 0, false
		}
		return string(hdr[:4]), binary.LittleEndian.Uint32(hdr[4:]), true
	}
	send := func(id string, data []byte) {
		hdr := make([]byte, 8)
		copy(hdr, id)
		binary.LittleEndian.PutUint32(hdr[4:], uint32(len(data)))
		c.Write(append(hdr, data...))
	}
	for {
		id, n, ok := readHdr()
		if !ok {
			return
		}
		arg := make([]byte, n)
		if id != "DONE" {
			io.ReadFull(r, arg)
		}
		switch id {
		case "QUIT":
			return
		case "STAT":
			s.mu.Lock()
			data, exists := s.files[string(arg)]
			mode := s.modes[string(arg)]
			s.mu.Unlock()
			var out [16]byte
			copy(out[:], "STAT")
			if exists {
				binary.LittleEndian.PutUint32(out[4:], mode)
				binary.LittleEndian.PutUint32(out[8:], uint32(len(data)))
				binary.LittleEndian.PutUint32(out[12:], 1700000000)
			}
			c.Write(out[:])
		case "SEND":
			path, modeStr, _ := strings.Cut(string(arg), ",")
			mode, _ := strconv.ParseUint(modeStr, 10, 32)
			var buf bytes.Buffer
			for {
				id, n, ok := readHdr()
				if !ok {
					return
				}
				if id == "DONE" {
					break
				}
				if id != "DATA" || n > syncMaxChunk {
					send("FAIL", []byte("bad chunk"))
					return
				}
				io.CopyN(&buf, r, int64(n))
			}
			if strings.HasPrefix(path, "/readonly/") {
				send("FAIL", []byte("couldn't create file: Read-only file system"))
				continue
			}
			s.mu.Lock()
			s.files[path] = buf.Bytes()
			s.modes[path] = uint32(mode)
			s.mu.Unlock()
			send("OKAY", nil)
		case "RECV":
			s.mu.Lock()
			data, exists := s.files[string(arg)]
			s.mu.Unlock()
			if !exists {
				send("FAIL", []byte("No such file or directory"))
				continue
			}
			for len(data) > 0 {
				k := min(len(data), syncMaxChunk)
				send("DATA", data[:k])
				data = data[k:]
			}
			send("DONE", nil)
		}
	}
}

func TestVersionAndDevices(t *testing.T) {
	_, c := newFakeServer(t)
	ctx := context.Background()
	v, err := c.Version(ctx)
	if err != nil || v != 41 {
		t.Fatalf("Version = %d, %v", v, err)
	}
	ds, err := c.Devices(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(ds) != 2 {
		t.Fatalf("got %d devices", len(ds))
	}
	if d := ds[0]; d.Serial != "3e8be48e" || d.State != StateDevice || d.Model != "Mi_10_Pro" || d.TransportID != "12607" || d.Wireless() {
		t.Errorf("device 0 = %+v", d)
	}
	if ds[1].State != StateUnauthorized {
		t.Errorf("device 1 state = %q", ds[1].State)
	}
}

func TestParseDevices(t *testing.T) {
	text := "List of devices attached\n" +
		"emulator-5554          device product:sdk_gphone64_arm64 model:sdk_gphone64_arm64 device:emu64a transport_id:3\n" +
		"192.168.1.20:5555      offline transport_id:4\n" +
		"adb-R58M-abc._adb-tls-connect._tcp device product:x model:SM_G9 device:y transport_id:5\n" +
		"0123456789ABCDEF       no permissions (missing udev rules? user is in the plugdev group); see [http://developer.android.com/tools/device.html] usb:1-1 transport_id:6\n"
	ds := ParseDevices(text)
	if len(ds) != 4 {
		t.Fatalf("got %d: %+v", len(ds), ds)
	}
	if ds[0].Wireless() || !ds[1].Wireless() || !ds[2].Wireless() || ds[3].Wireless() {
		t.Errorf("wireless flags wrong: %+v", ds)
	}
	if ds[1].State != StateOffline {
		t.Errorf("state = %q", ds[1].State)
	}
	if ds[3].State != StateNoPerm || !strings.Contains(ds[3].Detail, "udev") || ds[3].USB != "1-1" || ds[3].TransportID != "6" {
		t.Errorf("no-permissions entry = %+v", ds[3])
	}
}

func TestTrackDevices(t *testing.T) {
	_, c := newFakeServer(t)
	var got [][]DeviceEntry
	err := c.TrackDevices(context.Background(), func(d []DeviceEntry) { got = append(got, d) })
	if err == nil {
		t.Fatal("want an error when the server closes the stream")
	}
	if len(got) != 2 || len(got[0]) != 2 || len(got[1]) != 0 {
		t.Fatalf("updates = %+v", got)
	}
}

func TestDialErrors(t *testing.T) {
	_, c := newFakeServer(t)
	ctx := context.Background()
	_, err := c.Shell(ctx, "nope", "ls")
	if !errors.Is(err, ErrDeviceNotFound) {
		t.Errorf("unknown serial: %v", err)
	}
	_, err = c.Shell(ctx, "FA6C20303860", "ls")
	if !errors.Is(err, ErrUnauthorized) {
		t.Errorf("unauthorized: %v", err)
	}
	_, err = c.Dial(ctx, "3e8be48e", "jdwp:1")
	var ae *Error
	if !errors.As(err, &ae) || ae.Msg != "closed" {
		t.Errorf("unknown service: %v", err)
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	_, err = (&Client{Addr: addr}).Version(ctx)
	if !errors.Is(err, ErrServerNotRunning) {
		t.Errorf("no server: %v", err)
	}
}

func TestShellExecRun(t *testing.T) {
	s, c := newFakeServer(t)
	ctx := context.Background()
	out, err := c.Shell(ctx, "3e8be48e", Command("echo", "it's here"))
	if err != nil || string(out) != `shell:echo 'it'\''s here'` {
		t.Fatalf("Shell = %q, %v", out, err)
	}
	png, err := c.Exec(ctx, "3e8be48e", "screencap -p")
	if err != nil || !bytes.HasPrefix(png, []byte("\x89PNG\r\n\x1a\n")) {
		t.Fatalf("Exec = %q, %v", png, err)
	}
	res, err := c.Run(ctx, "3e8be48e", "false")
	if err != nil || string(res.Stdout) != "out:false" || string(res.Stderr) != "err" || res.ExitCode != 3 {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !contains(s.requests, "exec:screencap -p") || !contains(s.requests, "shell,v2,raw:false") {
		t.Errorf("requests = %q", s.requests)
	}
}

func TestShellCancel(t *testing.T) {
	_, c := newFakeServer(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.Shell(ctx, "3e8be48e", "sleep")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("cancel did not interrupt the read")
	}
}

func TestProps(t *testing.T) {
	_, c := newFakeServer(t)
	p, err := c.Props(context.Background(), "3e8be48e")
	if err != nil {
		t.Fatal(err)
	}
	if p["ro.product.model"] != "Mi 10 Pro" || p["ro.build.version.release"] != "17" || p["multi.line"] != "a\nb" {
		t.Errorf("props = %q", p)
	}
	if v, ok := p["empty"]; !ok || v != "" {
		t.Errorf("empty prop = %q, %v", v, ok)
	}
}

func TestPushPullStat(t *testing.T) {
	_, c := newFakeServer(t)
	ctx := context.Background()
	data := bytes.Repeat([]byte("pinky"), 30000) // spans several chunks
	if err := c.Push(ctx, "3e8be48e", bytes.NewReader(data), "/data/local/tmp/x.jar", 0o644, time.Unix(1700000000, 0)); err != nil {
		t.Fatal(err)
	}
	fi, err := c.Stat(ctx, "3e8be48e", "/data/local/tmp/x.jar")
	if err != nil {
		t.Fatal(err)
	}
	if !fi.Exists() || fi.Size != int64(len(data)) || fi.Mode.Perm() != 0o644 || !fi.Mode.IsRegular() {
		t.Errorf("stat = %+v", fi)
	}
	var buf bytes.Buffer
	n, err := c.Pull(ctx, "3e8be48e", "/data/local/tmp/x.jar", &buf)
	if err != nil || n != int64(len(data)) || !bytes.Equal(buf.Bytes(), data) {
		t.Fatalf("pull: n=%d err=%v equal=%v", n, err, bytes.Equal(buf.Bytes(), data))
	}
	fi, err = c.Stat(ctx, "3e8be48e", "/missing")
	if err != nil || fi.Exists() {
		t.Errorf("missing stat = %+v, %v", fi, err)
	}
	_, err = c.Pull(ctx, "3e8be48e", "/missing", io.Discard)
	var ae *Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Msg, "No such file") {
		t.Errorf("pull missing: %v", err)
	}
	err = c.Push(ctx, "3e8be48e", strings.NewReader("x"), "/readonly/x", 0o644, time.Now())
	if !errors.As(err, &ae) || !strings.Contains(ae.Msg, "Read-only") {
		t.Errorf("push read-only: %v", err)
	}
	// An empty file still works.
	if err := c.Push(ctx, "3e8be48e", strings.NewReader(""), "/data/local/tmp/empty", 0o600, time.Now()); err != nil {
		t.Fatal(err)
	}
}

func TestForward(t *testing.T) {
	s, c := newFakeServer(t)
	ctx := context.Background()
	local, err := c.Forward(ctx, "3e8be48e", "tcp:0", "localabstract:scrcpy_1", false)
	if err != nil || local != "tcp:43210" {
		t.Fatalf("Forward = %q, %v", local, err)
	}
	local2, err := c.Forward(ctx, "3e8be48e", "tcp:27183", "localabstract:scrcpy_2", true)
	if err != nil || local2 != "tcp:27183" {
		t.Fatalf("Forward fixed = %q, %v", local2, err)
	}
	fs, err := c.Forwards(ctx)
	if err != nil || len(fs) != 2 || fs[0] != (Forward{"3e8be48e", "tcp:43210", "localabstract:scrcpy_1"}) {
		t.Fatalf("Forwards = %+v, %v", fs, err)
	}
	if err := c.RemoveForward(ctx, "3e8be48e", "tcp:43210"); err != nil {
		t.Fatal(err)
	}
	if err := c.RemoveForward(ctx, "3e8be48e", "tcp:1"); err == nil {
		t.Error("removing a missing forward should fail")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !contains(s.requests, "host-serial:3e8be48e:forward:norebind:tcp:0;localabstract:scrcpy_1") {
		t.Errorf("requests = %q", s.requests)
	}
}

func TestDialLocalSocket(t *testing.T) {
	_, c := newFakeServer(t)
	conn, err := c.Dial(context.Background(), "3e8be48e", "localabstract:echo")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.Write([]byte("ping"))
	var buf [4]byte
	if _, err := io.ReadFull(conn, buf[:]); err != nil || string(buf[:]) != "ping" {
		t.Fatalf("echo = %q, %v", buf, err)
	}
}

func TestQuote(t *testing.T) {
	for in, want := range map[string]string{
		"":                 "''",
		"plain/path-1.jar": "plain/path-1.jar",
		"a b":              "'a b'",
		"it's":             `'it'\''s'`,
		"$HOME;rm":         "'$HOME;rm'",
		"key=value":        "key=value",
	} {
		if got := Quote(in); got != want {
			t.Errorf("Quote(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestServerAddr(t *testing.T) {
	t.Setenv("ADB_SERVER_SOCKET", "")
	t.Setenv("ANDROID_ADB_SERVER_ADDRESS", "")
	t.Setenv("ANDROID_ADB_SERVER_PORT", "")
	if a := ServerAddr(); a != DefaultAddr {
		t.Errorf("default = %q", a)
	}
	t.Setenv("ANDROID_ADB_SERVER_PORT", "5038")
	if a := ServerAddr(); a != "127.0.0.1:5038" {
		t.Errorf("port env = %q", a)
	}
	t.Setenv("ADB_SERVER_SOCKET", "tcp:10.0.0.2:5039")
	if a := ServerAddr(); a != "10.0.0.2:5039" {
		t.Errorf("socket env = %q", a)
	}
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
