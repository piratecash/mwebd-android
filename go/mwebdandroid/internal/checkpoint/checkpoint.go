package checkpoint

import (
	"bytes"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/neutrino/headerfs"
)

const Version = "mweb-checkpoint-v1"

type Data struct {
	Height        uint32
	BlockHeader   wire.BlockHeader
	FilterHeader  chainhash.Hash
	Leafset       []byte
	OutputMMRSize uint64
}

func Encode(data Data) (string, error) {
	if err := validate(data.Leafset, data.OutputMMRSize); err != nil {
		return "", err
	}

	var blockHeader bytes.Buffer
	if err := data.BlockHeader.Serialize(&blockHeader); err != nil {
		return "", err
	}

	return strings.Join([]string{
		Version,
		strconv.FormatUint(uint64(data.Height), 10),
		hex.EncodeToString(blockHeader.Bytes()),
		hex.EncodeToString(data.FilterHeader[:]),
		hex.EncodeToString(data.Leafset),
		strconv.FormatUint(data.OutputMMRSize, 10),
	}, "|"), nil
}

func Parse(encoded string) (*Data, error) {
	parts := strings.Split(strings.TrimSpace(encoded), "|")
	if len(parts) != 6 || parts[0] != Version {
		return nil, fmt.Errorf("invalid restore checkpoint format")
	}

	height, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, err
	}

	blockHeaderBytes, err := decodeHex(parts[2], headerfs.BlockHeaderSize)
	if err != nil {
		return nil, err
	}
	filterHeaderBytes, err := decodeHex(parts[3], headerfs.RegularFilterHeaderSize)
	if err != nil {
		return nil, err
	}
	leafset, err := hex.DecodeString(parts[4])
	if err != nil {
		return nil, err
	}
	outputMMRSize, err := strconv.ParseUint(parts[5], 10, 64)
	if err != nil {
		return nil, err
	}
	if err = validate(leafset, outputMMRSize); err != nil {
		return nil, err
	}

	var blockHeader wire.BlockHeader
	if err = blockHeader.Deserialize(bytes.NewReader(blockHeaderBytes)); err != nil {
		return nil, err
	}
	filterHeader, err := chainhash.NewHash(filterHeaderBytes)
	if err != nil {
		return nil, err
	}

	return &Data{
		Height:        uint32(height),
		BlockHeader:   blockHeader,
		FilterHeader:  *filterHeader,
		Leafset:       leafset,
		OutputMMRSize: outputMMRSize,
	}, nil
}

func validate(leafset []byte, outputMMRSize uint64) error {
	if uint64(len(leafset)) != (outputMMRSize+7)/8 {
		return fmt.Errorf("restore checkpoint leafset length mismatch")
	}
	return nil
}

func decodeHex(value string, expectedSize uint32) ([]byte, error) {
	bytes, err := hex.DecodeString(value)
	if err != nil {
		return nil, err
	}
	if len(bytes) != int(expectedSize) {
		return nil, fmt.Errorf("invalid restore checkpoint field size")
	}
	return bytes, nil
}
