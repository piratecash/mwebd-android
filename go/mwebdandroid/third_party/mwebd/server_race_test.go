package mwebd

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/ltcutil/mweb"
	"github.com/ltcmweb/ltcd/ltcutil/mweb/mw"
	"github.com/ltcmweb/ltcd/wire"
	"github.com/ltcmweb/mwebd/proto"
	"github.com/ltcmweb/neutrino"
	"github.com/ltcsuite/ltcwallet/walletdb"
	_ "github.com/ltcsuite/ltcwallet/walletdb/bdb"
	"google.golang.org/grpc/metadata"
)

// fakeCoinDB is a minimal mwebdb.CoinDatabase used to drive Server.Utxos and
// Server.utxoHandler without a real chain. The leafset callback lets each test
// decide whether Utxos runs to the steady-state receive loop (leafset ok) or
// returns early through the deferred cleanup path (leafset error).
type fakeCoinDB struct {
	leafset func() (*mweb.Leafset, error)
}

func (f fakeCoinDB) GetRollbackHeight() (uint32, error)              { return 0, nil }
func (f fakeCoinDB) PutRollbackHeight(uint32) error                 { return nil }
func (f fakeCoinDB) ClearRollbackHeight(uint32) error               { return nil }
func (f fakeCoinDB) GetLeavesAtHeight() (map[uint32]uint64, error)  { return map[uint32]uint64{}, nil }
func (f fakeCoinDB) PutLeavesAtHeight(map[uint32]uint64) error      { return nil }
func (f fakeCoinDB) RollbackLeavesAtHeight(uint32) error            { return nil }
func (f fakeCoinDB) GetLeafset() (*mweb.Leafset, error)             { return f.leafset() }
func (f fakeCoinDB) PutLeafsetAndPurge(*mweb.Leafset, []uint64) error { return nil }
func (f fakeCoinDB) PutCoins([]*wire.MwebNetUtxo) error             { return nil }
func (f fakeCoinDB) FetchCoin(*chainhash.Hash) (*wire.MwebOutput, error) {
	return nil, errors.New("not found")
}
func (f fakeCoinDB) FetchLeaves([]uint64) ([]*wire.MwebNetUtxo, error) { return nil, nil }
func (f fakeCoinDB) PurgeCoins() error                                { return nil }

// raceStream mirrors the real mwebdandroid utxoStream: Send fails once the
// per-subscription context is cancelled, which is how Utxos learns to exit.
type raceStream struct {
	proto.UnimplementedRpcServer
	ctx context.Context
}

func (s *raceStream) Send(*proto.Utxo) error {
	select {
	case <-s.ctx.Done():
		return s.ctx.Err()
	default:
		return nil
	}
}
func (s *raceStream) SetHeader(metadata.MD) error  { return nil }
func (s *raceStream) SendHeader(metadata.MD) error { return nil }
func (s *raceStream) SetTrailer(metadata.MD)       {}
func (s *raceStream) Context() context.Context     { return s.ctx }
func (s *raceStream) SendMsg(any) error            { return nil }
func (s *raceStream) RecvMsg(any) error            { return nil }

