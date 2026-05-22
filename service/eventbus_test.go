package service

import (
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestEventBus_PublishesWarning(t *testing.T) {
	dir := t.TempDir()
	sock := filepath.Join(dir, "mb.sock")
	ln, err := net.Listen("unix", sock)
	require.NoError(t, err)
	defer ln.Close()
	defer os.Remove(sock)

	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil { return }
		buf := make([]byte, 1024)
		n, _ := c.Read(buf)
		got <- buf[:n]
		c.Close()
	}()

	eb := NewEventBus(sock)
	eb.PublishWarning("embedder_offline", "parser at 127.0.0.1:8283 timeout")

	select {
	case msg := <-got:
		require.Contains(t, string(msg), "Search:Warning")
		require.Contains(t, string(msg), "embedder_offline")
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for event")
	}
}

func TestEventBus_FailsSilentlyWhenSocketMissing(t *testing.T) {
	eb := NewEventBus("/nonexistent/mb.sock")
	// Must not panic
	eb.PublishWarning("k", "v")
}
