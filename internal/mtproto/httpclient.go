package mtproto

import (
	"fmt"
	"net"
	"net/http"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// markedHTTPClient stands in for upstream b4's netprobe.HTTPClient: an HTTP
// client whose connections carry SO_MARK=mark (none when mark is 0).
func markedHTTPClient(mark int, timeout time.Duration) *http.Client {
	d := &net.Dialer{Timeout: timeout, KeepAlive: timeout}
	if mark != 0 {
		d.Control = func(_, _ string, c syscall.RawConn) error {
			var serr error
			if cerr := c.Control(func(fd uintptr) {
				serr = unix.SetsockoptInt(int(fd), unix.SOL_SOCKET, unix.SO_MARK, mark)
			}); cerr != nil {
				return cerr
			}
			if serr != nil {
				return fmt.Errorf("failed to set SO_MARK=%d: %w", mark, serr)
			}
			return nil
		}
	}
	tr := &http.Transport{
		DialContext:           d.DialContext,
		ForceAttemptHTTP2:     true,
		TLSHandshakeTimeout:   timeout,
		ResponseHeaderTimeout: timeout,
		MaxIdleConns:          100,
		IdleConnTimeout:       30 * time.Second,
	}
	return &http.Client{Transport: tr, Timeout: timeout}
}