func newRaceServer(t *testing.T, leafset func() (*mweb.Leafset, error)) *Server {
	t.Helper()
	db, err := walletdb.Create(
		"bdb", filepath.Join(t.TempDir(), "test.db"), true, time.Minute)
	if err != nil {
		t.Fatalf("create walletdb: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return &Server{
		db:       db,
		cs:       &neutrino.ChainService{MwebCoinDB: fakeCoinDB{leafset: leafset}},
		utxoChan: map[mw.SecretKey]map[*utxoStreamer]struct{}{},
	}
}

// TestUtxos_registerUnregisterChurn_underRace hammers the exact map operations
// blamed by the crash (the inner map[*utxoStreamer]struct{} add at server.go:274
// and delete at server.go:280) from many goroutines, while utxoHandler ranges
// the same map concurrently. GetLeafset always errors so every Utxos call runs
// register -> error -> deferred unregister, with no goroutine leaks.
//
// Hypothesis H2: a concurrent write reaches s.utxoChan despite s.mtx.
// If this passes under -race, the crash-site map ops are correctly serialised.
func TestUtxos_registerUnregisterChurn_underRace(t *testing.T) {
	s := newRaceServer(t, func() (*mweb.Leafset, error) {
		return nil, errors.New("boom")
	})

	var scan [32]byte
	scanBytes := scan[:]

	stop := make(chan struct{})

	// Concurrent readers of s.utxoChan via the real callback paths:
	// one neutrino-style (serialised in prod) and one Broadcast-style
	// (go s.utxoHandler, NOT serialised with the neutrino path in prod).
	var notifiers sync.WaitGroup
	for i := 0; i < 2; i++ {
		notifiers.Add(1)
		go func() {
			defer notifiers.Done()
			lfs := &mweb.Leafset{Bits: []byte{}, Size: 0, Height: 1}
			for {
				select {
				case <-stop:
					return
				default:
				}
				s.utxoHandler(lfs, nil)
			}
		}()
	}

	// Many concurrent subscribers, each repeatedly entering Utxos.
	var subs sync.WaitGroup
	const (
		workers = 12
		rounds  = 300
	)
	for w := 0; w < workers; w++ {
		subs.Add(1)
		go func() {
			defer subs.Done()
			for r := 0; r < rounds; r++ {
				ctx, cancel := context.WithCancel(context.Background())
				_ = s.Utxos(
					&proto.UtxosRequest{ScanSecret: scanBytes},
					&raceStream{ctx: ctx},
				)
				cancel()
			}
		}()
	}

	subs.Wait()
	close(stop)
	notifiers.Wait()
}

// TestUtxos_persistentSubscribersWithNotify_underRace keeps real subscribers
// registered (GetLeafset ok, replay completes, Utxos parks on <-u.ch) while
// utxoHandler delivers notifications, so notify() runs against the same
// utxoStreamer (u.lfs / u.leaves) that Utxos created. Time-bounded; blocked
// Utxos goroutines are an accepted consequence of the daemon's lazy cancel.
//
// Hypothesis H1: a data race on shared utxoStreamer state (u.lfs) between the
// Utxos replay phase and notify().
func TestUtxos_persistentSubscribersWithNotify_underRace(t *testing.T) {
	s := newRaceServer(t, func() (*mweb.Leafset, error) {
		return &mweb.Leafset{Bits: []byte{}, Size: 0, Height: 1}, nil
	})

	var scan [32]byte
	scanBytes := scan[:]

	stop := make(chan struct{})
	var live int32

	var wg sync.WaitGroup

	// Notifiers: neutrino-style (with leafset) + Broadcast-style (nil).
	for i := 0; i < 3; i++ {
		wg.Add(1)
		nilLfs := i == 0
		go func() {
			defer wg.Done()
			lfs := &mweb.Leafset{Bits: []byte{}, Size: 0, Height: 2}
			for {
				select {
				case <-stop:
					return
				default:
				}
				if nilLfs {
					s.utxoHandler(nil, nil)
				} else {
					s.utxoHandler(lfs, nil)
				}
			}
		}()
	}

	// Subscribers register and stay; each parks inside Utxos on <-u.ch.
	const workers = 8
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-stop:
					return
				default:
				}
				atomic.AddInt32(&live, 1)
				ctx, cancel := context.WithCancel(context.Background())
				go func() {
					_ = s.Utxos(
						&proto.UtxosRequest{ScanSecret: scanBytes},
						&raceStream{ctx: ctx},
					)
				}()
				time.Sleep(time.Millisecond)
				cancel()
			}
		}()
	}

	time.Sleep(2 * time.Second)
	close(stop)
	wg.Wait()
}

