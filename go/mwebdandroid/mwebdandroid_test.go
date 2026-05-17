package mwebdandroid

import (
	"context"
	"errors"
	"testing"

	"github.com/ltcmweb/mwebd/proto"
)

func TestUtxoStreamSend_replayCompleteSentinel_callsOnReplayComplete(t *testing.T) {
	listener := &recordingUtxoListener{}
	stream := newTestUtxoStream(context.Background(), listener)

	if err := stream.Send(&proto.Utxo{ReplayCompleteHeight: 123}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if len(listener.replayCompleteHeights) != 1 || listener.replayCompleteHeights[0] != 123 {
		t.Fatalf("unexpected replay complete heights: %v", listener.replayCompleteHeights)
	}
	if len(listener.utxos) != 0 {
		t.Fatalf("expected no regular UTXOs, got %d", len(listener.utxos))
	}
}

func TestUtxoStreamSend_initMarker_callsOnUtxoOnly(t *testing.T) {
	listener := &recordingUtxoListener{}
	stream := newTestUtxoStream(context.Background(), listener)

	if err := stream.Send(&proto.Utxo{}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if len(listener.utxos) != 1 {
		t.Fatalf("expected one regular UTXO marker, got %d", len(listener.utxos))
	}
	if len(listener.replayCompleteHeights) != 0 {
		t.Fatalf("expected no replay complete events, got %v", listener.replayCompleteHeights)
	}
}

func TestUtxoStreamSend_partialReplayCompleteSentinel_dropsMalformedMessage(t *testing.T) {
	listener := &recordingUtxoListener{}
	stream := newTestUtxoStream(context.Background(), listener)

	if err := stream.Send(&proto.Utxo{
		ReplayCompleteHeight: 123,
		OutputId:             "not-a-sentinel",
	}); err != nil {
		t.Fatalf("Send returned error: %v", err)
	}

	if len(listener.utxos) != 0 {
		t.Fatalf("expected malformed sentinel to be dropped, got %d regular UTXOs", len(listener.utxos))
	}
	if len(listener.replayCompleteHeights) != 0 {
		t.Fatalf("expected no replay complete events, got %v", listener.replayCompleteHeights)
	}
}

func TestUtxoStreamSend_cancelledContext_returnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	listener := &recordingUtxoListener{}
	stream := newTestUtxoStream(ctx, listener)

	err := stream.Send(&proto.Utxo{ReplayCompleteHeight: 123})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(listener.replayCompleteHeights) != 0 || len(listener.utxos) != 0 {
		t.Fatalf("expected cancelled stream to drop callbacks, got replay=%v utxos=%d", listener.replayCompleteHeights, len(listener.utxos))
	}
}

func TestUtxoStreamSend_cancelledContextRegularUtxo_returnsContextError(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	listener := &recordingUtxoListener{}
	stream := newTestUtxoStream(ctx, listener)

	err := stream.Send(&proto.Utxo{
		Height:   100,
		Value:    1,
		Address:  "ltcmweb1test",
		OutputId: "abcd",
	})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
	if len(listener.utxos) != 0 || len(listener.replayCompleteHeights) != 0 {
		t.Fatalf("expected cancelled stream to drop callbacks, got utxos=%d replay=%v", len(listener.utxos), listener.replayCompleteHeights)
	}
}

func newTestUtxoStream(ctx context.Context, listener UtxoListener) *utxoStream {
	return &utxoStream{
		ctx:      ctx,
		listener: listener,
	}
}

type recordingUtxoListener struct {
	utxos                 []*Utxo
	replayCompleteHeights []int64
	errors                []string
	completes             int
}

func (l *recordingUtxoListener) OnUtxo(utxo *Utxo) {
	l.utxos = append(l.utxos, utxo)
}

func (l *recordingUtxoListener) OnReplayComplete(height int64) {
	l.replayCompleteHeights = append(l.replayCompleteHeights, height)
}

func (l *recordingUtxoListener) OnError(message string) {
	l.errors = append(l.errors, message)
}

func (l *recordingUtxoListener) OnComplete() {
	l.completes++
}
