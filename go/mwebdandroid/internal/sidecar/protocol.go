package sidecar

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	ProtocolVersion uint32 = 1
	ModeDaemon      byte   = 0
	ModeAddresses   byte   = 1
	maxFieldSize           = 64 * 1024 * 1024
)

var (
	initMagic      = [8]byte{'M', 'W', 'E', 'B', 'I', 'N', 'I', '1'}
	readyMagic     = [8]byte{'M', 'W', 'E', 'B', 'R', 'D', 'Y', '1'}
	addressesMagic = [8]byte{'M', 'W', 'E', 'B', 'A', 'D', 'R', '1'}
)

type Init struct {
	Mode              byte
	Token             string
	Chain             string
	DataDir           string
	PeerAddress       string
	ProxyAddress      string
	RestoreCheckpoint string
	ScanSecret        []byte
	SpendPublicKey    []byte
	FromIndex         int32
	ToIndex           int32
}

type Ready struct {
	ProtocolVersion uint32
	NativeVersion   string
	Port            uint32
}

func ReadInit(reader io.Reader) (*Init, error) {
	if err := readMagic(reader, initMagic); err != nil {
		return nil, err
	}
	version, err := readUint32(reader)
	if err != nil {
		return nil, err
	}
	if version != ProtocolVersion {
		return nil, fmt.Errorf("unsupported sidecar protocol version: %d", version)
	}
	mode := []byte{0}
	if _, err = io.ReadFull(reader, mode); err != nil {
		return nil, err
	}
	if mode[0] != ModeDaemon && mode[0] != ModeAddresses {
		return nil, fmt.Errorf("unsupported sidecar mode: %d", mode[0])
	}

	init := &Init{Mode: mode[0]}
	if init.Token, err = readString(reader); err != nil {
		return nil, err
	}
	if init.Chain, err = readString(reader); err != nil {
		return nil, err
	}
	if init.DataDir, err = readString(reader); err != nil {
		return nil, err
	}
	if init.PeerAddress, err = readString(reader); err != nil {
		return nil, err
	}
	if init.ProxyAddress, err = readString(reader); err != nil {
		return nil, err
	}
	if init.RestoreCheckpoint, err = readString(reader); err != nil {
		return nil, err
	}
	if init.ScanSecret, err = readBytes(reader); err != nil {
		return nil, err
	}
	if init.SpendPublicKey, err = readBytes(reader); err != nil {
		return nil, err
	}
	if err = binary.Read(reader, binary.BigEndian, &init.FromIndex); err != nil {
		return nil, err
	}
	if err = binary.Read(reader, binary.BigEndian, &init.ToIndex); err != nil {
		return nil, err
	}
	return init, nil
}

func WriteReady(writer io.Writer, ready Ready) error {
	if _, err := writer.Write(readyMagic[:]); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.BigEndian, ready.ProtocolVersion); err != nil {
		return err
	}
	if err := writeString(writer, ready.NativeVersion); err != nil {
		return err
	}
	return binary.Write(writer, binary.BigEndian, ready.Port)
}

func WriteAddresses(writer io.Writer, addresses []string) error {
	if _, err := writer.Write(addressesMagic[:]); err != nil {
		return err
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(addresses))); err != nil {
		return err
	}
	for _, address := range addresses {
		if err := writeString(writer, address); err != nil {
			return err
		}
	}
	return nil
}

func readMagic(reader io.Reader, expected [8]byte) error {
	actual := [8]byte{}
	if _, err := io.ReadFull(reader, actual[:]); err != nil {
		return err
	}
	if actual != expected {
		return errors.New("invalid sidecar frame magic")
	}
	return nil
}

func readString(reader io.Reader) (string, error) {
	value, err := readBytes(reader)
	return string(value), err
}

func readBytes(reader io.Reader) ([]byte, error) {
	size, err := readUint32(reader)
	if err != nil {
		return nil, err
	}
	if size > maxFieldSize {
		return nil, fmt.Errorf("sidecar field is too large: %d", size)
	}
	value := make([]byte, int(size))
	_, err = io.ReadFull(reader, value)
	return value, err
}

func readUint32(reader io.Reader) (uint32, error) {
	var value uint32
	err := binary.Read(reader, binary.BigEndian, &value)
	return value, err
}

func writeString(writer io.Writer, value string) error {
	return writeBytes(writer, []byte(value))
}

func writeBytes(writer io.Writer, value []byte) error {
	if len(value) > maxFieldSize {
		return fmt.Errorf("sidecar field is too large: %d", len(value))
	}
	if err := binary.Write(writer, binary.BigEndian, uint32(len(value))); err != nil {
		return err
	}
	_, err := writer.Write(value)
	return err
}
