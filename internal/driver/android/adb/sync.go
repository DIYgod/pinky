package adb

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"time"
)

// syncMaxChunk is the largest DATA payload the sync protocol allows.
const syncMaxChunk = 64 * 1024

// FileInfo is what the sync STAT request returns.
type FileInfo struct {
	Mode    os.FileMode // Unix mode bits as the device reports them
	Size    int64
	ModTime time.Time
}

// Exists reports whether the stat found a file (adb reports zeros for a
// missing path).
func (fi FileInfo) Exists() bool { return fi.Mode != 0 || fi.Size != 0 || !fi.ModTime.IsZero() }

type syncConn struct {
	conn net.Conn
	r    *bufio.Reader
}

func (c *Client) openSync(ctx context.Context, serial string) (*syncConn, func() bool, error) {
	conn, err := c.Dial(ctx, serial, "sync:")
	if err != nil {
		return nil, nil, err
	}
	stop := context.AfterFunc(ctx, func() { _ = conn.SetDeadline(time.Unix(1, 0)) })
	return &syncConn{conn: conn, r: bufio.NewReaderSize(conn, syncMaxChunk+8)}, stop, nil
}

func (s *syncConn) close() {
	// QUIT is a courtesy; the server also handles a plain close.
	_ = s.send("QUIT", nil)
	s.conn.Close()
}

func (s *syncConn) send(id string, data []byte) error {
	hdr := make([]byte, 8, 8+len(data))
	copy(hdr, id)
	binary.LittleEndian.PutUint32(hdr[4:], uint32(len(data)))
	_, err := s.conn.Write(append(hdr, data...))
	return err
}

func (s *syncConn) readHeader() (string, uint32, error) {
	var hdr [8]byte
	if _, err := io.ReadFull(s.r, hdr[:]); err != nil {
		return "", 0, err
	}
	return string(hdr[:4]), binary.LittleEndian.Uint32(hdr[4:]), nil
}

func (s *syncConn) readFail(n uint32) error {
	if n > 64*1024 {
		return fmt.Errorf("adb: sync: failure message too long (%d)", n)
	}
	msg := make([]byte, n)
	if _, err := io.ReadFull(s.r, msg); err != nil {
		return err
	}
	return &Error{Msg: string(msg)}
}

// Push writes the content of r to remote on the device with the given
// permission bits (for example 0644) and modification time.
func (c *Client) Push(ctx context.Context, serial string, r io.Reader, remote string, perm os.FileMode, mtime time.Time) error {
	s, stop, err := c.openSync(ctx, serial)
	if err != nil {
		return err
	}
	defer stop()
	defer s.close()
	// The mode is sent in decimal and includes the regular-file type bit.
	spec := fmt.Sprintf("%s,%d", remote, 0o100000|uint32(perm.Perm()))
	if err := s.send("SEND", []byte(spec)); err != nil {
		return ctxErr(ctx, fmt.Errorf("adb: push %s: %w", remote, err))
	}
	buf := make([]byte, syncMaxChunk)
	for {
		n, rerr := io.ReadFull(r, buf)
		if n > 0 {
			if err := s.send("DATA", buf[:n]); err != nil {
				return ctxErr(ctx, fmt.Errorf("adb: push %s: %w", remote, err))
			}
		}
		if rerr == io.EOF || rerr == io.ErrUnexpectedEOF {
			break
		}
		if rerr != nil {
			return fmt.Errorf("adb: push %s: read source: %w", remote, rerr)
		}
	}
	var done [8]byte
	copy(done[:], "DONE")
	binary.LittleEndian.PutUint32(done[4:], uint32(mtime.Unix()))
	if _, err := s.conn.Write(done[:]); err != nil {
		return ctxErr(ctx, fmt.Errorf("adb: push %s: %w", remote, err))
	}
	id, n, err := s.readHeader()
	if err != nil {
		return ctxErr(ctx, fmt.Errorf("adb: push %s: %w", remote, err))
	}
	switch id {
	case "OKAY":
		return nil
	case "FAIL":
		return fmt.Errorf("push %s: %w", remote, s.readFail(n))
	default:
		return fmt.Errorf("adb: push %s: unexpected reply %q", remote, id)
	}
}

// Pull copies remote from the device into w and returns the bytes copied.
func (c *Client) Pull(ctx context.Context, serial, remote string, w io.Writer) (int64, error) {
	s, stop, err := c.openSync(ctx, serial)
	if err != nil {
		return 0, err
	}
	defer stop()
	defer s.close()
	if err := s.send("RECV", []byte(remote)); err != nil {
		return 0, ctxErr(ctx, fmt.Errorf("adb: pull %s: %w", remote, err))
	}
	var total int64
	for {
		id, n, err := s.readHeader()
		if err != nil {
			return total, ctxErr(ctx, fmt.Errorf("adb: pull %s: %w", remote, err))
		}
		switch id {
		case "DATA":
			if n > syncMaxChunk {
				return total, fmt.Errorf("adb: pull %s: chunk too large (%d)", remote, n)
			}
			m, err := io.CopyN(w, s.r, int64(n))
			total += m
			if err != nil {
				return total, ctxErr(ctx, fmt.Errorf("adb: pull %s: %w", remote, err))
			}
		case "DONE":
			return total, nil
		case "FAIL":
			return total, fmt.Errorf("pull %s: %w", remote, s.readFail(n))
		default:
			return total, fmt.Errorf("adb: pull %s: unexpected reply %q", remote, id)
		}
	}
}

// Stat returns the mode, size and modification time of remote. A missing
// path returns a FileInfo whose Exists is false.
func (c *Client) Stat(ctx context.Context, serial, remote string) (FileInfo, error) {
	s, stop, err := c.openSync(ctx, serial)
	if err != nil {
		return FileInfo{}, err
	}
	defer stop()
	defer s.close()
	if err := s.send("STAT", []byte(remote)); err != nil {
		return FileInfo{}, ctxErr(ctx, fmt.Errorf("adb: stat %s: %w", remote, err))
	}
	var buf [16]byte
	if _, err := io.ReadFull(s.r, buf[:]); err != nil {
		return FileInfo{}, ctxErr(ctx, fmt.Errorf("adb: stat %s: %w", remote, err))
	}
	if string(buf[:4]) != "STAT" {
		return FileInfo{}, fmt.Errorf("adb: stat %s: unexpected reply %q", remote, buf[:4])
	}
	mode := binary.LittleEndian.Uint32(buf[4:])
	size := binary.LittleEndian.Uint32(buf[8:])
	mtime := binary.LittleEndian.Uint32(buf[12:])
	fi := FileInfo{Mode: unixMode(mode), Size: int64(size)}
	if mtime != 0 {
		fi.ModTime = time.Unix(int64(mtime), 0)
	}
	return fi, nil
}

// unixMode converts st_mode bits to an os.FileMode.
func unixMode(m uint32) os.FileMode {
	fm := os.FileMode(m & 0o777)
	switch m & 0o170000 {
	case 0o040000:
		fm |= os.ModeDir
	case 0o120000:
		fm |= os.ModeSymlink
	case 0o020000:
		fm |= os.ModeDevice | os.ModeCharDevice
	case 0o060000:
		fm |= os.ModeDevice
	case 0o010000:
		fm |= os.ModeNamedPipe
	case 0o140000:
		fm |= os.ModeSocket
	}
	return fm
}
