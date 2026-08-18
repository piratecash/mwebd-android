package mwebd

import (
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg"
)

func TestServerStop_waitsForTrackedOperation(t *testing.T) {
	server := NewBareServer(chaincfg.MainNetParams)
	if !server.beginOperation(false) {
		t.Fatal("expected operation registration to succeed")
	}

	stopped := make(chan struct{})
	go func() {
		_ = server.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
		t.Fatal("Stop returned before the tracked operation completed")
	case <-time.After(50 * time.Millisecond):
	}

	server.operations.Done()
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return after the tracked operation completed")
	}
	if server.beginOperation(false) {
		server.operations.Done()
		t.Fatal("operation registered after Stop")
	}
}

func TestServerStart_afterStop_rejected(t *testing.T) {
	server := NewBareServer(chaincfg.MainNetParams)
	if err := server.Stop(); err != nil {
		t.Fatalf("Stop returned error: %v", err)
	}
	if _, err := server.Start(0); err == nil {
		t.Fatal("Start after Stop must fail")
	}
}
