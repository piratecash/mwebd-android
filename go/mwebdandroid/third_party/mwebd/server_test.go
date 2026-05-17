package mwebd

import (
	"context"
	"testing"

	"github.com/ltcmweb/mwebd/proto"
	"google.golang.org/grpc/metadata"
)

func TestSendReplayComplete_sendsSnapshotHeight(t *testing.T) {
	stream := &recordingUtxosStream{}

	if err := sendReplayComplete(stream, 3_108_749); err != nil {
		t.Fatalf("sendReplayComplete returned error: %v", err)
	}

	if len(stream.utxos) != 1 {
		t.Fatalf("expected one sent UTXO message, got %d", len(stream.utxos))
	}
	if stream.utxos[0].ReplayCompleteHeight != 3_108_749 {
		t.Fatalf("unexpected replay complete height: %d", stream.utxos[0].ReplayCompleteHeight)
	}
	if stream.utxos[0].Height != 0 || stream.utxos[0].OutputId != "" || stream.utxos[0].Value != 0 {
		t.Fatalf("replay complete sentinel must not look like a regular UTXO: %+v", stream.utxos[0])
	}
}

type recordingUtxosStream struct {
	proto.UnimplementedRpcServer
	utxos []*proto.Utxo
}

func (s *recordingUtxosStream) Send(utxo *proto.Utxo) error {
	s.utxos = append(s.utxos, utxo)
	return nil
}

func (s *recordingUtxosStream) SetHeader(metadata.MD) error {
	return nil
}

func (s *recordingUtxosStream) SendHeader(metadata.MD) error {
	return nil
}

func (s *recordingUtxosStream) SetTrailer(metadata.MD) {
}

func (s *recordingUtxosStream) Context() context.Context {
	return context.Background()
}

func (s *recordingUtxosStream) SendMsg(any) error {
	return nil
}

func (s *recordingUtxosStream) RecvMsg(any) error {
	return nil
}
