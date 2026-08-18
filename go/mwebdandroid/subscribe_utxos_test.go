package mwebdandroid

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg"
	"github.com/ltcmweb/mwebd"
)

type recordingListener struct {
	errs      chan string
	completed chan struct{}
}

func newRecordingListener() *recordingListener {
	return &recordingListener{
		errs:      make(chan string, 1),
		completed: make(chan struct{}, 1),
	}
}

func (l *recordingListener) OnUtxo(*Utxo)           {}
func (l *recordingListener) OnReplayComplete(int64) {}
func (l *recordingListener) OnError(message string) {
	select {
	case l.errs <- message:
	default:
	}
}
func (l *recordingListener) OnComplete() {
	select {
	case l.completed <- struct{}{}:
	default:
	}
}

// TestSubscribeUtxos_panicInStream_reportedNotCrashed verifies that a panic
// inside the streaming goroutine (here a bare server without ChainService — the
// reported crash condition) is converted into OnError/OnComplete instead of
// aborting the whole process. A daemon subscription must never be able to take
// down the host app.
func TestSubscribeUtxos_panicInStream_reportedNotCrashed(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		server:        mwebd.NewBareServer(chaincfg.MainNetParams),
		state:         daemonRunning,
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: map[*UtxoSubscription]struct{}{},
	}
	listener := newRecordingListener()
	scanSecret := make([]byte, 32)

	subscription, err := d.SubscribeUtxos(0, scanSecret, listener)
	if err != nil {
		t.Fatalf("SubscribeUtxos returned error: %v", err)
	}
	defer subscription.Close()

	select {
	case <-listener.errs:
		// Panic was recovered and surfaced as an error.
	case <-time.After(3 * time.Second):
		t.Fatal("expected OnError after a panicking stream, got none (panic was not recovered)")
	}

	select {
	case <-listener.completed:
	case <-time.After(time.Second):
		t.Fatal("expected OnComplete after the stream goroutine ended")
	}
}

func TestUtxoSubscriptionClose_waitsForCompletion(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	d := &Daemon{
		server:        mwebd.NewBareServer(chaincfg.MainNetParams),
		state:         daemonRunning,
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: map[*UtxoSubscription]struct{}{},
	}
	listener := newRecordingListener()
	subscription, err := d.SubscribeUtxos(0, make([]byte, 32), listener)
	if err != nil {
		t.Fatalf("SubscribeUtxos returned error: %v", err)
	}

	subscription.Close()

	select {
	case <-listener.completed:
	default:
		t.Fatal("Close returned before OnComplete")
	}
	select {
	case <-subscription.done:
	default:
		t.Fatal("Close returned before the stream goroutine stopped")
	}
}

// TestCopyScanSecret_isIndependentOfCaller verifies the scan secret is copied
// into a Go-owned buffer. gomobile passes []byte params as transient views over
// a JNI buffer that is released once the bound call returns; the streaming
// goroutine in SubscribeUtxos uses the secret after returning, so it must own a
// copy made synchronously at the boundary, not a view into freed memory.
func TestCopyScanSecret_isIndependentOfCaller(t *testing.T) {
	src := bytes.Repeat([]byte{0xAA}, 32)
	got := copyScanSecret(src)

	// Caller's transient buffer is released/reused after the call returns.
	for i := range src {
		src[i] = 0xBB
	}

	if want := bytes.Repeat([]byte{0xAA}, 32); !bytes.Equal(got, want) {
		t.Fatalf("scan secret not copied: got %x, want %x", got, want)
	}
	if len(got) > 0 && &got[0] == &src[0] {
		t.Fatal("copyScanSecret returned an alias of the caller buffer, not a copy")
	}
}
