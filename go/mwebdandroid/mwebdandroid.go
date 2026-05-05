package mwebdandroid

import (
	"context"
	"errors"
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
)

type Daemon struct {
	server *mwebd.Server
	mu     sync.Mutex
	port   int
}

func NewDaemon(chain, dataDir, peerAddress, proxyAddress string) (*Daemon, error) {
	server, err := mwebd.NewServer2(&mwebd.ServerArgs{
		Chain:     chain,
		DataDir:   dataDir,
		PeerAddr:  peerAddress,
		ProxyAddr: proxyAddress,
	})
	if err != nil {
		return nil, err
	}
	return &Daemon{server: server}, nil
}

func AddressesMainnet(scanSecret, spendPubKey []byte, fromIndex, toIndex int64) string {
	return mwebd.Addresses(scanSecret, spendPubKey, int32(fromIndex), int32(toIndex))
}

func (d *Daemon) Start(port int64) (int64, error) {
	if d == nil || d.server == nil {
		return 0, errors.New("mwebd daemon is not initialized")
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	if d.port != 0 {
		return int64(d.port), nil
	}

	startedPort, err := d.server.Start(int(port))
	if err != nil {
		return 0, err
	}
	d.port = startedPort
	return int64(startedPort), nil
}

func (d *Daemon) Stop() {
	if d == nil || d.server == nil {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.server.Stop()
	d.port = 0
}

func (d *Daemon) Status() (*Status, error) {
	if d == nil || d.server == nil {
		return nil, errors.New("mwebd daemon is not initialized")
	}

	status, err := d.server.Status(context.Background(), &proto.StatusRequest{})
	if err != nil {
		return nil, err
	}
	return newStatus(status), nil
}

func (d *Daemon) Addresses(scanSecret, spendPubKey []byte, fromIndex, toIndex int64) (*StringList, error) {
	if d == nil || d.server == nil {
		return nil, errors.New("mwebd daemon is not initialized")
	}

	response, err := d.server.Addresses(context.Background(), &proto.AddressRequest{
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
	if d == nil || d.server == nil {
		return nil, errors.New("mwebd daemon is not initialized")
	}

	outputIds := splitCsv(outputIdsCsv)
	response, err := d.server.Spent(context.Background(), &proto.SpentRequest{
		OutputId: outputIds,
	})
	if err != nil {
		return nil, err
	}
	return newStringList(response.OutputId), nil
}

func (d *Daemon) Create(rawTx, scanSecret, spendSecret []byte, feeRatePerKb int64, dryRun bool) (*CreateResult, error) {
	if d == nil || d.server == nil {
		return nil, errors.New("mwebd daemon is not initialized")
	}

	response, err := d.server.Create(context.Background(), &proto.CreateRequest{
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
	if d == nil || d.server == nil {
		return nil, errors.New("mwebd daemon is not initialized")
	}

	response, err := d.server.Broadcast(context.Background(), &proto.BroadcastRequest{
		RawTx: rawTx,
	})
	if err != nil {
		return nil, err
	}
	return &BroadcastResult{txId: response.Txid}, nil
}

func (d *Daemon) SubscribeUtxos(fromHeight int64, scanSecret []byte, listener UtxoListener) (*UtxoSubscription, error) {
	if d == nil || d.server == nil {
		return nil, errors.New("mwebd daemon is not initialized")
	}
	if listener == nil {
		return nil, errors.New("utxo listener is nil")
	}

	ctx, cancel := context.WithCancel(context.Background())
	subscription := &UtxoSubscription{cancel: cancel, done: make(chan struct{})}

	go func() {
		defer close(subscription.done)
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
		s.listener.OnUtxo(newUtxo(utxo))
		return nil
	}
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
