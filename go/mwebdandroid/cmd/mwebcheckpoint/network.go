package main

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg"
	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/ltcutil/mweb"
	"github.com/ltcmweb/ltcd/peer"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/piratecash/mwebd-android/go/mwebdandroid/internal/checkpoint"
)

func resolveNetworkHeights(value string, startHeight, endHeight, interval uint32) ([]uint32, error) {
	if strings.TrimSpace(value) != "" {
		return parseHeights(value)
	}
	if startHeight == 0 || endHeight == 0 {
		return nil, errors.New("network generation requires -heights or -start/-end")
	}
	if interval == 0 {
		return nil, errors.New("checkpoint interval must be greater than zero")
	}
	if startHeight > endHeight {
		return nil, errors.New("-start must be less than or equal to -end")
	}

	var heights []uint32
	for height := startHeight; height <= endHeight; height += interval {
		heights = append(heights, height)
		if endHeight-height < interval {
			break
		}
	}
	return heights, nil
}

func parseHeights(value string) ([]uint32, error) {
	if strings.EqualFold(strings.TrimSpace(value), "tip") {
		return []uint32{0}, nil
	}

	seen := map[uint32]struct{}{}
	for _, part := range strings.Split(value, ",") {
		height, err := strconv.ParseUint(strings.TrimSpace(part), 10, 32)
		if err != nil {
			return nil, err
		}
		seen[uint32(height)] = struct{}{}
	}

	heights := make([]uint32, 0, len(seen))
	for height := range seen {
		heights = append(heights, height)
	}
	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	return heights, nil
}

