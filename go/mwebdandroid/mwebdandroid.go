package mwebdandroid

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/ltcmweb/mwebd"
	"github.com/ltcmweb/mwebd/proto"
	"google.golang.org/grpc/metadata"
)

const (
	ChainMainnet = "mainnet"
	ChainTestnet = "testnet"
	ChainRegtest = "regtest"

	upstreamVersion = "0.1.19"
)

var (
	artifactVersion = "0.0.0-SNAPSHOT"
	commitSHA       = "unknown"
)

type daemonState uint8

const (
	daemonNew daemonState = iota
	daemonStarting
	daemonRunning
	daemonStopping
	daemonStopped
)

type Daemon struct {
	server        *mwebd.Server
	mu            sync.Mutex
	state         daemonState
	port          int
	ctx           context.Context
	cancel        context.CancelFunc
	operations    sync.WaitGroup
	subscriptions map[*UtxoSubscription]struct{}
	stopOnce      sync.Once
}

func NewDaemon(chain, dataDir, peerAddress, proxyAddress string) (*Daemon, error) {
	return NewDaemonWithRestoreCheckpoint(chain, dataDir, peerAddress, proxyAddress, "")
}

func NewDaemonWithRestoreCheckpoint(chain, dataDir, peerAddress, proxyAddress, restoreCheckpoint string) (*Daemon, error) {
	if err := bootstrapRestoreCheckpoint(dataDir, restoreCheckpoint); err != nil {
		return nil, err
	}

	server, err := mwebd.NewServer2(&mwebd.ServerArgs{
		Chain:     chain,
		DataDir:   dataDir,
		PeerAddr:  peerAddress,
		ProxyAddr: proxyAddress,
	})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Daemon{
		server:        server,
		state:         daemonNew,
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: map[*UtxoSubscription]struct{}{},
	}, nil
}

func AddressesMainnet(scanSecret, spendPubKey []byte, fromIndex, toIndex int64) string {
	return mwebd.Addresses(scanSecret, spendPubKey, int32(fromIndex), int32(toIndex))
}

func Version() string {
	return fmt.Sprintf("ltcmweb/mwebd v%s, mwebd-kmp %s (%s)", upstreamVersion, artifactVersion, commitSHA)
}

