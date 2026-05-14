package mwebdandroid

import (
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ltcmweb/ltcd/ltcutil/mweb"
	"github.com/ltcmweb/neutrino/headerfs"
	"github.com/ltcmweb/neutrino/mwebdb"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
	"github.com/piratecash/mwebd-android/go/mwebdandroid/internal/checkpoint"
)

func bootstrapRestoreCheckpoint(dataDir, encodedCheckpoint string) error {
	if strings.TrimSpace(encodedCheckpoint) == "" {
		return nil
	}

	restoreCheckpoint, err := checkpoint.Parse(encodedCheckpoint)
	if err != nil {
		return err
	}

	exists, err := restoreCheckpointStateExists(dataDir)
	if err != nil {
		return err
	}
	if exists {
		return nil
	}

	if err = os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	db, err := walletdb.Create(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		return err
	}
	defer db.Close()

	if err = headerfs.BootstrapBlockHeaderStore(dataDir, db, headerfs.BlockHeader{
		BlockHeader: &restoreCheckpoint.BlockHeader,
		Height:      restoreCheckpoint.Height,
	}); err != nil {
		return err
	}

	blockHash := restoreCheckpoint.BlockHeader.BlockHash()
	if err = headerfs.BootstrapFilterHeaderStore(dataDir, db, headerfs.FilterHeader{
		HeaderHash: blockHash,
		FilterHash: restoreCheckpoint.FilterHeader,
		Height:     restoreCheckpoint.Height,
	}); err != nil {
		return err
	}

	coinStore, err := mwebdb.NewCoinStore(db)
	if err != nil {
		return err
	}
	if err = coinStore.PutLeavesAtHeight(map[uint32]uint64{
		restoreCheckpoint.Height: restoreCheckpoint.OutputMMRSize,
	}); err != nil {
		return err
	}

	return coinStore.PutLeafsetAndPurge(&mweb.Leafset{
		Bits:   restoreCheckpoint.Leafset,
		Size:   restoreCheckpoint.OutputMMRSize,
		Height: restoreCheckpoint.Height,
		Block:  &restoreCheckpoint.BlockHeader,
	}, nil)
}

func restoreCheckpointStateExists(dataDir string) (bool, error) {
	if exists, err := headerfs.HeaderStoreFilesExist(dataDir); err != nil || exists {
		return exists, err
	}

	_, err := os.Stat(filepath.Join(dataDir, "neutrino.db"))
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
