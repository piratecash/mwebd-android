package neutrino

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/neutrino/headerfs"
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
