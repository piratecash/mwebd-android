package mwebdandroid

import (
	"context"
	"testing"

	"github.com/ltcmweb/ltcd/chaincfg"
	"github.com/ltcmweb/mwebd"
)

func TestDaemonOperations_beforeStart_rejected(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		server:        mwebd.NewBareServer(chaincfg.MainNetParams),
		state:         daemonNew,
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: map[*UtxoSubscription]struct{}{},
	}

	if _, err := d.Status(); err == nil {
		t.Fatal("Status before Start must fail")
	}
	if _, err := d.SubscribeUtxos(0, make([]byte, 32), newRecordingListener()); err == nil {
		t.Fatal("SubscribeUtxos before Start must fail")
	}
}

func TestDaemonInputs_invalidSignedValues_rejected(t *testing.T) {
	d := &Daemon{}
	if _, err := d.Addresses(nil, nil, -1, 1); err == nil {
		t.Fatal("negative address index must fail")
	}
	if _, err := d.Create(nil, nil, nil, -1, false); err == nil {
		t.Fatal("negative fee rate must fail")
	}
	if _, err := d.SubscribeUtxos(-1, nil, newRecordingListener()); err == nil {
		t.Fatal("negative UTXO height must fail")
	}
	if _, err := d.Start(65536); err == nil {
		t.Fatal("out-of-range port must fail")
	}
}
