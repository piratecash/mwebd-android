package mwebd

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/btcsuite/btclog"
	lru "github.com/hashicorp/golang-lru/v2"
	"github.com/ltcmweb/ltcd/chaincfg"
	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/ltcutil"
	"github.com/ltcmweb/ltcd/ltcutil/mweb"
	"github.com/ltcmweb/ltcd/ltcutil/mweb/mw"
	"github.com/ltcmweb/ltcd/txscript"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/mwebd/ledger"
	"github.com/ltcmweb/mwebd/proto"
	"github.com/ltcmweb/mwebd/sign"
	"github.com/ltcmweb/neutrino"
	"github.com/ltcmweb/neutrino/mwebdb"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
	"golang.org/x/net/proxy"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"gopkg.in/natefinch/lumberjack.v2"
)

type Server struct {
	proto.UnimplementedRpcServer
	db        walletdb.DB
	cs        *neutrino.ChainService
	cp        chaincfg.Params
	mtx       sync.Mutex
	server    *grpc.Server
	utxoChan  map[mw.SecretKey]map[*utxoStreamer]struct{}
	coinCache *lru.Cache[mw.SecretKey, *lru.Cache[chainhash.Hash, *mweb.Coin]]
	ledgerTx  *ledger.TxContext

	lifecycleMu sync.Mutex
	started     bool
	stopping    bool
	accepting   bool
	done        chan struct{}
	cleanupOnce sync.Once
	operations  sync.WaitGroup
	cleanupErr  error
}

type ServerArgs struct {
	Chain, DataDir, PeerAddr, ProxyAddr string
	UnaryInterceptors                   []grpc.UnaryServerInterceptor
	StreamInterceptors                  []grpc.StreamServerInterceptor
}

func NewBareServer(chainParams chaincfg.Params) *Server {
	return &Server{
		cp:       chainParams,
		done:     make(chan struct{}),
		utxoChan: map[mw.SecretKey]map[*utxoStreamer]struct{}{},
	}
}

func NewServer(chain, dataDir, peer string) (*Server, error) {
	return NewServer2(&ServerArgs{
		Chain: chain, DataDir: dataDir, PeerAddr: peer,
	})
}

func NewServer2(args *ServerArgs) (s *Server, err error) {
	if args == nil {
		return nil, errors.New("server args are required")
	}

	s = NewBareServer(chaincfg.MainNetParams)
	unaryInterceptors := append(
		[]grpc.UnaryServerInterceptor{s.trackUnaryOperation},
		args.UnaryInterceptors...,
	)
	streamInterceptors := append(
		[]grpc.StreamServerInterceptor{s.trackStreamOperation},
		args.StreamInterceptors...,
	)
	grpcOptions := []grpc.ServerOption{
		grpc.ChainUnaryInterceptor(unaryInterceptors...),
		grpc.ChainStreamInterceptor(streamInterceptors...),
	}
	s.server = grpc.NewServer(grpcOptions...)
	proto.RegisterRpcServer(s.server, s)

	s.coinCache, _ = lru.New[mw.SecretKey, *lru.Cache[chainhash.Hash, *mweb.Coin]](10)
	defer func() {
		if err != nil {
			s.finish(err)
		}
	}()

	s.db, err = walletdb.Create(
		"bdb", filepath.Join(args.DataDir, "neutrino.db"), false, time.Minute)
	if err != nil {
		return
	}

	cfg := neutrino.Config{
		DataDir:     args.DataDir,
		Database:    s.db,
		ChainParams: chaincfg.MainNetParams,
	}

	switch args.Chain {
	case "testnet":
		cfg.ChainParams = chaincfg.TestNet4Params
	case "regtest":
		cfg.ChainParams = chaincfg.RegressionNetParams
	}

	if args.PeerAddr != "" {
		cfg.AddPeers = []string{args.PeerAddr}
	}

	if args.ProxyAddr != "" {
		url, err := url.Parse(args.ProxyAddr)
		if err != nil {
			return nil, err
		}
		dialer, err := proxy.FromURL(url, proxy.Direct)
		if err != nil {
			return nil, err
		}
		cfg.Dialer = func(addr net.Addr) (net.Conn, error) {
			return dialer.Dial(addr.Network(), addr.String())
		}
	}

	log := btclog.NewBackend(&lumberjack.Logger{
		Filename:   filepath.Join(args.DataDir, "logs", "debug.log"),
		MaxSize:    10,
		MaxBackups: 10,
		Compress:   true,
	}).Logger("")
	log.SetLevel(btclog.LevelDebug)
	neutrino.UseLogger(log)

	s.cs, err = neutrino.NewChainService(cfg)
	if err != nil {
		return
	}
	s.cp = s.cs.ChainParams()

	s.cs.RegisterMwebUtxosCallback(s.utxoHandler)
	if err = s.cs.Start(); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Server) Start(port int) (int, error) {
	return s.StartAddr(fmt.Sprintf("127.0.0.1:%d", port))
}

