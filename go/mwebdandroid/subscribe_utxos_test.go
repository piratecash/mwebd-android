package mwebdandroid

import (
	"bytes"
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
// inside the streaming goroutine (here a server whose utxoChan is nil — the
// reported crash condition) is converted into OnError/OnComplete instead of
// aborting the whole process. A daemon subscription must never be able to take
// down the host app.
func TestSubscribeUtxos_panicInStream_reportedNotCrashed(t *testing.T) {
	d := &Daemon{server: mwebd.NewBareServer(chaincfg.MainNetParams)}
	listener := newRecordingListener()
	scanSecret := make([]byte, 32)

	if _, err := d.SubscribeUtxos(0, scanSecret, listener); err != nil {
		t.Fatalf("SubscribeUtxos returned error: %v", err)
	}

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
