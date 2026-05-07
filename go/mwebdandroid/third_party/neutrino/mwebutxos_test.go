package neutrino

import (
	"testing"
	"time"
)

func TestMwebUtxosQueryOptions_allowMobilePeerRetries(t *testing.T) {
	if mwebUtxosQueryTimeout < 5*time.Minute {
		t.Fatalf("mweb utxo query timeout is too short: %v", mwebUtxosQueryTimeout)
	}

	if mwebUtxosQueryRetries < 10 {
		t.Fatalf("mweb utxo query retries are too low: %v", mwebUtxosQueryRetries)
	}
}