func (s *Server) StartAddr(addr string) (int, error) {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopping {
		return 0, errors.New("mwebd server is stopping")
	}
	if s.started {
		return 0, errors.New("mwebd server is already started")
	}

	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return 0, err
	}
	s.started = true
	s.accepting = true
	go s.serve(lis)
	return lis.Addr().(*net.TCPAddr).Port, nil
}

func (s *Server) StartUnix(path string) error {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopping {
		return errors.New("mwebd server is stopping")
	}
	if s.started {
		return errors.New("mwebd server is already started")
	}

	os.Remove(path)
	lis, err := net.Listen("unix", path)
	if err != nil {
		return err
	}
	s.started = true
	s.accepting = true
	go s.serve(lis)
	return nil
}

func (s *Server) serve(lis net.Listener) {
	s.finish(s.server.Serve(lis))
}

func (s *Server) Stop() error {
	if s == nil {
		return nil
	}

	s.lifecycleMu.Lock()
	started := s.started
	s.stopping = true
	s.accepting = false
	s.lifecycleMu.Unlock()

	if started && s.server != nil {
		s.server.Stop()
	} else {
		s.finish(nil)
	}
	<-s.done
	return s.cleanupErr
}

func (s *Server) Wait() error {
	if s == nil {
		return nil
	}
	<-s.done
	return s.cleanupErr
}

func (s *Server) finish(serveErr error) {
	s.cleanupOnce.Do(func() {
		s.lifecycleMu.Lock()
		s.stopping = true
		s.accepting = false
		s.lifecycleMu.Unlock()

		if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) && s.server != nil {
			s.server.Stop()
		}
		s.operations.Wait()
		var cleanupErr error
		if s.cs != nil {
			cleanupErr = errors.Join(cleanupErr, s.cs.Stop())
		}
		if s.db != nil {
			cleanupErr = errors.Join(cleanupErr, s.db.Close())
		}
		if serveErr != nil && !errors.Is(serveErr, grpc.ErrServerStopped) {
			cleanupErr = errors.Join(serveErr, cleanupErr)
		}
		s.cleanupErr = cleanupErr
		close(s.done)
	})
}

func (s *Server) trackUnaryOperation(
	ctx context.Context,
	req any,
	info *grpc.UnaryServerInfo,
	handler grpc.UnaryHandler,
) (any, error) {
	if !s.beginOperation(true) {
		return nil, status.Error(codes.Unavailable, "mwebd server is not running")
	}
	defer s.operations.Done()
	return handler(ctx, req)
}

func (s *Server) trackStreamOperation(
	srv any,
	stream grpc.ServerStream,
	info *grpc.StreamServerInfo,
	handler grpc.StreamHandler,
) error {
	if !s.beginOperation(true) {
		return status.Error(codes.Unavailable, "mwebd server is not running")
	}
	defer s.operations.Done()
	return handler(srv, stream)
}

func (s *Server) beginOperation(requireStarted bool) bool {
	s.lifecycleMu.Lock()
	defer s.lifecycleMu.Unlock()
	if s.stopping || requireStarted && !s.accepting {
		return false
	}
	s.operations.Add(1)
	return true
}

func (s *Server) Status(context.Context,
	*proto.StatusRequest) (*proto.StatusResponse, error) {

	bh, bhHeight, err := s.cs.BlockHeaders.ChainTip()
	if err != nil {
		return nil, err
	}

	heightMap, err := s.cs.MwebCoinDB.GetLeavesAtHeight()
	if err != nil {
		return nil, err
	}

	var mhHeight uint32
	for height := range heightMap {
		if height > mhHeight {
			mhHeight = height
		}
	}

	lfs, err := s.cs.MwebCoinDB.GetLeafset()
	if err != nil {
		return nil, err
	}

	return &proto.StatusResponse{
		BlockHeaderHeight: int32(bhHeight),
		MwebHeaderHeight:  int32(mhHeight),
		MwebUtxosHeight:   int32(lfs.Height),
		BlockTime:         uint32(bh.Timestamp.Unix()),
	}, nil
}

