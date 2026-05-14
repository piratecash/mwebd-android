package mwebdandroid

import (
	"bytes"
	"encoding/hex"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/neutrino/headerfs"
	"github.com/ltcmweb/neutrino/mwebdb"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
	"github.com/piratecash/mwebd-android/go/mwebdandroid/internal/checkpoint"
)

func TestBootstrapRestoreCheckpoint_seedsNativeStores(t *testing.T) {
	dataDir := t.TempDir()
	checkpoint := testRestoreCheckpoint(t, 1000, 16)

	if err := bootstrapRestoreCheckpoint(dataDir, checkpoint); err != nil {
		t.Fatal(err)
	}

	db, err := walletdb.Open(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	blockStore, err := headerfs.NewBlockHeaderStore(dataDir, db, nil)
	if err != nil {
		t.Fatal(err)
	}
	blockHeader, blockHeight, err := blockStore.ChainTip()
	if err != nil {
		t.Fatal(err)
	}
	if blockHeight != 1000 {
		t.Fatalf("unexpected block height: %d", blockHeight)
	}
	expectedBlockHeader := testBlockHeader()
	if blockHeader.BlockHash() != expectedBlockHeader.BlockHash() {
		t.Fatal("unexpected block checkpoint header")
	}

	filterStore, err := headerfs.NewFilterHeaderStore(dataDir, db, headerfs.RegularFilter, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	filterHeader, filterHeight, err := filterStore.ChainTip()
	if err != nil {
		t.Fatal(err)
	}
	if filterHeight != 1000 {
		t.Fatalf("unexpected filter height: %d", filterHeight)
	}
	if *filterHeader != testFilterHeader() {
		t.Fatal("unexpected filter checkpoint header")
	}

	coinStore, err := mwebdb.NewCoinStore(db)
	if err != nil {
		t.Fatal(err)
	}
	leafset, err := coinStore.GetLeafset()
	if err != nil {
		t.Fatal(err)
	}
	if leafset.Height != 1000 || leafset.Size != 16 {
		t.Fatalf("unexpected leafset checkpoint: height=%d size=%d", leafset.Height, leafset.Size)
	}
}

func TestParseRestoreCheckpoint_leafsetSizeMismatch_fails(t *testing.T) {
	_, err := checkpoint.Parse(testRestoreCheckpoint(t, 1000, 17))
	if err == nil {
		t.Fatal("expected leafset size mismatch error")
	}
}

func TestBootstrapRestoreCheckpoint_existingDatabase_skipsBootstrap(t *testing.T) {
	dataDir := t.TempDir()
	db, err := walletdb.Create(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	if err = walletdb.Update(db, func(tx walletdb.ReadWriteTx) error {
		bucket, err := tx.CreateTopLevelBucket([]byte("sentinel"))
		if err != nil {
			return err
		}
		return bucket.Put([]byte("key"), []byte("value"))
	}); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}

	if err = bootstrapRestoreCheckpoint(dataDir, testRestoreCheckpoint(t, 1000, 16)); err != nil {
		t.Fatal(err)
	}

	db, err = walletdb.Open(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	err = walletdb.View(db, func(tx walletdb.ReadTx) error {
		value := tx.ReadBucket([]byte("sentinel")).Get([]byte("key"))
		if !bytes.Equal(value, []byte("value")) {
			t.Fatalf("unexpected sentinel value: %x", value)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func testRestoreCheckpoint(t *testing.T, height uint32, outputMMRSize uint64) string {
	t.Helper()

	leafset := []byte{0xff, 0x00}
	if outputMMRSize == 17 {
		return strings.Join([]string{
			checkpoint.Version,
			strconv.FormatUint(uint64(height), 10),
			strings.Repeat("00", 80),
			strings.Repeat("00", 32),
			hex.EncodeToString(leafset),
			strconv.FormatUint(outputMMRSize, 10),
		}, "|")
	}

	encoded, err := checkpoint.Encode(checkpoint.Data{
		Height:        height,
		BlockHeader:   testBlockHeader(),
		FilterHeader:  testFilterHeader(),
		Leafset:       leafset,
		OutputMMRSize: outputMMRSize,
	})
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func testBlockHeader() wire.BlockHeader {
	return wire.BlockHeader{
		Version:    1,
		PrevBlock:  testHash(1),
		MerkleRoot: testHash(2),
		Timestamp:  time.Unix(1_700_000_000, 0),
		Bits:       0x1d00ffff,
		Nonce:      42,
	}
}

func testFilterHeader() chainhash.Hash {
	return testHash(3)
}

func testHash(seed byte) chainhash.Hash {
	var hash chainhash.Hash
	for i := range hash {
		hash[i] = seed
	}
	return hash
}
