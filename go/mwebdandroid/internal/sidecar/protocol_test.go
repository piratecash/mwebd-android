package sidecar

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestReadInit_validFrame_decodesEveryField(t *testing.T) {
	buffer := &bytes.Buffer{}
	buffer.Write(initMagic[:])
	writeTestUint32(t, buffer, ProtocolVersion)
	buffer.WriteByte(ModeDaemon)
	writeTestBytes(t, buffer, []byte("token"))
	writeTestBytes(t, buffer, []byte("regtest"))
	writeTestBytes(t, buffer, []byte("/tmp/mwebd"))
	writeTestBytes(t, buffer, []byte("peer"))
	writeTestBytes(t, buffer, []byte("proxy"))
	writeTestBytes(t, buffer, []byte("checkpoint"))
	writeTestBytes(t, buffer, []byte{1, 2})
	writeTestBytes(t, buffer, []byte{3, 4})
	if err := binary.Write(buffer, binary.BigEndian, int32(7)); err != nil {
		t.Fatal(err)
	}
	if err := binary.Write(buffer, binary.BigEndian, int32(11)); err != nil {
		t.Fatal(err)
	}

	init, err := ReadInit(buffer)
	if err != nil {
		t.Fatalf("ReadInit returned error: %v", err)
	}
	if init.Mode != ModeDaemon || init.Token != "token" || init.Chain != "regtest" ||
		init.DataDir != "/tmp/mwebd" || init.PeerAddress != "peer" ||
		init.ProxyAddress != "proxy" || init.RestoreCheckpoint != "checkpoint" ||
		!bytes.Equal(init.ScanSecret, []byte{1, 2}) ||
		!bytes.Equal(init.SpendPublicKey, []byte{3, 4}) ||
		init.FromIndex != 7 || init.ToIndex != 11 {
		t.Fatalf("unexpected decoded init: %+v", init)
	}
}

func TestReadInit_wrongMagic_rejected(t *testing.T) {
	if _, err := ReadInit(bytes.NewReader(make([]byte, 8))); err == nil {
		t.Fatal("wrong frame magic must fail")
	}
}

func TestWriteReady_writesVersionAndIdentity(t *testing.T) {
	buffer := &bytes.Buffer{}
	if err := WriteReady(buffer, Ready{
		ProtocolVersion: ProtocolVersion,
		NativeVersion:   "ltcmweb/mwebd v0.1.19, mwebd-kmp 1.0.0 (abc)",
		Port:            1234,
	}); err != nil {
		t.Fatalf("WriteReady returned error: %v", err)
	}
	if !bytes.HasPrefix(buffer.Bytes(), readyMagic[:]) {
		t.Fatal("ready frame is missing magic")
	}
}

func writeTestUint32(t *testing.T, buffer *bytes.Buffer, value uint32) {
	t.Helper()
	if err := binary.Write(buffer, binary.BigEndian, value); err != nil {
		t.Fatal(err)
	}
}

func writeTestBytes(t *testing.T, buffer *bytes.Buffer, value []byte) {
	t.Helper()
	writeTestUint32(t, buffer, uint32(len(value)))
	if _, err := buffer.Write(value); err != nil {
		t.Fatal(err)
	}
}