func (s *Server) utxoHandler(lfs *mweb.Leafset, utxos []*wire.MwebNetUtxo) {
	if !s.beginOperation(false) {
		return
	}
	defer s.operations.Done()

	walletdb.Update(s.db, func(tx walletdb.ReadWriteTx) error {
		bucket, err := tx.CreateTopLevelBucket([]byte("mweb-mempool"))
		if err != nil {
			return err
		}
		for _, utxo := range utxos {
			if utxo.Height == 0 {
				var buf bytes.Buffer
				if err = utxo.Output.Serialize(&buf); err != nil {
					return err
				}
				if err = bucket.Put(utxo.OutputId[:], buf.Bytes()); err != nil {
					return err
				}
			} else if err = bucket.Delete(utxo.OutputId[:]); err != nil {
				return err
			}
		}
		return nil
	})

	var leaves []uint64
	for _, utxo := range utxos {
		if utxo.Height > 0 {
			leaves = append(leaves, utxo.LeafIndex)
		}
	}

	s.mtx.Lock()
	defer s.mtx.Unlock()

	for scanSecret, us := range s.utxoChan {
		utxos := s.filterUtxos(&scanSecret, utxos)
		for u := range us {
			u.notify(lfs, utxos, leaves)
		}
	}
}

func (s *Server) filterUtxos(scanSecret *mw.SecretKey,
	utxos []*wire.MwebNetUtxo) (result []*proto.Utxo) {

	for _, utxo := range utxos {
		coin, err := s.rewindOutput(utxo.Output, scanSecret)
		if err != nil {
			continue
		}
		addr := ltcutil.NewAddressMweb(coin.Address, &s.cp)
		bh, err := s.cs.BlockHeaders.FetchHeaderByHeight(uint32(utxo.Height))
		if err != nil {
			bh = &wire.BlockHeader{Timestamp: time.Unix(0, 0)}
		}
		result = append(result, &proto.Utxo{
			Height:    utxo.Height,
			Value:     coin.Value,
			Address:   addr.String(),
			OutputId:  hex.EncodeToString(utxo.OutputId[:]),
			BlockTime: uint32(bh.Timestamp.Unix()),
		})
	}
	return
}

// registerStreamer adds u to the set of streamers for scanSecret. The deferred
// unlock guarantees s.mtx is released even if the map write panics, so a single
// bad registration cannot wedge every other subscriber on a leaked lock.
func (s *Server) registerStreamer(scanSecret mw.SecretKey, u *utxoStreamer) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	if s.utxoChan[scanSecret] == nil {
		s.utxoChan[scanSecret] = map[*utxoStreamer]struct{}{}
	}
	s.utxoChan[scanSecret][u] = struct{}{}
}

// unregisterStreamer removes u and drops the scanSecret entry once it is empty.
func (s *Server) unregisterStreamer(scanSecret mw.SecretKey, u *utxoStreamer) {
	s.mtx.Lock()
	defer s.mtx.Unlock()
	delete(s.utxoChan[scanSecret], u)
	if len(s.utxoChan[scanSecret]) == 0 {
		delete(s.utxoChan, scanSecret)
	}
}

func (s *Server) Utxos(req *proto.UtxosRequest,
	stream proto.Rpc_UtxosServer) (err error) {

	if len(req.ScanSecret) != len(mw.SecretKey{}) {
		return fmt.Errorf("invalid scan secret length: %d", len(req.ScanSecret))
	}
	// Snapshot the caller-owned scan secret. The request slice may be reused or
	// mutated by the caller after this call, and the key is dereferenced more
	// than once during registration; a stable copy keeps the map key consistent.
	var scanSecret mw.SecretKey
	copy(scanSecret[:], req.ScanSecret)

	u := s.newUtxoStreamer(&scanSecret)
	s.registerStreamer(scanSecret, u)
	defer func() {
		close(u.quit)
		s.unregisterStreamer(scanSecret, u)
	}()

	heightMap, err := s.cs.MwebCoinDB.GetLeavesAtHeight()
	if err != nil {
		return
	}
	var heights []uint32
	for height := range heightMap {
		heights = append(heights, height)
	}
	slices.Sort(heights)
	index, _ := slices.BinarySearch(heights, uint32(req.FromHeight))
	leaf := uint64(0)
	if index > 0 {
		leaf = heightMap[heights[index-1]]
	}

	u.lfs, err = s.cs.MwebCoinDB.GetLeafset()
	if err != nil {
		return
	}
	for leaves := []uint64{}; leaf < u.lfs.Size; leaf++ {
		if u.lfs.Contains(leaf) {
			leaves = append(leaves, leaf)
		}
		if len(leaves) == 1000 || leaf == u.lfs.Size-1 {
			utxos, err := s.cs.MwebCoinDB.FetchLeaves(leaves)
			if err != nil {
				return err
			}
			for _, utxo := range s.filterUtxos(&scanSecret, utxos) {
				if err = stream.Send(utxo); err != nil {
					return err
				}
			}
			leaves = leaves[:0]
		}
	}
	if err = sendReplayComplete(stream, uint32(u.lfs.Height)); err != nil {
		return err
	}
	// Stream live UTXOs until the client cancels. Select on the stream context
	// so cancellation wakes us immediately instead of parking on <-u.ch until
	// the next notify, which may never arrive on an idle chain.
	ctx := stream.Context()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case utxo := <-u.ch:
			if err = stream.Send(utxo); err != nil {
				return err
			}
		}
	}
}

