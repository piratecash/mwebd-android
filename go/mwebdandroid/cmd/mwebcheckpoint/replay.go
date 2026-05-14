package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
	"lukechampine.com/blake3"
)

const replayBlockFetchers = 8

type mwebReplayClient struct {
	baseURL     string
	cacheDir    string
	networkName string
	httpClient  *http.Client
}

type mwebReplayBlock struct {
	Height        uint32   `json:"height"`
	Hash          string   `json:"hash"`
	Version       int32    `json:"version"`
	PrevBlock     string   `json:"previousblockhash"`
	MerkleRoot    string   `json:"merkleroot"`
	Timestamp     int64    `json:"time"`
	Bits          string   `json:"bits"`
	Nonce         uint32   `json:"nonce"`
	OutputMMRSize uint64   `json:"num_txos"`
	LeafRoot      string   `json:"leaf_root"`
	Inputs        []string `json:"inputs"`
	Outputs       []string `json:"outputs"`
}

type rpcExplorerBlock struct {
	Height     uint32          `json:"height"`
	Hash       string          `json:"hash"`
	Version    int32           `json:"version"`
	PrevBlock  string          `json:"previousblockhash"`
	MerkleRoot string          `json:"merkleroot"`
	Timestamp  int64           `json:"time"`
	Bits       string          `json:"bits"`
	Nonce      uint32          `json:"nonce"`
	Mweb       rpcExplorerMweb `json:"mweb"`
}

type rpcExplorerMweb struct {
	NumTxos  uint64   `json:"num_txos"`
	LeafRoot string   `json:"leaf_root"`
	Inputs   []string `json:"inputs"`
	Outputs  []string `json:"outputs"`
}

type checkpointSnapshot struct {
	height        uint32
	blockHeader   wire.BlockHeader
	leafset       []byte
	outputMMRSize uint64
}

