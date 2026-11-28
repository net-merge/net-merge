package tun

import (
	"io"
	"net"
	"testing"
	"time"
)

// An app sends its request, half-closes, and keeps reading the answer. The read deadline tun2socks
// then sets must not cut the download, and the half-close must still reach the server.
func TestHalfClosedDownloadKeepsReading(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = io.Copy(io.Discard, c) // the request, until the app half-closes
		time.Sleep(100 * time.Millisecond)
		if err == nil {
			c.Write([]byte("fin"))
		} else {
			c.Write([]byte("no fin"))
		}
	}()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c := keepReading{raw}
	defer c.Close()
	c.CloseWrite()
	c.SetReadDeadline(time.Now()) // what tun2socks does once the app is done sending
	b, err := io.ReadAll(c)
	if err != nil || string(b) != "fin" {
		t.Fatalf("read %q, %v", b, err)
	}
}
