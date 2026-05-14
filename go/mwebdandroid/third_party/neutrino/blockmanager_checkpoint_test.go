package neutrino

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/blockchain"
	"github.com/ltcmweb/ltcd/chaincfg"
	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/neutrino/headerfs"
	"github.com/ltcmweb/neutrino/headerlist"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
)

func TestCheckCFCheckptSanity_sparseFilterStore_skipsCheckpointsBelowBaseHeight(t *testing.T) {
	dataDir := t.TempDir()
	db, err := walletdb.Create("bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	filterHeader := testNeutrinoHash(1)
	if err = headerfs.BootstrapFilterHeaderStore(dataDir, db, headerfs.FilterHeader{
		HeaderHash: testNeutrinoHash(2),
		FilterHash: filterHeader,
		Height:     125_000,
	}); err != nil {
		t.Fatal(err)
	}
	filterStore, err := headerfs.NewFilterHeaderStore(dataDir, db, headerfs.RegularFilter, nil, nil)
	if err != nil {
		t.Fatal(err)
	}

	heightDiff, err := checkCFCheckptSanity(map[string][]*chainhash.Hash{
		"peer-a": {
			testNeutrinoHashPtr(3),
			testNeutrinoHashPtr(4),
			testNeutrinoHashPtr(5),
		},
		"peer-b": {
			testNeutrinoHashPtr(3),
			testNeutrinoHashPtr(4),
			testNeutrinoHashPtr(5),
		},
	}, filterStore)
	if err != nil {
		t.Fatal(err)
	}
	if heightDiff != -1 {
		t.Fatalf("unexpected checkpoint diff: %d", heightDiff)
	}
}

func TestCheckHeaderSanity_sparseCheckpointFirstRetarget_passes(t *testing.T) {
	const (
		checkpointHeight = 3_048_192
		prevNodeHeight   = 3_050_207
	)

	prevHeader := testValidPowHeader(
		chaincfg.SimNetParams.PowLimitBits, time.Unix(1_700_000_000, 0),
	)
	nextHeader := testValidPowHeader(
		chaincfg.SimNetParams.PowLimitBits,
		prevHeader.Timestamp.Add(chaincfg.SimNetParams.TargetTimePerBlock),
	)

	blockHeaders := newMockBlockHeaderStore()
	blockHeaders.firstKnownHeight = checkpointHeight
	manager := testBlockManager(chaincfg.SimNetParams, blockHeaders)

	err := manager.checkHeaderSanity(nextHeader, false, prevNodeHeight, prevHeader)
	if err != nil {
		t.Fatal(err)
	}
}

func TestCheckHeaderSanity_sparseCheckpointFirstRetarget_rejectsInvalidPow(t *testing.T) {
	const (
		checkpointHeight = 3_048_192
		prevNodeHeight   = 3_050_207
	)

	prevHeader := testValidPowHeader(
		chaincfg.SimNetParams.PowLimitBits, time.Unix(1_700_000_000, 0),
	)
	nextHeader := &wire.BlockHeader{
		Version:   4,
		Bits:      0x01010000,
		Timestamp: prevHeader.Timestamp.Add(chaincfg.SimNetParams.TargetTimePerBlock),
	}

	blockHeaders := newMockBlockHeaderStore()
	blockHeaders.firstKnownHeight = checkpointHeight
	manager := testBlockManager(chaincfg.SimNetParams, blockHeaders)

	err := manager.checkHeaderSanity(nextHeader, false, prevNodeHeight, prevHeader)
	if err == nil {
		t.Fatal("expected invalid proof-of-work error")
	}
}

func TestHeaderSanityFlags_noRetargeting_returnsNoFlags(t *testing.T) {
	manager := &blockManager{
		cfg: &blockManagerCfg{
			ChainParams: chaincfg.RegressionNetParams,
		},
		blocksPerRetarget: 2016,
	}

	flags := manager.headerSanityFlags(2015)
	if flags != blockchain.BFNone {
		t.Fatalf("expected no flags, got %v", flags)
	}
}

func TestSparseCheckpointFastAddRequired(t *testing.T) {
	tests := []struct {
		name              string
		prevNodeHeight    int32
		firstKnownHeight  uint32
		blocksPerRetarget int32
		expected          bool
	}{
		{
			name:              "missing retarget ancestor below checkpoint",
			prevNodeHeight:    3_050_207,
			firstKnownHeight:  3_048_192,
			blocksPerRetarget: 2016,
			expected:          true,
		},
		{
			name:              "retarget ancestor is available",
			prevNodeHeight:    3_050_207,
			firstKnownHeight:  3_048_191,
			blocksPerRetarget: 2016,
			expected:          false,
		},
		{
			name:              "not a retarget boundary",
			prevNodeHeight:    3_050_206,
			firstKnownHeight:  3_048_192,
			blocksPerRetarget: 2016,
			expected:          false,
		},
		{
			name:              "invalid retarget interval",
			prevNodeHeight:    3_050_207,
			firstKnownHeight:  3_048_192,
			blocksPerRetarget: 0,
			expected:          false,
		},
		{
			name:              "first chain retarget missing genesis ancestor",
			prevNodeHeight:    2015,
			firstKnownHeight:  1,
			blocksPerRetarget: 2016,
			expected:          true,
		},
		{
			name:              "first chain retarget synced from genesis",
			prevNodeHeight:    2015,
			firstKnownHeight:  0,
			blocksPerRetarget: 2016,
			expected:          false,
		},
		{
			name:              "after genesis is not retarget boundary",
			prevNodeHeight:    0,
			firstKnownHeight:  1,
			blocksPerRetarget: 2016,
			expected:          false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			actual := sparseCheckpointFastAddRequired(
				test.prevNodeHeight, test.firstKnownHeight,
				test.blocksPerRetarget,
			)
			if actual != test.expected {
				t.Fatalf("expected %v, got %v", test.expected, actual)
			}
		})
	}
}

func testBlockManager(params chaincfg.Params,
	blockHeaders headerfs.BlockHeaderStore) *blockManager {

	targetTimespan := int64(params.TargetTimespan / time.Second)
	targetTimePerBlock := int64(params.TargetTimePerBlock / time.Second)

	return &blockManager{
		cfg: &blockManagerCfg{
			ChainParams:  params,
			BlockHeaders: blockHeaders,
			TimeSource:   blockchain.NewMedianTime(),
		},
		headerList:          headerlist.NewBoundedMemoryChain(numMaxMemHeaders),
		blocksPerRetarget:   int32(targetTimespan / targetTimePerBlock),
		minRetargetTimespan: targetTimespan / params.RetargetAdjustmentFactor,
		maxRetargetTimespan: targetTimespan * params.RetargetAdjustmentFactor,
	}
}

func testNeutrinoHash(seed byte) chainhash.Hash {
	var hash chainhash.Hash
	for i := range hash {
		hash[i] = seed
	}
	return hash
}

func testNeutrinoHashPtr(seed byte) *chainhash.Hash {
	hash := testNeutrinoHash(seed)
	return &hash
}

func testValidPowHeader(bits uint32, timestamp time.Time) *wire.BlockHeader {
	header := &wire.BlockHeader{
		Version:   4,
		Bits:      bits,
		Timestamp: timestamp,
	}
	target := blockchain.CompactToBig(bits)
	for {
		hash := header.PowHash()
		if blockchain.HashToBig(&hash).Cmp(target) <= 0 {
			return header
		}
		header.Nonce++
	}
}
