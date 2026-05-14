package headerfs

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
)

func TestBootstrapHeaderStores_useCheckpointAsBaseHeight(t *testing.T) {
	dataDir := t.TempDir()
	db, err := walletdb.Create("bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	blockHeader := testHeaderfsBlockHeader(1)
	filterHeader := testHeaderfsHash(2)
	if err = BootstrapBlockHeaderStore(dataDir, db, BlockHeader{
		BlockHeader: &blockHeader,
		Height:      1000,
	}); err != nil {
		t.Fatal(err)
	}
	if err = BootstrapFilterHeaderStore(dataDir, db, FilterHeader{
		HeaderHash: blockHeader.BlockHash(),
		FilterHash: filterHeader,
		Height:     1000,
	}); err != nil {
		t.Fatal(err)
	}

	assertFileSize(t, filepath.Join(dataDir, "block_headers.bin"), BlockHeaderSize)
	assertFileSize(t, filepath.Join(dataDir, "reg_filter_headers.bin"), RegularFilterHeaderSize)

	blockStore, err := NewBlockHeaderStore(dataDir, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = blockStore.FetchHeaderByHeight(999); err == nil {
		t.Fatal("expected missing header below checkpoint")
	}
	locator, err := blockStore.LatestBlockLocator()
	if err != nil {
		t.Fatal(err)
	}
	if len(locator) != 1 {
		t.Fatalf("unexpected locator length: %d", len(locator))
	}

	filterStore, err := NewFilterHeaderStore(dataDir, db, RegularFilter, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = filterStore.FetchHeaderByHeight(0); err == nil {
		t.Fatal("expected missing filter header below checkpoint")
	}
}

func TestBootstrapBlockHeaderStore_appendsAfterCheckpointBase(t *testing.T) {
	dataDir := t.TempDir()
	db, err := walletdb.Create("bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	checkpointHeader := testHeaderfsBlockHeader(1)
	nextHeader := testHeaderfsBlockHeader(2)
	nextHeader.PrevBlock = checkpointHeader.BlockHash()
	if err = BootstrapBlockHeaderStore(dataDir, db, BlockHeader{
		BlockHeader: &checkpointHeader,
		Height:      1000,
	}); err != nil {
		t.Fatal(err)
	}
	blockStore, err := NewBlockHeaderStore(dataDir, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err = blockStore.WriteHeaders(BlockHeader{
		BlockHeader: &nextHeader,
		Height:      1001,
	}); err != nil {
		t.Fatal(err)
	}

	assertFileSize(t, filepath.Join(dataDir, "block_headers.bin"), 2*BlockHeaderSize)
	ancestors, startHeight, err := blockStore.FetchHeaderAncestors(1, headerHashPtr(nextHeader.BlockHash()))
	if err != nil {
		t.Fatal(err)
	}
	if startHeight != 1000 || len(ancestors) != 2 {
		t.Fatalf("unexpected ancestors: start=%d count=%d", startHeight, len(ancestors))
	}
}

func assertFileSize(t *testing.T, path string, expected uint32) {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Size() != int64(expected) {
		t.Fatalf("unexpected size for %s: %d", path, info.Size())
	}
}

func testHeaderfsBlockHeader(seed byte) wire.BlockHeader {
	return wire.BlockHeader{
		Version:    1,
		PrevBlock:  testHeaderfsHash(seed),
		MerkleRoot: testHeaderfsHash(seed + 1),
		Timestamp:  time.Unix(1_700_000_000+int64(seed), 0),
		Bits:       0x1d00ffff,
		Nonce:      uint32(seed),
	}
}

func testHeaderfsHash(seed byte) chainhash.Hash {
	var hash chainhash.Hash
	for i := range hash {
		hash[i] = seed
	}
	return hash
}

func headerHashPtr(hash chainhash.Hash) *chainhash.Hash {
	return &hash
}
