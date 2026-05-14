package headerfs

import (
	"bytes"
	"os"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcsuite/ltcwallet/walletdb"
)

// BootstrapBlockHeaderStore seeds the block header store with a trusted
// checkpoint before the normal append-only store opens.
func BootstrapBlockHeaderStore(dataDir string, db walletdb.DB, checkpoint BlockHeader) error {
	var raw bytes.Buffer
	if err := checkpoint.Serialize(&raw); err != nil {
		return err
	}

	return bootstrapHeaderStore(
		dataDir,
		db,
		Block,
		BlockHeaderSize,
		checkpoint.Height,
		checkpoint.BlockHash(),
		raw.Bytes(),
	)
}

// BootstrapFilterHeaderStore seeds the filter header store with a trusted
// checkpoint before the normal append-only store opens.
func BootstrapFilterHeaderStore(dataDir string, db walletdb.DB, checkpoint FilterHeader) error {
	return bootstrapHeaderStore(
		dataDir,
		db,
		RegularFilter,
		RegularFilterHeaderSize,
		checkpoint.Height,
		checkpoint.HeaderHash,
		checkpoint.FilterHash[:],
	)
}

func bootstrapHeaderStore(
	dataDir string,
	db walletdb.DB,
	hType HeaderType,
	headerSize uint32,
	height uint32,
	hash chainhash.Hash,
	rawHeader []byte,
) error {
	if err := os.MkdirAll(dataDir, 0755); err != nil {
		return err
	}

	name, err := flatFileName(dataDir, hType)
	if err != nil {
		return err
	}

	file, err := os.OpenFile(name, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return err
	}
	defer file.Close()

	if _, err = file.WriteAt(rawHeader, 0); err != nil {
		return err
	}
	if err = file.Truncate(int64(headerSize)); err != nil {
		return err
	}

	index, err := newHeaderIndex(db, hType)
	if err != nil {
		return err
	}

	return index.addHeaders(headerBatch{{
		hash:   hash,
		height: height,
	}})
}

func HeaderStoreFilesExist(dataDir string) (bool, error) {
	for _, hType := range []HeaderType{Block, RegularFilter} {
		name, err := flatFileName(dataDir, hType)
		if err != nil {
			return false, err
		}
		if _, err = os.Stat(name); err == nil {
			return true, nil
		} else if !os.IsNotExist(err) {
			return false, err
		}
	}

	return false, nil
}

func RemoveHeaderStoreFiles(dataDir string) error {
	for _, hType := range []HeaderType{Block, RegularFilter} {
		name, err := flatFileName(dataDir, hType)
		if err != nil {
			return err
		}
		if err = os.Remove(name); err != nil && !os.IsNotExist(err) {
			return err
		}
	}

	return nil
}
