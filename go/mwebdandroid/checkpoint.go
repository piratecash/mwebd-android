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

	bootstrap, err := shouldBootstrapRestoreCheckpoint(dataDir, restoreCheckpoint.Height)
	if err != nil {
		return err
	}
	if !bootstrap {
		return nil
	}

	if err = removeRestoreCheckpointState(dataDir); err != nil {
		return err
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

func shouldBootstrapRestoreCheckpoint(dataDir string, checkpointHeight uint32) (bool, error) {
	headerFilesExist, err := headerfs.HeaderStoreFilesExist(dataDir)
	if err != nil {
		return false, err
	}

	dbExists, err := fileExists(filepath.Join(dataDir, "neutrino.db"))
	if err != nil {
		return false, err
	}
	if !headerFilesExist && !dbExists {
		return true, nil
	}
	if !headerFilesExist {
		return false, nil
	}
	if !dbExists {
		return true, nil
	}

	return restoreCheckpointStateBehind(dataDir, checkpointHeight)
}

func restoreCheckpointStateBehind(dataDir string, checkpointHeight uint32) (bool, error) {
	db, err := walletdb.Open(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		return false, err
	}
	defer db.Close()

	blockStore, err := headerfs.NewBlockHeaderStore(dataDir, db, nil)
	if err != nil {
		return false, err
	}
	_, blockHeight, err := blockStore.ChainTip()
	if err != nil {
		return false, err
	}

	filterStore, err := headerfs.NewFilterHeaderStore(dataDir, db, headerfs.RegularFilter, nil, nil)
	if err != nil {
		return false, err
	}
	_, filterHeight, err := filterStore.ChainTip()
	if err != nil {
		return false, err
	}

	return blockHeight < checkpointHeight || filterHeight < checkpointHeight, nil
}

func removeRestoreCheckpointState(dataDir string) error {
	if err := headerfs.RemoveHeaderStoreFiles(dataDir); err != nil {
		return err
	}

	err := os.Remove(filepath.Join(dataDir, "neutrino.db"))
	if err == nil || os.IsNotExist(err) {
		return nil
	}
	return err
}

func fileExists(path string) (bool, error) {
	_, err := os.Stat(path)
	if err == nil {
		return true, nil
	}
	if os.IsNotExist(err) {
		return false, nil
	}
	return false, err
}