func (d *Daemon) Start(port int64) (int64, error) {
	if d == nil || d.server == nil {
		return 0, errors.New("mwebd daemon is not initialized")
	}
	if port < 0 || port > 65535 {
		return 0, errors.New("mwebd port is outside 0..65535")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.state == daemonRunning {
		return int64(d.port), nil
	}
	if d.state != daemonNew {
		return 0, errors.New("mwebd daemon cannot be started in its current state")
	}
	d.state = daemonStarting

	startedPort, err := d.server.Start(int(port))
	if err != nil {
		d.state = daemonStopping
		d.cancel()
		_ = d.server.Stop()
		d.state = daemonStopped
		return 0, err
	}
	d.port = startedPort
	d.state = daemonRunning
	return int64(startedPort), nil
}

func (d *Daemon) Stop() {
	if d == nil || d.server == nil {
		return
	}

	d.stopOnce.Do(func() {
		d.mu.Lock()
		if d.state == daemonStopped {
			d.mu.Unlock()
			return
		}
		d.state = daemonStopping
		d.cancel()
		subscriptions := make([]*UtxoSubscription, 0, len(d.subscriptions))
		for subscription := range d.subscriptions {
			subscriptions = append(subscriptions, subscription)
		}
		d.mu.Unlock()

		for _, subscription := range subscriptions {
			subscription.cancelStream()
		}
		d.operations.Wait()
		_ = d.server.Stop()

		d.mu.Lock()
		d.port = 0
		d.state = daemonStopped
		d.mu.Unlock()
	})
}

func (d *Daemon) Status() (*Status, error) {
	ctx, done, err := d.beginOperation()
	if err != nil {
		return nil, err
	}
	defer done()

	status, err := d.server.Status(ctx, &proto.StatusRequest{})
	if err != nil {
		return nil, err
	}
	return newStatus(status), nil
}

func (d *Daemon) Addresses(scanSecret, spendPubKey []byte, fromIndex, toIndex int64) (*StringList, error) {
	if fromIndex < 0 || toIndex < fromIndex || toIndex > int64(^uint32(0)) {
		return nil, errors.New("invalid address index range")
	}
	ctx, done, err := d.beginOperation()
	if err != nil {
		return nil, err
	}
	defer done()

	response, err := d.server.Addresses(ctx, &proto.AddressRequest{
		ScanSecret:  scanSecret,
		SpendPubkey: spendPubKey,
		FromIndex:   uint32(fromIndex),
		ToIndex:     uint32(toIndex),
	})
	if err != nil {
		return nil, err
	}
	return newStringList(response.Address), nil
}

func (d *Daemon) Spent(outputIdsCsv string) (*StringList, error) {
	ctx, done, err := d.beginOperation()
	if err != nil {
		return nil, err
	}
	defer done()

	outputIds := splitCsv(outputIdsCsv)
	response, err := d.server.Spent(ctx, &proto.SpentRequest{
		OutputId: outputIds,
	})
	if err != nil {
		return nil, err
	}
	return newStringList(response.OutputId), nil
}

func (d *Daemon) Create(rawTx, scanSecret, spendSecret []byte, feeRatePerKb int64, dryRun bool) (*CreateResult, error) {
	if feeRatePerKb < 0 {
		return nil, errors.New("fee rate must not be negative")
	}
	ctx, done, err := d.beginOperation()
	if err != nil {
		return nil, err
	}
	defer done()

	response, err := d.server.Create(ctx, &proto.CreateRequest{
		RawTx:        rawTx,
		ScanSecret:   scanSecret,
		SpendSecret:  spendSecret,
		FeeRatePerKb: uint64(feeRatePerKb),
		DryRun:       dryRun,
	})
	if err != nil {
		return nil, err
	}
	return &CreateResult{
		rawTx:     response.RawTx,
		outputIds: response.OutputId,
	}, nil
}

func (d *Daemon) Broadcast(rawTx []byte) (*BroadcastResult, error) {
	ctx, done, err := d.beginOperation()
	if err != nil {
		return nil, err
	}
	defer done()

	response, err := d.server.Broadcast(ctx, &proto.BroadcastRequest{
		RawTx: rawTx,
	})
	if err != nil {
		return nil, err
	}
	return &BroadcastResult{txId: response.Txid}, nil
}

// copyScanSecret returns a Go-owned copy of the scan secret. gomobile passes
// []byte arguments as transient views over a JNI buffer that is released once
// the bound call returns. SubscribeUtxos hands the secret to a goroutine that
// runs after it returns, so the copy must be made synchronously here, while the
// buffer is still valid — otherwise the goroutine reads freed memory.
func copyScanSecret(scanSecret []byte) []byte {
	owned := make([]byte, len(scanSecret))
	copy(owned, scanSecret)
	return owned
}

func (d *Daemon) SubscribeUtxos(fromHeight int64, scanSecret []byte, listener UtxoListener) (*UtxoSubscription, error) {
	if listener == nil {
		return nil, errors.New("utxo listener is nil")
	}
	if fromHeight < 0 || fromHeight > int64(^uint32(0)>>1) {
		return nil, errors.New("invalid UTXO start height")
	}

	// Copy the transient gomobile buffer now, before the goroutine below uses
	// it past this function's return (see copyScanSecret).
	scanSecret = copyScanSecret(scanSecret)

	d.mu.Lock()
	if d.server == nil || d.state != daemonRunning {
		d.mu.Unlock()
		return nil, errors.New("mwebd daemon is not running")
	}
	ctx, cancel := context.WithCancel(d.ctx)
	subscription := &UtxoSubscription{cancel: cancel, done: make(chan struct{})}
	d.operations.Add(1)
	d.subscriptions[subscription] = struct{}{}
	d.mu.Unlock()

	go func() {
		defer func() {
			d.mu.Lock()
			delete(d.subscriptions, subscription)
			d.mu.Unlock()
			d.operations.Done()
			close(subscription.done)
		}()
		// A panic inside the streaming goroutine (e.g. a runtime fault deep in
		// the daemon) must never abort the host app: surface it as an error.
		defer func() {
			if r := recover(); r != nil {
				log.Printf("mwebdandroid: recovered panic in utxo stream: %v", r)
				listener.OnError(fmt.Sprintf("utxo stream panic: %v", r))
				listener.OnComplete()
			}
		}()
		err := d.server.Utxos(&proto.UtxosRequest{
			FromHeight: int32(fromHeight),
			ScanSecret: scanSecret,
		}, &utxoStream{
			ctx:      ctx,
			listener: listener,
		})
		if err != nil && !errors.Is(err, context.Canceled) {
			listener.OnError(err.Error())
		}
		listener.OnComplete()
	}()

	return subscription, nil
}

func (d *Daemon) beginOperation() (context.Context, func(), error) {
	if d == nil {
		return nil, nil, errors.New("mwebd daemon is not initialized")
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.server == nil || d.state != daemonRunning {
		return nil, nil, errors.New("mwebd daemon is not running")
	}
	d.operations.Add(1)
	return d.ctx, d.operations.Done, nil
}

type Status struct {
	blockHeaderHeight int64
	mwebHeaderHeight  int64
	mwebUtxosHeight   int64
	blockTime         int64
}

func newStatus(status *proto.StatusResponse) *Status {
	return &Status{
		blockHeaderHeight: int64(status.BlockHeaderHeight),
		mwebHeaderHeight:  int64(status.MwebHeaderHeight),
		mwebUtxosHeight:   int64(status.MwebUtxosHeight),
		blockTime:         int64(status.BlockTime),
	}
}

func (s *Status) BlockHeaderHeight() int64 {
	return s.blockHeaderHeight
}

func (s *Status) MwebHeaderHeight() int64 {
	return s.mwebHeaderHeight
}

func (s *Status) MwebUtxosHeight() int64 {
	return s.mwebUtxosHeight
}

func (s *Status) BlockTime() int64 {
	return s.blockTime
}

type Utxo struct {
	height    int64
	value     int64
	address   string
	outputId  string
	blockTime int64
}

func newUtxo(utxo *proto.Utxo) *Utxo {
	return &Utxo{
		height:    int64(utxo.Height),
		value:     int64(utxo.Value),
		address:   utxo.Address,
		outputId:  utxo.OutputId,
		blockTime: int64(utxo.BlockTime),
	}
}

func (u *Utxo) Height() int64 {
	return u.height
}

func (u *Utxo) Value() int64 {
	return u.value
}

func (u *Utxo) Address() string {
	return u.address
}

func (u *Utxo) OutputId() string {
	return u.outputId
}

func (u *Utxo) BlockTime() int64 {
	return u.blockTime
}

type CreateResult struct {
	rawTx     []byte
	outputIds []string
}

func (r *CreateResult) RawTx() []byte {
	return r.rawTx
}

func (r *CreateResult) OutputIds() *StringList {
	return newStringList(r.outputIds)
}

type BroadcastResult struct {
	txId string
}

func (r *BroadcastResult) TxId() string {
	return r.txId
}

type StringList struct {
	values []string
}

func newStringList(values []string) *StringList {
	copied := make([]string, len(values))
	copy(copied, values)
	return &StringList{values: copied}
}

func (l *StringList) Len() int64 {
	return int64(len(l.values))
}

func (l *StringList) Get(index int64) string {
	if index < 0 || index >= int64(len(l.values)) {
		return ""
	}
	return l.values[index]
}

func (l *StringList) Csv() string {
	return strings.Join(l.values, ",")
}

type UtxoListener interface {
	OnUtxo(utxo *Utxo)
	OnReplayComplete(height int64)
	OnError(message string)
	OnComplete()
}

type UtxoSubscription struct {
	cancel context.CancelFunc
	once   sync.Once
	done   chan struct{}
}

func (s *UtxoSubscription) Close() {
	if s == nil {
		return
	}
	s.cancelStream()
	<-s.done
}

func (s *UtxoSubscription) cancelStream() {
	s.once.Do(func() {
		s.cancel()
	})
}

type utxoStream struct {
	ctx      context.Context
	listener UtxoListener
}

func (s *utxoStream) Send(utxo *proto.Utxo) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		if isReplayCompleteSentinel(utxo) {
			s.listener.OnReplayComplete(int64(utxo.ReplayCompleteHeight))
			return nil
		}
		if isMalformedReplayCompleteSentinel(utxo) {
			log.Printf("mwebdandroid: dropping malformed replay-complete UTXO message at height %d", utxo.ReplayCompleteHeight)
			return nil
		}
		s.listener.OnUtxo(newUtxo(utxo))
		return nil
	}
}

func isReplayCompleteSentinel(utxo *proto.Utxo) bool {
	return utxo.ReplayComplete &&
		utxo.Height == 0 &&
		utxo.Value == 0 &&
		utxo.Address == "" &&
		utxo.OutputId == "" &&
		utxo.BlockTime == 0
}

func isMalformedReplayCompleteSentinel(utxo *proto.Utxo) bool {
	return utxo.ReplayComplete && !isReplayCompleteSentinel(utxo)
}

func (s *utxoStream) SetHeader(metadata.MD) error {
	return nil
}

func (s *utxoStream) SendHeader(metadata.MD) error {
	return nil
}

func (s *utxoStream) SetTrailer(metadata.MD) {
}

func (s *utxoStream) Context() context.Context {
	return s.ctx
}

func (s *utxoStream) SendMsg(any) error {
	return nil
}

func (s *utxoStream) RecvMsg(any) error {
	return errors.New("recv is not supported")
}

func splitCsv(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	result := make([]string, 0, len(parts))
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			result = append(result, part)
		}
	}
	return result
}
