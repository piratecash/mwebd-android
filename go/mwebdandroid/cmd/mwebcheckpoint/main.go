package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/neutrino/headerfs"
	"github.com/ltcmweb/neutrino/mwebdb"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
	"github.com/piratecash/mwebd-android/go/mwebdandroid/internal/checkpoint"
)

func main() {
	dataDir := flag.String("data-dir", "", "mwebd/neutrino data directory")
	peerAddress := flag.String("peer", "", "Litecoin peer address for network generation")
	explorerURL := flag.String("explorer-url", "", "MWEB Explorer URL for historical replay height indexing")
	replayURL := flag.String("replay-url", "https://lexp.feeme.com", "btc-rpc-explorer compatible URL for historical MWEB block replay")
	explorerCacheDir := flag.String("explorer-cache-dir", "", "optional historical replay cache directory")
	networkName := flag.String("network", "mainnet", "network: mainnet or testnet4")
	heightsValue := flag.String("heights", "", "comma-separated checkpoint heights")
	startHeight := flag.Uint("start", 0, "first checkpoint height for interval generation")
	endHeight := flag.Uint("end", 0, "last checkpoint height for interval generation")
	checkpointInterval := flag.Uint("checkpoint-interval", 50_000, "checkpoint height interval")
	legacyInterval := flag.Uint("interval", 0, "deprecated alias for -checkpoint-interval")
	height := flag.Uint("height", 0, "checkpoint height; defaults to the stored leafset height")
	outputPath := flag.String("out", "", "optional output file")
	timeout := flag.Duration("timeout", 30*time.Second, "network request timeout")
	checkPeer := flag.Bool("check-peer", false, "check that -peer supports required checkpoint services")
	watch := flag.Bool("watch", false, "watch a local data directory and append new checkpoints to -out")
	watchMinStep := flag.Uint("watch-min-step", 50_000, "minimum height step between local watch checkpoints")
	watchInterval := flag.Duration("watch-interval", 10*time.Minute, "local watch polling interval")
	safeDepth := flag.Uint("safe-depth", 8064, "depth used when resolving the safe tip with -heights tip")
	flag.Parse()

	interval := uint32(*checkpointInterval)
	if *legacyInterval != 0 {
		interval = uint32(*legacyInterval)
	}
	resolveHeights := func() ([]uint32, error) {
		return resolveNetworkHeights(*heightsValue, uint32(*startHeight), uint32(*endHeight), interval)
	}

	if *checkPeer {
		if *peerAddress == "" {
			exitWithError(errors.New("-check-peer requires -peer"))
		}
		if err := checkNetworkPeer(*networkName, *peerAddress, *timeout); err != nil {
			exitWithError(err)
		}
		fmt.Println("peer ok")
		return
	}

	if *watch {
		if *outputPath == "" {
			exitWithError(errors.New("-watch requires -out"))
		}
		if *dataDir == "" {
			exitWithError(errors.New("-watch requires -data-dir"))
		}
		ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer stop()
		err := watchLocalCheckpoints(ctx, *dataDir, *outputPath, uint32(*watchMinStep), *watchInterval)
		if err != nil {
			exitWithError(err)
		}
		return
	}

	var (
		lines []string
		err   error
	)
	switch {
	case *explorerURL != "":
		if *peerAddress == "" {
			exitWithError(errors.New("-explorer-url requires -peer for block and filter headers"))
		}
		var heights []uint32
		heights, err = resolveHeights()
		if err != nil {
			exitWithError(err)
		}
		lines, err = generateExplorerPeerCheckpoints(
			*networkName,
			*peerAddress,
			*explorerURL,
			*replayURL,
			*explorerCacheDir,
			heights,
			interval,
			uint32(*safeDepth),
			*timeout,
		)
	case *peerAddress != "" && *dataDir != "":
		var heights []uint32
		heights, err = resolveHeights()
		if err != nil {
			exitWithError(err)
		}
		lines, err = generateHybridCheckpoints(*networkName, *dataDir, *peerAddress, heights, *timeout)
	case *peerAddress != "":
		var heights []uint32
		heights, err = resolveHeights()
		if err != nil {
			exitWithError(err)
		}
		lines, err = generateNetworkCheckpoints(*networkName, *peerAddress, heights, *timeout)
	case *dataDir != "":
		line, exportErr := exportCheckpoint(*dataDir, uint32(*height))
		if exportErr != nil {
			err = exportErr
		} else {
			lines = []string{line}
		}
	default:
		exitWithError(errors.New("missing -peer or -data-dir"))
	}
	if err != nil {
		exitWithError(err)
	}

	if *outputPath == "" {
		for _, line := range lines {
			fmt.Println(line)
		}
		return
	}

	if err = os.WriteFile(*outputPath, []byte(joinLines(lines)), 0644); err != nil {
		exitWithError(err)
	}
}

