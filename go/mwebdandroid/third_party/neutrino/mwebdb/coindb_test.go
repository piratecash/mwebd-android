package mwebdb

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/ltcutil/mweb"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
)

func TestPutLeafsetAndPurge_missingCheckpointLeaf_updatesLeafsetWithoutError(t *testing.T) {
	store, closeStore := testCoinStore(t)
	defer closeStore()

	if err := store.PutLeafsetAndPurge(testLeafset(100, 16, 1, 3), []uint64{1, 3}); err != nil {
		t.Fatal(err)
	}

	leafset, err := store.GetLeafset()
	if err != nil {
		t.Fatal(err)
	}
	if leafset.Height != 100 || leafset.Size != 16 {
		t.Fatalf("unexpected leafset: height=%d size=%d", leafset.Height, leafset.Size)
	}
}

func TestPutLeafsetAndPurge_existingLeaf_deletesCoin(t *testing.T) {
	store, closeStore := testCoinStore(t)
	defer closeStore()

	coin := testCoin(2)
	if err := store.PutCoins([]*wire.MwebNetUtxo{coin}); err != nil {
		t.Fatal(err)
	}

	if err := store.PutLeafsetAndPurge(testLeafset(101, 16, 2), []uint64{2}); err != nil {
		t.Fatal(err)
	}

	coins, err := store.FetchLeaves([]uint64{2})
	if err != nil {
		t.Fatal(err)
	}
	if len(coins) != 0 {
		t.Fatalf("expected purged coin, got %d", len(coins))
	}
}

func TestPutLeafsetAndPurge_mixedKnownAndMissingLeaves_deletesKnownAndUpdatesLeafset(t *testing.T) {
	store, closeStore := testCoinStore(t)
	defer closeStore()

	coin := testCoin(2)
	if err := store.PutCoins([]*wire.MwebNetUtxo{coin}); err != nil {
		t.Fatal(err)
	}

	if err := store.PutLeafsetAndPurge(testLeafset(102, 16, 2, 7), []uint64{2, 7}); err != nil {
		t.Fatal(err)
	}

	coins, err := store.FetchLeaves([]uint64{2, 7})
	if err != nil {
		t.Fatal(err)
	}
	if len(coins) != 0 {
		t.Fatalf("expected purged known coin, got %d", len(coins))
	}

	leafset, err := store.GetLeafset()
	if err != nil {
		t.Fatal(err)
	}
	if leafset.Height != 102 || leafset.Contains(2) || leafset.Contains(7) {
		t.Fatalf("unexpected leafset after mixed purge: height=%d bits=%x", leafset.Height, leafset.Bits)
	}
}

func TestPutLeafsetAndPurge_emptyRemovedLeaves_updatesLeafset(t *testing.T) {
	store, closeStore := testCoinStore(t)
	defer closeStore()

	if err := store.PutLeafsetAndPurge(testLeafset(103, 16), nil); err != nil {
		t.Fatal(err)
	}

	leafset, err := store.GetLeafset()
	if err != nil {
		t.Fatal(err)
	}
	if leafset.Height != 103 || leafset.Size != 16 {
		t.Fatalf("unexpected leafset: height=%d size=%d", leafset.Height, leafset.Size)
	}
}

func testCoinStore(t *testing.T) (*CoinStore, func()) {
	t.Helper()

	db, err := walletdb.Create(
		"bdb", filepath.Join(t.TempDir(), "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}

	store, err := NewCoinStore(db)
	if err != nil {
		t.Fatal(err)
	}

	return store, func() {
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func testLeafset(height uint32, size uint64, spentLeaves ...uint64) *mweb.Leafset {
	bits := make([]byte, (size+7)/8)
	for i := range bits {
		bits[i] = 0xff
	}
	for _, leaf := range spentLeaves {
		bits[leaf/8] &^= 0x80 >> (leaf % 8)
	}

	return &mweb.Leafset{
		Bits:   bits,
		Size:   size,
		Height: height,
		Block:  &wire.BlockHeader{},
	}
}

func testCoin(leafIndex uint64) *wire.MwebNetUtxo {
	output := &wire.MwebOutput{}
	return &wire.MwebNetUtxo{
		Height:    1,
		LeafIndex: leafIndex,
		Output:    output,
		OutputId:  output.Hash(),
	}
}