// sendReplayComplete marks the end of the historical replay phase so clients can
// advance their local delivery cursor only after replayed UTXOs were streamed.
func sendReplayComplete(stream proto.Rpc_UtxosServer, height uint32) error {
	return stream.Send(&proto.Utxo{
		ReplayComplete:       true,
		ReplayCompleteHeight: height,
	})
}

func (s *Server) Addresses(ctx context.Context,
	req *proto.AddressRequest) (*proto.AddressResponse, error) {

	resp := sign.Addresses(&sign.AddressesRequest{
		Scan:     req.ScanSecret,
		SpendPub: req.SpendPubkey,
		From:     req.FromIndex,
		To:       req.ToIndex,
	}, &s.cp)
	return &proto.AddressResponse{Address: resp.Address}, nil
}

func Addresses(scanSecret, spendPubKey []byte, i, j int32) string {
	resp := sign.Addresses(&sign.AddressesRequest{
		Scan:     scanSecret,
		SpendPub: spendPubKey,
		From:     uint32(i),
		To:       uint32(j),
	}, &chaincfg.MainNetParams)
	return strings.Join(resp.Address, ",")
}

func (s *Server) Spent(ctx context.Context,
	req *proto.SpentRequest) (*proto.SpentResponse, error) {

	resp := &proto.SpentResponse{}
	for _, outputIdStr := range req.OutputId {
		outputId, err := hex.DecodeString(outputIdStr)
		if err != nil {
			return nil, err
		}
		if !s.cs.MwebUtxoExists((*chainhash.Hash)(outputId)) {
			resp.OutputId = append(resp.OutputId, outputIdStr)
		}
	}
	return resp, nil
}

func (s *Server) fetchCoin(outputId chainhash.Hash) (*wire.MwebOutput, error) {
	output, err := s.cs.MwebCoinDB.FetchCoin(&outputId)
	if err == mwebdb.ErrCoinNotFound {
		slices.Reverse(outputId[:])
		output, err = s.cs.MwebCoinDB.FetchCoin(&outputId)
	}
	if err == mwebdb.ErrCoinNotFound {
		err = walletdb.View(s.db, func(tx walletdb.ReadTx) error {
			bucket := tx.ReadBucket([]byte("mweb-mempool"))
			if bucket == nil {
				return err
			}
			b := bucket.Get(outputId[:])
			if b == nil {
				slices.Reverse(outputId[:])
				b = bucket.Get(outputId[:])
			}
			if b == nil {
				return err
			}
			output = &wire.MwebOutput{}
			return output.Deserialize(bytes.NewReader(b))
		})
	}
	return output, err
}

func (s *Server) rewindOutput(output *wire.MwebOutput,
	scanSecret *mw.SecretKey) (coin *mweb.Coin, err error) {

	cache, ok := s.coinCache.Get(*scanSecret)
	if !ok {
		cache, _ = lru.New[chainhash.Hash, *mweb.Coin](100)
		s.coinCache.Add(*scanSecret, cache)
	}
	coin, ok = cache.Get(*output.Hash())
	if !ok {
		coin, err = mweb.RewindOutput(output, scanSecret)
		if err == nil {
			cache.Add(*output.Hash(), coin)
		}
	}
	if coin != nil {
		c := mweb.Coin(*coin)
		coin = &c
	}
	return
}