// TestUtxos_nilUtxoChan_panicsAssignNilMap_matchesCrashSignature reproduces the
// production crash signature. When Utxos runs with a nil s.utxoChan it panics
// with "assignment to entry in nil map" at server.go:272. Because the cleanup
// defer is only registered at server.go:277 — AFTER the s.mtx critical section
// (270-275) — the panic escapes Utxos with no deferred recovery. In the
// recover-less production goroutine (SubscribeUtxos.func1) the same panic is
// unrecovered: gopanic -> fatalpanic -> SIGABRT, exactly the reported stack
// ([libgojni.so] runtime.raise / SIGABRT, mapassign at Utxos server.go:272/274).
func TestUtxos_nilUtxoChan_panicsAssignNilMap_matchesCrashSignature(t *testing.T) {
	s := &Server{ // like NewBareServer: utxoChan is nil
		cs: &neutrino.ChainService{MwebCoinDB: fakeCoinDB{
			leafset: func() (*mweb.Leafset, error) {
				return &mweb.Leafset{Bits: []byte{}, Size: 0, Height: 1}, nil
			},
		}},
	}
	var scan [32]byte
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var recovered any
	func() {
		defer func() { recovered = recover() }()
		_ = s.Utxos(&proto.UtxosRequest{ScanSecret: scan[:]}, &raceStream{ctx: ctx})
	}()

	if recovered == nil {
		t.Fatal("expected a panic to escape Utxos, got none")
	}
	if msg := fmt.Sprint(recovered); !strings.Contains(msg, "assignment to entry in nil map") {
		t.Fatalf("unexpected panic, want assignment to entry in nil map, got: %q", msg)
	}

	// Registration must release s.mtx even when it panics, so a single bad
	// subscription cannot wedge every other subscriber on a leaked lock.
	if !s.mtx.TryLock() {
		t.Fatal("s.mtx leaked: it must be released even when registration panics")
	}
	s.mtx.Unlock()
}

// TestUtxos_copiesScanSecret_immuneToCallerMutation verifies Utxos snapshots the
// caller-owned ScanSecret instead of holding a pointer into it. The reported
// crash (nil-map write at server.go:274) is best explained by *scanSecret
// yielding a different key between the map create (272) and the map write (274),
// which can only happen if the underlying ScanSecret bytes change mid-call.
// Reading the secret once makes the key — and the streamer's retained secret —
// stable regardless of what the caller does with the slice afterwards.
func TestUtxos_copiesScanSecret_immuneToCallerMutation(t *testing.T) {
	s := newRaceServer(t, func() (*mweb.Leafset, error) {
		return &mweb.Leafset{Bits: []byte{}, Size: 0, Height: 1}, nil
	})

	const original byte = 0xAA
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = original
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	go func() {
		_ = s.Utxos(&proto.UtxosRequest{ScanSecret: secret}, &raceStream{ctx: ctx})
	}()

	u := waitForStreamer(t, s)

	// Caller reuses/mutates its own buffer after the call (plausible across the
	// rapid re-subscriptions of the new reconnect logic). Utxos must be immune.
	for i := range secret {
		secret[i] = 0xBB
	}

	var want mw.SecretKey
	for i := range want {
		want[i] = original
	}
	if got := *u.scan; got != want {
		t.Fatalf("Utxos retained the caller's ScanSecret buffer: streamer.scan became %x "+
			"after the caller mutated its slice; want a stable snapshot %x", got, want)
	}
}

// TestUtxos_cancelExitsSteadyStateReceive verifies that cancelling the stream
// context wakes Utxos out of its post-replay receive. Without this, Utxos parks
// on <-u.ch and only a future notify can unblock it, so cancelled subscriptions
// leak their goroutine/streamer/listener on an idle chain — which accumulates
// under the new reconnect/replay churn.
func TestUtxos_cancelExitsSteadyStateReceive(t *testing.T) {
	s := newRaceServer(t, func() (*mweb.Leafset, error) {
		return &mweb.Leafset{Bits: []byte{}, Size: 0, Height: 1}, nil
	})

	secret := make([]byte, 32)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		_ = s.Utxos(&proto.UtxosRequest{ScanSecret: secret}, &raceStream{ctx: ctx})
		close(done)
	}()

	waitForStreamer(t, s)
	// Let Utxos finish the (empty) replay and park in the steady-state receive.
	time.Sleep(100 * time.Millisecond)

	cancel() // no notify will arrive: only context cancellation can wake Utxos

	select {
	case <-done:
		// Exited promptly on cancellation.
	case <-time.After(2 * time.Second):
		t.Fatal("Utxos did not exit after cancel: it parks on <-u.ch and ignores context cancellation")
	}
}

func waitForStreamer(t *testing.T, s *Server) *utxoStreamer {
	t.Helper()
	for i := 0; i < 2000; i++ {
		var found *utxoStreamer
		s.mtx.Lock()
		for _, us := range s.utxoChan {
			for u := range us {
				found = u
			}
		}
		s.mtx.Unlock()
		if found != nil {
			return found
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("no streamer was registered in time")
	return nil
}