func exportCheckpoint(dataDir string, wantedHeight uint32) (string, error) {
	db, err := walletdb.Open(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		return "", err
	}
	defer db.Close()

	coinStore, err := mwebdb.NewCoinStore(db)
	if err != nil {
		return "", err
	}
	leafset, err := coinStore.GetLeafset()
	if err != nil {
		return "", err
	}
	if leafset.Height == 0 {
		return "", errors.New("MWEB leafset is missing")
	}

	height := leafset.Height
	if wantedHeight != 0 && wantedHeight != height {
		return "", fmt.Errorf(
			"stored MWEB leafset is at height %d, not requested height %d",
			height,
			wantedHeight,
		)
	}

	leavesAtHeight, err := coinStore.GetLeavesAtHeight()
	if err != nil {
		return "", err
	}
	if leaves := leavesAtHeight[height]; leaves != leafset.Size {
		return "", fmt.Errorf(
			"leafset size mismatch at height %d: leafset=%d index=%d",
			height,
			leafset.Size,
			leaves,
		)
	}

	inputs, err := readLocalCheckpointInputs(dataDir, db, []uint32{height})
	if err != nil {
		return "", err
	}
	input := inputs[0]
	if leafset.Block != nil && leafset.Block.BlockHash() != input.blockHeader.BlockHash() {
		return "", errors.New("leafset block header does not match block store")
	}

	return checkpoint.Encode(checkpoint.Data{
		Height:        height,
		BlockHeader:   input.blockHeader,
		FilterHeader:  input.filterHeader,
		Leafset:       leafset.Bits,
		OutputMMRSize: leafset.Size,
	})
}

func generateHybridCheckpoints(
	networkName string,
	dataDir string,
	peerAddress string,
	heights []uint32,
	timeout time.Duration,
) ([]string, error) {
	params, err := networkParams(networkName)
	if err != nil {
		return nil, err
	}
	inputs, err := loadLocalCheckpointInputs(dataDir, heights)
	if err != nil {
		return nil, err
	}
	client, err := newNetworkClient(peerAddress, params, timeout)
	if err != nil {
		return nil, err
	}
	defer client.close()

	lines := make([]string, 0, len(inputs))
	for _, input := range inputs {
		mwebData, err := client.fetchMwebData(input.blockHeader.BlockHash(), input.height, timeout)
		if err != nil {
			return nil, err
		}
		line, err := checkpoint.Encode(checkpoint.Data{
			Height:        input.height,
			BlockHeader:   input.blockHeader,
			FilterHeader:  input.filterHeader,
			Leafset:       mwebData.leafset,
			OutputMMRSize: mwebData.outputMMRSize,
		})
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}

	return lines, nil
}

type localCheckpointInput struct {
	height       uint32
	blockHeader  wire.BlockHeader
	filterHeader chainhash.Hash
}

func loadLocalCheckpointInputs(dataDir string, heights []uint32) ([]localCheckpointInput, error) {
	db, err := walletdb.Open(
		"bdb", filepath.Join(dataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		return nil, err
	}
	defer db.Close()

	return readLocalCheckpointInputs(dataDir, db, heights)
}

func readLocalCheckpointInputs(
	dataDir string,
	db walletdb.DB,
	heights []uint32,
) ([]localCheckpointInput, error) {
	if len(heights) == 0 {
		return nil, errors.New("no checkpoint heights requested")
	}

	blockStore, err := headerfs.NewBlockHeaderStore(dataDir, db, nil)
	if err != nil {
		return nil, err
	}
	filterStore, err := headerfs.NewFilterHeaderStore(
		dataDir,
		db,
		headerfs.RegularFilter,
		nil,
		nil,
	)
	if err != nil {
		return nil, err
	}
	if len(heights) == 1 && heights[0] == 0 {
		_, height, err := blockStore.ChainTip()
		if err != nil {
			return nil, err
		}
		heights = []uint32{height}
	}

	inputs := make([]localCheckpointInput, 0, len(heights))
	for _, height := range heights {
		blockHeader, err := blockStore.FetchHeaderByHeight(height)
		if err != nil {
			return nil, err
		}
		filterHeader, err := filterStore.FetchHeaderByHeight(height)
		if err != nil {
			return nil, err
		}
		inputs = append(inputs, localCheckpointInput{
			height:       height,
			blockHeader:  *blockHeader,
			filterHeader: *filterHeader,
		})
	}

	return inputs, nil
}

func exitWithError(err error) {
	_, _ = fmt.Fprintf(os.Stderr, "mwebcheckpoint: %v\n", err)
	os.Exit(1)
}

func joinLines(lines []string) string {
	if len(lines) == 0 {
		return ""
	}

	var output string
	for _, line := range lines {
		output += line + "\n"
	}
	return output
}
