package mwebd

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/btcsuite/btclog"
)

func TestNeutrinoLogger_disabled_writesNoFile(t *testing.T) {
	dataDir := t.TempDir()

	logger := neutrinoLogger(&ServerArgs{DataDir: dataDir, DisableDebugLog: true})
	logger.Errorf("peer 192.0.2.1")

	if logger != btclog.Disabled {
		t.Fatal("expected the disabled logger")
	}
	if _, err := os.Stat(filepath.Join(dataDir, "logs")); !os.IsNotExist(err) {
		t.Fatalf("logs directory must not exist, stat error: %v", err)
	}
}

func TestNeutrinoLogger_default_writesDebugLog(t *testing.T) {
	dataDir := t.TempDir()

	neutrinoLogger(&ServerArgs{DataDir: dataDir}).Errorf("peer 192.0.2.1")

	if _, err := os.Stat(filepath.Join(dataDir, "logs", "debug.log")); err != nil {
		t.Fatalf("debug.log must be written by default: %v", err)
	}
}