func newMwebReplayClient(baseURL string, cacheDir string, networkName string, timeout time.Duration) *mwebReplayClient {
	return &mwebReplayClient{
		baseURL:     strings.TrimRight(baseURL, "/"),
		cacheDir:    cacheDir,
		networkName: networkName,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func replayMwebBlocks(
	client *mwebReplayClient,
	checkpointHeights []uint32,
	nonEmptyHeights []uint32,
) ([]checkpointSnapshot, error) {
	replayer := newMwebLeafsetReplayer()
	checkpointSet := heightSet(checkpointHeights)
	nonEmptySet := heightSet(nonEmptyHeights)
	eventHeights := sortedUniqueHeights(append(checkpointHeights, nonEmptyHeights...))
	blockFetcher := newReplayBlockFetcher(client, eventHeights)
	defer blockFetcher.close()
	snapshots := make([]checkpointSnapshot, 0, len(checkpointHeights))
	lastAppliedHeight := uint32(0)

	for index, height := range eventHeights {
		if index == 0 || index%1000 == 0 {
			fmt.Fprintf(os.Stderr, "replaying MWEB blocks: %d/%d\n", index+1, len(eventHeights))
		}

		block, err := blockFetcher.block(height)
		if err != nil {
			return nil, err
		}
		if _, ok := nonEmptySet[height]; ok {
			before := replayer.clone()
			if err = replayer.apply(height, *block); err == nil {
				err = replayer.verifyBlockState(*block)
			}
			if err != nil {
				recovered := before
				if recoverErr := recoverMissingReplayGap(client, recovered, lastAppliedHeight+1, height-1); recoverErr != nil {
					return nil, fmt.Errorf("height %d: %w; gap recovery failed: %v", height, err, recoverErr)
				}
				if err = recovered.apply(height, *block); err != nil {
					return nil, fmt.Errorf("height %d: %w", height, err)
				}
				if err = recovered.verifyBlockState(*block); err != nil {
					return nil, fmt.Errorf("height %d: %w", height, err)
				}
				replayer = recovered
			}
			lastAppliedHeight = height
		} else if err = replayer.verifyBlockState(*block); err != nil {
			return nil, fmt.Errorf("height %d: %w", height, err)
		}

		if _, ok := checkpointSet[height]; ok {
			blockHeader, err := block.blockHeader()
			if err != nil {
				return nil, fmt.Errorf("height %d: %w", height, err)
			}
			snapshots = append(snapshots, checkpointSnapshot{
				height:        height,
				blockHeader:   blockHeader,
				leafset:       replayer.cloneLeafsetBytes(),
				outputMMRSize: replayer.size(),
			})
		}
	}

	return snapshots, nil
}

func recoverMissingReplayGap(
	client *mwebReplayClient,
	replayer *mwebLeafsetReplayer,
	fromHeight uint32,
	toHeight uint32,
) error {
	if fromHeight > toHeight {
		return nil
	}
	for height := fromHeight; height <= toHeight; height++ {
		block, err := client.block(height)
		if err != nil {
			return err
		}
		if block.hasLeafsetEvents() {
			fmt.Fprintf(os.Stderr, "recovered missing MWEB replay block at height %d\n", height)
			if err = replayer.apply(height, *block); err != nil {
				return err
			}
		}
		if err = replayer.verifyBlockState(*block); err != nil {
			return fmt.Errorf("height %d: %w", height, err)
		}
	}
	return nil
}

func (c *mwebReplayClient) block(height uint32) (*mwebReplayBlock, error) {
	if c.cacheDir != "" {
		block, err := c.cachedBlock(height)
		if err == nil {
			return block, nil
		}
		if !errors.Is(err, os.ErrNotExist) {
			return nil, err
		}
	}

	block, err := c.remoteBlock(height)
	if err != nil {
		return nil, err
	}
	if c.cacheDir != "" {
		if err = c.writeCachedBlock(height, block); err != nil {
			return nil, err
		}
	}
	return block, nil
}

func (c *mwebReplayClient) remoteBlock(height uint32) (*mwebReplayBlock, error) {
	response, err := doCheckpointRequest(c.httpClient, "MWEB replay source", func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, fmt.Sprintf("%s/api/block/%d", c.baseURL, height), nil)
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("replay block %d returned HTTP %d", height, response.StatusCode)
	}
	return parseRpcExplorerBlock(response.Body)
}

func parseRpcExplorerBlock(reader io.Reader) (*mwebReplayBlock, error) {
	var block rpcExplorerBlock
	if err := json.NewDecoder(reader).Decode(&block); err != nil {
		return nil, err
	}
	if block.Mweb.LeafRoot == "" {
		return nil, fmt.Errorf("block %d missing MWEB data", block.Height)
	}
	leafRoot, err := normalizeHash(block.Mweb.LeafRoot)
	if err != nil {
		return nil, fmt.Errorf("invalid MWEB leaf root: %w", err)
	}
	inputs, err := normalizeHashes(block.Mweb.Inputs)
	if err != nil {
		return nil, fmt.Errorf("invalid MWEB input: %w", err)
	}
	outputs, err := normalizeHashes(block.Mweb.Outputs)
	if err != nil {
		return nil, fmt.Errorf("invalid MWEB output: %w", err)
	}

	replayBlock := &mwebReplayBlock{
		Height:        block.Height,
		Hash:          strings.ToLower(strings.TrimSpace(block.Hash)),
		Version:       block.Version,
		PrevBlock:     strings.ToLower(strings.TrimSpace(block.PrevBlock)),
		MerkleRoot:    strings.ToLower(strings.TrimSpace(block.MerkleRoot)),
		Timestamp:     block.Timestamp,
		Bits:          strings.ToLower(strings.TrimSpace(block.Bits)),
		Nonce:         block.Nonce,
		OutputMMRSize: block.Mweb.NumTxos,
		LeafRoot:      leafRoot,
		Inputs:        inputs,
		Outputs:       outputs,
	}
	if _, err = replayBlock.blockHeader(); err != nil {
		return nil, err
	}
	return replayBlock, nil
}

func (b mwebReplayBlock) blockHeader() (wire.BlockHeader, error) {
	prevBlock, err := chainhash.NewHashFromStr(b.PrevBlock)
	if err != nil {
		return wire.BlockHeader{}, err
	}
	merkleRoot, err := chainhash.NewHashFromStr(b.MerkleRoot)
	if err != nil {
		return wire.BlockHeader{}, err
	}
	bits, err := strconv.ParseUint(b.Bits, 16, 32)
	if err != nil {
		return wire.BlockHeader{}, err
	}
	header := wire.BlockHeader{
		Version:    b.Version,
		PrevBlock:  *prevBlock,
		MerkleRoot: *merkleRoot,
		Timestamp:  time.Unix(b.Timestamp, 0),
		Bits:       uint32(bits),
		Nonce:      b.Nonce,
	}
	expectedHash, err := chainhash.NewHashFromStr(b.Hash)
	if err != nil {
		return wire.BlockHeader{}, err
	}
	if header.BlockHash() != *expectedHash {
		return wire.BlockHeader{}, fmt.Errorf("block header hash mismatch: header=%s json=%s", header.BlockHash(), b.Hash)
	}
	return header, nil
}

func (b mwebReplayBlock) hasLeafsetEvents() bool {
	return len(b.Inputs) > 0 || len(b.Outputs) > 0
}

func (c *mwebReplayClient) cachedBlock(height uint32) (*mwebReplayBlock, error) {
	bytes, err := os.ReadFile(c.cachePath(height))
	if err != nil {
		return nil, err
	}
	var block mwebReplayBlock
	if err = json.Unmarshal(bytes, &block); err != nil {
		_ = os.Remove(c.cachePath(height))
		return nil, os.ErrNotExist
	}
	if err = block.normalize(height); err != nil {
		_ = os.Remove(c.cachePath(height))
		return nil, os.ErrNotExist
	}
	return &block, nil
}

func (b *mwebReplayBlock) normalize(height uint32) error {
	if b.Height != height {
		return fmt.Errorf("cached block height mismatch: %d", b.Height)
	}
	leafRoot, err := normalizeHash(b.LeafRoot)
	if err != nil {
		return err
	}
	inputs, err := normalizeHashes(b.Inputs)
	if err != nil {
		return err
	}
	outputs, err := normalizeHashes(b.Outputs)
	if err != nil {
		return err
	}

	b.Hash = strings.ToLower(strings.TrimSpace(b.Hash))
	b.PrevBlock = strings.ToLower(strings.TrimSpace(b.PrevBlock))
	b.MerkleRoot = strings.ToLower(strings.TrimSpace(b.MerkleRoot))
	b.Bits = strings.ToLower(strings.TrimSpace(b.Bits))
	b.LeafRoot = leafRoot
	b.Inputs = inputs
	b.Outputs = outputs
	_, err = b.blockHeader()
	return err
}

func (c *mwebReplayClient) writeCachedBlock(height uint32, block *mwebReplayBlock) error {
	if err := os.MkdirAll(filepath.Dir(c.cachePath(height)), 0755); err != nil {
		return err
	}
	bytes, err := json.Marshal(block)
	if err != nil {
		return err
	}
	return os.WriteFile(c.cachePath(height), bytes, 0644)
}

func (c *mwebReplayClient) cachePath(height uint32) string {
	networkName := c.networkName
	if networkName == "" {
		networkName = "unknown"
	}
	return filepath.Join(c.cacheDir, networkName, fmt.Sprintf("replay-block-%d.json", height))
}

type replayBlockFetchResult struct {
	height uint32
	block  *mwebReplayBlock
	err    error
}

type replayBlockFetcher struct {
	cancel  context.CancelFunc
	results <-chan replayBlockFetchResult
	pending map[uint32]replayBlockFetchResult
}

func newReplayBlockFetcher(client *mwebReplayClient, eventHeights []uint32) *replayBlockFetcher {
	ctx, cancel := context.WithCancel(context.Background())
	jobs := make(chan uint32)
	results := make(chan replayBlockFetchResult, replayBlockFetchers)

	go func() {
		defer close(jobs)
		for _, height := range eventHeights {
			select {
			case jobs <- height:
			case <-ctx.Done():
				return
			}
		}
	}()

	var wg sync.WaitGroup
	wg.Add(replayBlockFetchers)
	for range replayBlockFetchers {
		go func() {
			defer wg.Done()
			for {
				select {
				case height, ok := <-jobs:
					if !ok {
						return
					}
					block, err := client.block(height)
					select {
					case results <- replayBlockFetchResult{height: height, block: block, err: err}:
					case <-ctx.Done():
						return
					}
				case <-ctx.Done():
					return
				}
			}
		}()
	}
	go func() {
		wg.Wait()
		close(results)
	}()

	return &replayBlockFetcher{
		cancel:  cancel,
		results: results,
		pending: map[uint32]replayBlockFetchResult{},
	}
}

func (f *replayBlockFetcher) close() {
	f.cancel()
}

func (f *replayBlockFetcher) block(height uint32) (*mwebReplayBlock, error) {
	if result, ok := f.pending[height]; ok {
		delete(f.pending, height)
		return result.block, result.err
	}
	for result := range f.results {
		if result.height == height {
			return result.block, result.err
		}
		f.pending[result.height] = result
	}
	return nil, fmt.Errorf("missing fetched MWEB replay block %d", height)
}

type mwebLeafsetReplayer struct {
	bits          []byte
	outputIndexes map[string]uint64
	mmrSize       uint64
}

func newMwebLeafsetReplayer() *mwebLeafsetReplayer {
	return &mwebLeafsetReplayer{
		outputIndexes: map[string]uint64{},
	}
}

func (r *mwebLeafsetReplayer) apply(height uint32, block mwebReplayBlock) error {
	if block.Height != height {
		return fmt.Errorf("MWEB block height mismatch: %d", block.Height)
	}
	for _, outputID := range block.Inputs {
		if err := r.spendOutput(outputID); err != nil {
			return err
		}
	}
	for _, outputID := range block.Outputs {
		if err := r.addOutput(outputID); err != nil {
			return err
		}
	}
	return nil
}

func (r *mwebLeafsetReplayer) addOutput(outputID string) error {
	id, err := normalizeHash(outputID)
	if err != nil {
		return err
	}
	if _, exists := r.outputIndexes[id]; exists {
		return fmt.Errorf("duplicate MWEB output ID: %s", id)
	}

	index := r.mmrSize
	r.outputIndexes[id] = index
	if err = r.ensureSize(index + 1); err != nil {
		return err
	}
	r.setBit(index, true)
	r.mmrSize++
	return nil
}

func (r *mwebLeafsetReplayer) spendOutput(outputID string) error {
	id, err := normalizeHash(outputID)
	if err != nil {
		return err
	}
	index, ok := r.outputIndexes[id]
	if !ok {
		return fmt.Errorf("unknown MWEB input output ID: %s", id)
	}
	r.setBit(index, false)
	delete(r.outputIndexes, id)
	return nil
}

func (r *mwebLeafsetReplayer) ensureSize(size uint64) error {
	requiredBytes := (size + 7) / 8
	if requiredBytes > uint64(^uint(0)>>1) {
		return errors.New("leafset size exceeds platform limit")
	}
	for uint64(len(r.bits)) < requiredBytes {
		r.bits = append(r.bits, 0)
	}
	return nil
}

func (r *mwebLeafsetReplayer) setBit(index uint64, value bool) {
	mask := byte(0x80 >> (index % 8))
	if value {
		r.bits[index/8] |= mask
		return
	}
	r.bits[index/8] &^= mask
}

func (r *mwebLeafsetReplayer) verifyBlockState(block mwebReplayBlock) error {
	expected, err := decodeHash(block.LeafRoot)
	if err != nil {
		return err
	}
	actual := chainhash.Hash(blake3.Sum256(r.bits))
	if actual != expected {
		return fmt.Errorf("leaf root mismatch: replay=%s rpc=%s", actual, expected)
	}
	if block.OutputMMRSize != 0 && r.mmrSize != block.OutputMMRSize {
		return fmt.Errorf("output MMR size mismatch: replay=%d rpc=%d", r.mmrSize, block.OutputMMRSize)
	}
	return nil
}

func (r *mwebLeafsetReplayer) cloneLeafsetBytes() []byte {
	return append([]byte(nil), r.bits...)
}

func (r *mwebLeafsetReplayer) clone() *mwebLeafsetReplayer {
	clone := &mwebLeafsetReplayer{
		bits:          append([]byte(nil), r.bits...),
		outputIndexes: make(map[string]uint64, len(r.outputIndexes)),
		mmrSize:       r.mmrSize,
	}
	for key, value := range r.outputIndexes {
		clone.outputIndexes[key] = value
	}
	return clone
}

func (r *mwebLeafsetReplayer) size() uint64 {
	return r.mmrSize
}

func normalizeHashes(values []string) ([]string, error) {
	result := make([]string, 0, len(values))
	for _, value := range values {
		normalized, err := normalizeHash(value)
		if err != nil {
			return nil, err
		}
		result = append(result, normalized)
	}
	return result, nil
}

func normalizeHash(value string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(value))
	if _, err := decodeHash(normalized); err != nil {
		return "", err
	}
	return normalized, nil
}

func decodeHash(value string) (chainhash.Hash, error) {
	bytes, err := hex.DecodeString(value)
	if err != nil {
		return chainhash.Hash{}, err
	}
	if len(bytes) != chainhash.HashSize {
		return chainhash.Hash{}, errors.New("invalid hash size")
	}

	var hash chainhash.Hash
	copy(hash[:], bytes)
	return hash, nil
}

func sortedUniqueHeights(heights []uint32) []uint32 {
	seen := map[uint32]struct{}{}
	for _, height := range heights {
		seen[height] = struct{}{}
	}

	result := make([]uint32, 0, len(seen))
	for height := range seen {
		result = append(result, height)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}
