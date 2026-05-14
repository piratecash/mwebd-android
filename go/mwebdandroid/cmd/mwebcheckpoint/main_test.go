package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/ltcutil/mweb"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/neutrino/headerfs"
	"github.com/ltcmweb/neutrino/mwebdb"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
	"github.com/piratecash/mwebd-android/go/mwebdandroid/internal/checkpoint"
)

func TestExportCheckpoint_seededNativeStores_returnsCheckpointLine(t *testing.T) {
	dataDir := t.TempDir()
	expected := seedCheckpointStores(t, dataDir, 2_900_000, 16)

	encoded, err := exportCheckpoint(dataDir, 0)
	if err != nil {
		t.Fatal(err)
	}

	parsed, err := checkpoint.Parse(encoded)
	if err != nil {
		t.Fatal(err)
	}

	if parsed.Height != expected.Height {
		t.Fatalf("unexpected checkpoint height: %d", parsed.Height)
	}
	if parsed.BlockHeader.BlockHash() != expected.BlockHeader.BlockHash() {
		t.Fatal("unexpected block header")
	}
	if parsed.FilterHeader != expected.FilterHeader {
		t.Fatal("unexpected filter header")
	}
	if parsed.OutputMMRSize != expected.OutputMMRSize {
		t.Fatalf("unexpected output MMR size: %d", parsed.OutputMMRSize)
	}
}

func TestExportCheckpoint_requestedHeightMismatch_fails(t *testing.T) {
	dataDir := t.TempDir()
	seedCheckpointStores(t, dataDir, 2_900_000, 16)

	_, err := exportCheckpoint(dataDir, 2_950_000)
	if err == nil {
		t.Fatal("expected requested height mismatch error")
	}
}

func TestLoadLocalCheckpointInputs_tipSentinel_usesLocalHeaderTip(t *testing.T) {
	dataDir := t.TempDir()
	expected := seedCheckpointStores(t, dataDir, 2_900_000, 16)

	inputs, err := loadLocalCheckpointInputs(dataDir, []uint32{0})
	if err != nil {
		t.Fatal(err)
	}

	if len(inputs) != 1 {
		t.Fatalf("unexpected inputs count: %d", len(inputs))
	}
	input := inputs[0]
	if input.height != expected.Height {
		t.Fatalf("unexpected checkpoint height: %d", input.height)
	}
	if input.blockHeader.BlockHash() != expected.BlockHeader.BlockHash() {
		t.Fatal("unexpected block header")
	}
	if input.filterHeader != expected.FilterHeader {
		t.Fatal("unexpected filter header")
	}
}

func TestArchiveLocalCheckpoint_newCheckpoint_appendsLine(t *testing.T) {
	dataDir := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "MainNetLitecoin-mweb.checkpoint")
	seedCheckpointStores(t, dataDir, 2_900_000, 16)

	appended, err := archiveLocalCheckpoint(dataDir, outputPath, 50_000)
	if err != nil {
		t.Fatal(err)
	}
	if !appended {
		t.Fatal("expected checkpoint append")
	}

	lines, err := readCheckpointLines(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 {
		t.Fatalf("unexpected line count: %d", len(lines))
	}
}

func TestArchiveLocalCheckpoint_recentCheckpoint_skipsDuplicate(t *testing.T) {
	dataDir := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "MainNetLitecoin-mweb.checkpoint")
	seedCheckpointStores(t, dataDir, 2_900_000, 16)

	if _, err := archiveLocalCheckpoint(dataDir, outputPath, 50_000); err != nil {
		t.Fatal(err)
	}
	appended, err := archiveLocalCheckpoint(dataDir, outputPath, 50_000)
	if err != nil {
		t.Fatal(err)
	}
	if appended {
		t.Fatal("expected duplicate checkpoint skip")
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if lines := splitNonEmptyLines(string(content)); len(lines) != 1 {
		t.Fatalf("unexpected line count: %d", len(lines))
	}
}

func TestArchiveLocalCheckpoint_corruptOutputFile_returnsErrorWithoutRewrite(t *testing.T) {
	dataDir := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "MainNetLitecoin-mweb.checkpoint")
	seedCheckpointStores(t, dataDir, 2_900_000, 16)
	if err := os.WriteFile(outputPath, []byte("corrupt\n"), 0644); err != nil {
		t.Fatal(err)
	}

	appended, err := archiveLocalCheckpoint(dataDir, outputPath, 50_000)
	if err == nil {
		t.Fatal("expected corrupt checkpoint error")
	}
	if appended {
		t.Fatal("expected no append")
	}

	content, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "corrupt\n" {
		t.Fatalf("unexpected rewritten content: %q", string(content))
	}
}

func seedCheckpointStores(
	t *testing.T,
	dataDir string,
	height uint32,
	outputMMRSize uint64,
) checkpoint.Data {
	t.Helper()

	db, err := walletdb.Create(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	blockHeader := testBlockHeader()
	filterHeader := testHash(3)
	data := checkpoint.Data{
		Height:        height,
		BlockHeader:   blockHeader,
		FilterHeader:  filterHeader,
		Leafset:       []byte{0xff, 0x00},
		OutputMMRSize: outputMMRSize,
	}

	if err = headerfs.BootstrapBlockHeaderStore(dataDir, db, headerfs.BlockHeader{
		BlockHeader: &blockHeader,
		Height:      height,
	}); err != nil {
		t.Fatal(err)
	}

	if err = headerfs.BootstrapFilterHeaderStore(dataDir, db, headerfs.FilterHeader{
		HeaderHash: blockHeader.BlockHash(),
		FilterHash: filterHeader,
		Height:     height,
	}); err != nil {
		t.Fatal(err)
	}

	coinStore, err := mwebdb.NewCoinStore(db)
	if err != nil {
		t.Fatal(err)
	}
	if err = coinStore.PutLeavesAtHeight(map[uint32]uint64{
		height: outputMMRSize,
	}); err != nil {
		t.Fatal(err)
	}
	if err = coinStore.PutLeafsetAndPurge(&mweb.Leafset{
		Bits:   data.Leafset,
		Size:   outputMMRSize,
		Height: height,
		Block:  &blockHeader,
	}, nil); err != nil {
		t.Fatal(err)
	}

	return data
}

func splitNonEmptyLines(value string) []string {
	var lines []string
	for _, line := range strings.Split(value, "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
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

func testHash(seed byte) chainhash.Hash {
	var hash chainhash.Hash
	for i := range hash {
		hash[i] = seed
	}
	return hash
}
