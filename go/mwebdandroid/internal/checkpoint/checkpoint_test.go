package checkpoint

import (
	"encoding/hex"
	"strings"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
)

func TestEncodeParse_roundTrip_preservesCheckpointData(t *testing.T) {
	data := testData()

	encoded, err := Encode(data)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Height != data.Height {
		t.Fatalf("unexpected height: %d", parsed.Height)
	}
	if parsed.BlockHeader.BlockHash() != data.BlockHeader.BlockHash() {
		t.Fatal("unexpected block header")
	}
	if parsed.FilterHeader != data.FilterHeader {
		t.Fatal("unexpected filter header")
	}
	if hex.EncodeToString(parsed.Leafset) != hex.EncodeToString(data.Leafset) {
		t.Fatal("unexpected leafset")
	}
	if parsed.OutputMMRSize != data.OutputMMRSize {
		t.Fatalf("unexpected output MMR size: %d", parsed.OutputMMRSize)
	}
}

func TestParse_invalidBlockHeaderSize_fails(t *testing.T) {
	data := testData()
	encoded, err := Encode(data)
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(encoded, "|")
	parts[2] = strings.Repeat("00", 79)

	if _, err = Parse(strings.Join(parts, "|")); err == nil {
		t.Fatal("expected invalid block header size error")
	}
}

func TestEncode_leafsetSizeMismatch_fails(t *testing.T) {
	data := testData()
	data.OutputMMRSize = 17

	if _, err := Encode(data); err == nil {
		t.Fatal("expected leafset size mismatch error")
	}
}

func testData() Data {
	return Data{
		Height: 2_900_000,
		BlockHeader: wire.BlockHeader{
			Version:    1,
			PrevBlock:  testHash(1),
			MerkleRoot: testHash(2),
			Timestamp:  time.Unix(1_700_000_000, 0),
			Bits:       0x1d00ffff,
			Nonce:      42,
		},
		FilterHeader:  testHash(3),
		Leafset:       []byte{0xff, 0x00},
		OutputMMRSize: 16,
	}
}

func testHash(seed byte) chainhash.Hash {
	var hash chainhash.Hash
	for i := range hash {
		hash[i] = seed
	}
	return hash
}