func generateNetworkCheckpoints(
	networkName string,
	peerAddress string,
	heights []uint32,
	timeout time.Duration,
) ([]string, error) {
	params, err := networkParams(networkName)
	if err != nil {
		return nil, err
	}
	if len(heights) == 0 {
		return nil, errors.New("no checkpoint heights requested")
	}
	tipMode := len(heights) == 1 && heights[0] == 0

	client, err := newNetworkClient(peerAddress, params, timeout)
	if err != nil {
		return nil, err
	}
	defer client.close()

	blockData, err := client.fetchBlockHeaders(params, heights, timeout)
	if err != nil {
		return nil, err
	}
	if tipMode {
		heights = []uint32{blockData.tipHeight}
	}

	mwebDataByHeight := map[uint32]*mwebCheckpointData{}
	if tipMode {
		mwebData, err := client.fetchMwebData(blockData.hashes[blockData.tipHeight], blockData.tipHeight, timeout)
		if err != nil {
			return nil, err
		}
		mwebDataByHeight[blockData.tipHeight] = mwebData
	}

	filterHeaders, err := client.fetchFilterHeaders(blockData.hashes, heights, timeout)
	if err != nil {
		return nil, err
	}

	lines := make([]string, 0, len(heights))
	for _, height := range heights {
		mwebData := mwebDataByHeight[height]
		if mwebData == nil {
			mwebData, err = client.fetchMwebData(blockData.hashes[height], height, timeout)
			if err != nil {
				return nil, err
			}
		}
		line, err := checkpoint.Encode(checkpoint.Data{
			Height:        height,
			BlockHeader:   mwebData.blockHeader,
			FilterHeader:  filterHeaders[height],
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

func generateExplorerPeerCheckpoints(
	networkName string,
	peerAddress string,
	explorerURL string,
	replayURL string,
	cacheDir string,
	heights []uint32,
	interval uint32,
	safeDepth uint32,
	timeout time.Duration,
) ([]string, error) {
	startHeight, err := mwebActivationStartHeight(networkName)
	if err != nil {
		return nil, err
	}
	explorer := newMwebExplorerClient(networkName, explorerURL, cacheDir, timeout)
	heights, err = resolveExplorerHeights(explorer, heights, interval, safeDepth, startHeight)
	if err != nil {
		return nil, err
	}
	maxHeight := heights[len(heights)-1]
	nonEmptyHeights, err := explorer.nonEmptyHeights(startHeight, maxHeight)
	if err != nil {
		return nil, err
	}
	replayClient := newMwebReplayClient(replayURL, cacheDir, networkName, timeout)
	snapshots, err := replayMwebBlocks(replayClient, heights, nonEmptyHeights)
	if err != nil {
		return nil, err
	}

	params, err := networkParams(networkName)
	if err != nil {
		return nil, err
	}
	client, err := newNetworkClient(peerAddress, params, timeout)
	if err != nil {
		return nil, err
	}
	defer client.close()

	lines := make([]string, 0, len(snapshots))
	for _, snapshot := range snapshots {
		blockHash := snapshot.blockHeader.BlockHash()
		filterHeader, err := client.fetchFilterHeaderByHash(snapshot.height, blockHash, timeout)
		if err != nil {
			return nil, err
		}
		line, err := checkpoint.Encode(checkpoint.Data{
			Height:        snapshot.height,
			BlockHeader:   snapshot.blockHeader,
			FilterHeader:  filterHeader,
			Leafset:       snapshot.leafset,
			OutputMMRSize: snapshot.outputMMRSize,
		})
		if err != nil {
			return nil, err
		}
		lines = append(lines, line)
	}
	return lines, nil
}

func checkNetworkPeer(networkName string, peerAddress string, timeout time.Duration) error {
	params, err := networkParams(networkName)
	if err != nil {
		return err
	}
	client, err := newNetworkClient(peerAddress, params, timeout)
	if err != nil {
		return err
	}
	client.close()
	return nil
}

func networkParams(networkName string) (*chaincfg.Params, error) {
	switch strings.ToLower(networkName) {
	case "mainnet", "main":
		return &chaincfg.MainNetParams, nil
	case "testnet4", "testnet":
		return &chaincfg.TestNet4Params, nil
	default:
		return nil, fmt.Errorf("unsupported network: %s", networkName)
	}
}

type networkClient struct {
	peer     *peer.Peer
	messages chan wire.Message
	errors   chan error
}

func newNetworkClient(address string, params *chaincfg.Params, timeout time.Duration) (*networkClient, error) {
	messages := make(chan wire.Message, 64)
	errorsChan := make(chan error, 4)
	ready := make(chan struct{})

	config := &peer.Config{
		UserAgentName:    "mwebcheckpoint",
		UserAgentVersion: "0.1.0",
		ChainParams:      params,
		ProtocolVersion:  wire.MwebLightClientVersion,
		Services:         wire.SFNodeWitness | wire.SFNodeCF | wire.SFNodeMWEB,
		DisableRelayTx:   true,
		Listeners: peer.MessageListeners{
			OnVersion: func(_ *peer.Peer, msg *wire.MsgVersion) *wire.MsgReject {
				required := wire.ServiceFlag(wire.SFNodeCF | wire.SFNodeMWEB | wire.SFNodeMWEBLightClient)
				if msg.Services&required != required {
					errorsChan <- fmt.Errorf("peer does not advertise required services: %v", msg.Services)
				}
				return nil
			},
			OnVerAck: func(_ *peer.Peer, _ *wire.MsgVerAck) {
				close(ready)
			},
			OnHeaders: func(_ *peer.Peer, msg *wire.MsgHeaders) {
				messages <- msg
			},
			OnCFHeaders: func(_ *peer.Peer, msg *wire.MsgCFHeaders) {
				messages <- msg
			},
			OnCFCheckpt: func(_ *peer.Peer, msg *wire.MsgCFCheckpt) {
				messages <- msg
			},
			OnNotFound: func(_ *peer.Peer, msg *wire.MsgNotFound) {
				messages <- msg
			},
			OnMwebHeader: func(_ *peer.Peer, msg *wire.MsgMwebHeader) {
				messages <- msg
			},
			OnMwebLeafset: func(_ *peer.Peer, msg *wire.MsgMwebLeafset) {
				messages <- msg
			},
			OnReject: func(_ *peer.Peer, msg *wire.MsgReject) {
				errorsChan <- fmt.Errorf("peer rejected %s: %s", msg.Cmd, msg.Reason)
			},
		},
	}

	remotePeer, err := peer.NewOutboundPeer(config, address)
	if err != nil {
		return nil, err
	}
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, err
	}
	remotePeer.AssociateConnection(conn)

	select {
	case <-ready:
	case err := <-errorsChan:
		remotePeer.Disconnect()
		return nil, err
	case <-time.After(timeout):
		remotePeer.Disconnect()
		return nil, errors.New("peer handshake timed out")
	}

	return &networkClient{
		peer:     remotePeer,
		messages: messages,
		errors:   errorsChan,
	}, nil
}

func (c *networkClient) close() {
	c.peer.Disconnect()
}

type blockHeaderData struct {
	headers   map[uint32]wire.BlockHeader
	hashes    []chainhash.Hash
	tipHeight uint32
}

func (c *networkClient) fetchBlockHeaders(
	params *chaincfg.Params,
	heights []uint32,
	timeout time.Duration,
) (*blockHeaderData, error) {
	maxHeight := heights[len(heights)-1]
	toTip := maxHeight == 0
	needed := heightSet(heights)
	headers := map[uint32]wire.BlockHeader{}
	hashes := []chainhash.Hash{*params.GenesisHash}
	if !toTip {
		hashes = make([]chainhash.Hash, maxHeight+1)
		hashes[0] = *params.GenesisHash
	}

	lastHash := *params.GenesisHash
	height := uint32(0)
	for toTip || height < maxHeight {
		if height == 0 || height%100_000 == 0 {
			fmt.Fprintf(os.Stderr, "fetching block headers: %d/%d\n", height, maxHeight)
		}
		request := wire.NewMsgGetHeaders()
		request.ProtocolVersion = wire.ProtocolVersion
		if err := request.AddBlockLocatorHash(&lastHash); err != nil {
			return nil, err
		}
		c.peer.QueueMessage(request, nil)

		response, err := c.waitHeaders(timeout)
		if err != nil {
			return nil, err
		}
		if len(response.Headers) == 0 {
			return nil, fmt.Errorf("peer returned no headers before height %d", maxHeight)
		}

		for _, header := range response.Headers {
			height++
			if !toTip && height > maxHeight {
				break
			}
			hash := header.BlockHash()
			if toTip {
				hashes = append(hashes, hash)
			} else {
				hashes[height] = hash
			}
			lastHash = hash
			if _, ok := needed[height]; ok {
				headers[height] = *header
			}
			if toTip && len(response.Headers) < wire.MaxBlockHeadersPerMsg {
				headers[height] = *header
			}
		}
		if toTip && len(response.Headers) < wire.MaxBlockHeadersPerMsg {
			break
		}
	}

	return &blockHeaderData{
		headers:   headers,
		hashes:    hashes,
		tipHeight: height,
	}, nil
}

func (c *networkClient) fetchFilterHeaders(
	blockHashes []chainhash.Hash,
	heights []uint32,
	timeout time.Duration,
) (map[uint32]chainhash.Hash, error) {
	headers := map[uint32]chainhash.Hash{}
	for _, height := range heights {
		header, err := c.fetchFilterHeader(blockHashes, height, timeout)
		if err != nil {
			return nil, err
		}
		headers[height] = header
	}

	return headers, nil
}

func (c *networkClient) fetchFilterHeader(
	blockHashes []chainhash.Hash,
	height uint32,
	timeout time.Duration,
) (chainhash.Hash, error) {
	return c.fetchFilterHeaderByHash(height, blockHashes[height], timeout)
}

func (c *networkClient) fetchFilterHeaderByHash(
	height uint32,
	blockHash chainhash.Hash,
	timeout time.Duration,
) (chainhash.Hash, error) {
	fmt.Fprintf(os.Stderr, "fetching filter header at height %d\n", height)
	request := wire.NewMsgGetCFHeaders(wire.GCSFilterRegular, height, &blockHash)
	c.peer.QueueMessage(request, nil)

	response, err := c.waitCFHeaders(blockHash, timeout)
	if err != nil {
		return chainhash.Hash{}, err
	}
	if len(response.FilterHashes) != 1 {
		return chainhash.Hash{}, fmt.Errorf("expected 1 filter hash at height %d, got %d", height, len(response.FilterHashes))
	}
	return chainhash.DoubleHashH(append(response.FilterHashes[0][:], response.PrevFilterHeader[:]...)), nil
}

type mwebCheckpointData struct {
	blockHeader   wire.BlockHeader
	leafset       []byte
	outputMMRSize uint64
}

func (c *networkClient) fetchMwebData(
	blockHash chainhash.Hash,
	height uint32,
	timeout time.Duration,
) (*mwebCheckpointData, error) {
	if err := c.primeMwebPeer(blockHash, height, timeout); err != nil {
		return nil, err
	}

	request := wire.NewMsgGetData()
	fmt.Fprintf(os.Stderr, "fetching MWEB header and leafset at height %d\n", height)
	request.AddInvVect(wire.NewInvVect(wire.InvTypeMwebHeader, &blockHash))
	request.AddInvVect(wire.NewInvVect(wire.InvTypeMwebLeafset, &blockHash))
	c.peer.QueueMessageWithEncoding(request, nil, wire.LatestEncoding)

	var header *wire.MsgMwebHeader
	var leafset *wire.MsgMwebLeafset
	deadline := time.After(timeout)
	for header == nil || leafset == nil {
		select {
		case msg := <-c.messages:
			switch typed := msg.(type) {
			case *wire.MsgMwebHeader:
				if typed.Merkle.Header.BlockHash() != blockHash {
					continue
				}
				if uint32(typed.MwebHeader.Height) != height {
					return nil, fmt.Errorf("mwebheader height mismatch: %d", typed.MwebHeader.Height)
				}
				if err := mweb.VerifyHeader(typed); err != nil {
					return nil, err
				}
				header = typed
			case *wire.MsgMwebLeafset:
				if typed.BlockHash != blockHash {
					continue
				}
				leafset = typed
			case *wire.MsgNotFound:
				return nil, fmt.Errorf("peer returned notfound for MWEB data at height %d", height)
			}
		case err := <-c.errors:
			return nil, err
		case <-deadline:
			return nil, fmt.Errorf("timed out waiting for MWEB data at height %d", height)
		}
	}

	if err := mweb.VerifyLeafset(header, leafset); err != nil {
		return nil, err
	}

	return &mwebCheckpointData{
		blockHeader:   header.Merkle.Header,
		leafset:       leafset.Leafset,
		outputMMRSize: header.MwebHeader.OutputMMRSize,
	}, nil
}

func (c *networkClient) primeMwebPeer(
	blockHash chainhash.Hash,
	height uint32,
	timeout time.Duration,
) error {
	request := wire.NewMsgGetCFHeaders(wire.GCSFilterRegular, height, &blockHash)
	c.peer.QueueMessage(request, nil)
	_, err := c.waitCFHeaders(blockHash, timeout)
	return err
}

func (c *networkClient) waitHeaders(timeout time.Duration) (*wire.MsgHeaders, error) {
	msg, err := c.waitMessage(func(msg wire.Message) bool {
		_, ok := msg.(*wire.MsgHeaders)
		return ok
	}, timeout)
	if err != nil {
		return nil, err
	}
	return msg.(*wire.MsgHeaders), nil
}

func (c *networkClient) waitCFHeaders(stopHash chainhash.Hash, timeout time.Duration) (*wire.MsgCFHeaders, error) {
	msg, err := c.waitMessage(func(msg wire.Message) bool {
		response, ok := msg.(*wire.MsgCFHeaders)
		return ok && response.StopHash == stopHash
	}, timeout)
	if err != nil {
		return nil, err
	}
	return msg.(*wire.MsgCFHeaders), nil
}

func (c *networkClient) waitCFCheckpt(stopHash chainhash.Hash, timeout time.Duration) (*wire.MsgCFCheckpt, error) {
	msg, err := c.waitMessage(func(msg wire.Message) bool {
		response, ok := msg.(*wire.MsgCFCheckpt)
		return ok && response.StopHash == stopHash
	}, timeout)
	if err != nil {
		return nil, err
	}
	return msg.(*wire.MsgCFCheckpt), nil
}

func (c *networkClient) waitMessage(
	matches func(wire.Message) bool,
	timeout time.Duration,
) (wire.Message, error) {
	deadline := time.After(timeout)
	for {
		select {
		case msg := <-c.messages:
			if matches(msg) {
				return msg, nil
			}
		case err := <-c.errors:
			return nil, err
		case <-deadline:
			return nil, errors.New("timed out waiting for peer response")
		}
	}
}

func heightSet(heights []uint32) map[uint32]struct{} {
	set := make(map[uint32]struct{}, len(heights))
	for _, height := range heights {
		set[height] = struct{}{}
	}
	return set
}