func (s *Server) Create(ctx context.Context,
	req *proto.CreateRequest) (*proto.CreateResponse, error) {

	var (
		tx         wire.MsgTx
		txIns      []*wire.TxIn
		pegouts    []*wire.TxOut
		coins      []*mweb.Coin
		addrIndex  []uint32
		recipients []*mweb.Recipient
		pegin      uint64
		sumCoins   uint64
		sumOutputs uint64
	)

	err := tx.Deserialize(bytes.NewReader(req.RawTx))
	if err != nil {
		return nil, err
	}

	keychain := &mweb.Keychain{
		Scan:  (*mw.SecretKey)(req.ScanSecret),
		Spend: (*mw.SecretKey)(req.SpendSecret),
	}

	for _, txIn := range tx.TxIn {
		output, err := s.fetchCoin(txIn.PreviousOutPoint.Hash)
		switch err {
		case nil:
			coin, err := s.rewindOutput(output, keychain.Scan)
			if err != nil {
				return nil, err
			}

			index := txIn.PreviousOutPoint.Index
			coin.CalculateOutputKey(keychain.SpendKey(index))
			coins = append(coins, coin)
			addrIndex = append(addrIndex, index)
			sumCoins += coin.Value

		case mwebdb.ErrCoinNotFound:
			txIns = append(txIns, txIn)

		default:
			return nil, err
		}
	}

	for _, txOut := range tx.TxOut {
		sumOutputs += uint64(txOut.Value)
		if !txscript.IsMweb(txOut.PkScript) {
			pegouts = append(pegouts, txOut)
			continue
		}

		_, addrs, _, err := txscript.ExtractPkScriptAddrs(txOut.PkScript, &s.cp)
		if err != nil {
			return nil, err
		}

		recipients = append(recipients, &mweb.Recipient{
			Value:   uint64(txOut.Value),
			Address: addrs[0].(*ltcutil.AddressMweb).StealthAddress(),
		})
	}

	if len(coins) == 0 && len(recipients) == 0 {
		return &proto.CreateResponse{RawTx: req.RawTx}, nil
	}

	fee := mweb.EstimateFee(tx.TxOut, ltcutil.Amount(req.FeeRatePerKb), false)
	if sumOutputs+fee > sumCoins {
		pegin = sumOutputs + fee - sumCoins
	} else {
		fee = sumCoins - sumOutputs
	}

	if !req.DryRun {
		if *keychain.Spend == (mw.SecretKey{}) {
			if s.ledgerTx == nil || s.ledgerTx.Tx == nil {
				s.ledgerTx = &ledger.TxContext{
					Coins:      coins,
					AddrIndex:  addrIndex,
					Recipients: recipients,
					Fee:        fee,
					Pegin:      pegin,
					Pegouts:    pegouts,
				}
				return &proto.CreateResponse{}, nil
			}
			tx.Mweb = s.ledgerTx.Tx
			coins = s.ledgerTx.NewCoins
			s.ledgerTx = nil
		} else {
			tx.Mweb, coins, err = mweb.NewTransaction(
				coins, recipients, fee, pegin, pegouts, nil, nil)
		}
		if err != nil {
			return nil, err
		}
	} else {
		tx.Mweb = &wire.MwebTx{
			TxBody: &wire.MwebTxBody{
				Kernels: []*wire.MwebKernel{{}},
			},
		}
	}

	tx.TxIn = txIns
	tx.TxOut = nil
	if pegin > 0 {
		tx.AddTxOut(mweb.NewPegin(pegin, tx.Mweb.TxBody.Kernels[0].Hash()))
	}

	var buf bytes.Buffer
	if err = tx.Serialize(&buf); err != nil {
		return nil, err
	}

	resp := &proto.CreateResponse{RawTx: buf.Bytes()}
	for _, coin := range coins {
		resp.OutputId = append(resp.OutputId, hex.EncodeToString(coin.OutputId[:]))
	}

	return resp, nil
}

func (s *Server) LedgerExchange(ctx context.Context,
	req *proto.LedgerApdu) (*proto.LedgerApdu, error) {

	if s.ledgerTx == nil {
		return nil, errors.New("nil ledger tx")
	}
	if err := s.ledgerTx.Process(req.Data); err != nil {
		return nil, err
	}
	if s.ledgerTx.Tx != nil {
		return &proto.LedgerApdu{}, nil
	}
	return &proto.LedgerApdu{Data: s.ledgerTx.Request()}, nil
}

func (s *Server) Broadcast(ctx context.Context,
	req *proto.BroadcastRequest) (*proto.BroadcastResponse, error) {

	var tx wire.MsgTx
	if err := tx.Deserialize(bytes.NewReader(req.RawTx)); err != nil {
		return nil, err
	}
	if err := s.cs.SendTransaction(&tx); err != nil {
		return nil, err
	}

	if tx.Mweb != nil {
		var utxos []*wire.MwebNetUtxo
		for _, output := range tx.Mweb.TxBody.Outputs {
			utxos = append(utxos, &wire.MwebNetUtxo{
				Output:   output,
				OutputId: output.Hash(),
			})
		}
		go s.utxoHandler(nil, utxos)
	}

	return &proto.BroadcastResponse{Txid: tx.TxHash().String()}, nil
}
