package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"lukechampine.com/blake3"
)

func TestParseRpcExplorerBlock_extractsMwebReplayData(t *testing.T) {
	output := testHash(8).String()
	block, err := parseRpcExplorerBlock(strings.NewReader(rpcExplorerBlockJSON(100, []string{testHash(7).String()}, []string{output}, []byte{0x80})))
	if err != nil {
		t.Fatal(err)
	}

	if block.Height != 100 || block.OutputMMRSize != 1 || block.LeafRoot != leafRootHex([]byte{0x80}) {
		t.Fatalf("unexpected block metadata: %+v", block)
	}
	if !reflect.DeepEqual(block.Inputs, []string{testHash(7).String()}) {
		t.Fatalf("unexpected inputs: %v", block.Inputs)
	}
	if !reflect.DeepEqual(block.Outputs, []string{output}) {
		t.Fatalf("unexpected outputs: %v", block.Outputs)
	}
}

func TestMwebLeafsetReplayer_apply_reconstructsLeafset(t *testing.T) {
	replayer := newMwebLeafsetReplayer()
	firstOutput := testHash(10).String()
	secondOutput := testHash(11).String()
	thirdOutput := testHash(12).String()

	if err := replayer.apply(1, mwebReplayBlock{
		Height:  1,
		Outputs: []string{firstOutput, secondOutput},
	}); err != nil {
		t.Fatal(err)
	}
	if err := replayer.apply(2, mwebReplayBlock{
		Height:  2,
		Inputs:  []string{firstOutput},
		Outputs: []string{thirdOutput},
	}); err != nil {
		t.Fatal(err)
	}

	if !reflect.DeepEqual(replayer.cloneLeafsetBytes(), []byte{0x60}) {
		t.Fatalf("unexpected leafset: %x", replayer.cloneLeafsetBytes())
	}
	if replayer.size() != 3 {
		t.Fatalf("unexpected output MMR size: %d", replayer.size())
	}
	if err := replayer.verifyBlockState(mwebReplayBlock{
		Height:        2,
		OutputMMRSize: 3,
		LeafRoot:      leafRootHex([]byte{0x60}),
	}); err != nil {
		t.Fatal(err)
	}
}

func TestMwebLeafsetReplayer_applyUnknownInput_fails(t *testing.T) {
	replayer := newMwebLeafsetReplayer()

	err := replayer.apply(1, mwebReplayBlock{
		Height: 1,
		Inputs: []string{testHash(10).String()},
	})
	if err == nil {
		t.Fatal("expected unknown input error")
	}
}

func TestMwebLeafsetReplayer_applyIntraBlockSpend_fails(t *testing.T) {
	replayer := newMwebLeafsetReplayer()
	output := testHash(10).String()

	err := replayer.apply(1, mwebReplayBlock{
		Height:  1,
		Inputs:  []string{output},
		Outputs: []string{output},
	})
	if err == nil {
		t.Fatal("expected intra-block spend error")
	}
}

func TestReplayMwebBlocks_missingIndexedEvent_recoversGap(t *testing.T) {
	firstOutput := testHash(1).String()
	secondOutput := testHash(2).String()
	thirdOutput := testHash(3).String()
	blocks := map[string]string{
		"/api/block/1": rpcExplorerBlockJSONWithSize(1, nil, []string{firstOutput}, 1, []byte{0x80}),
		"/api/block/2": rpcExplorerBlockJSONWithSize(2, nil, []string{secondOutput}, 2, []byte{0xc0}),
		"/api/block/3": rpcExplorerBlockJSONWithSize(3, nil, []string{thirdOutput}, 3, []byte{0xe0}),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, ok := blocks[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprint(w, body)
	}))
	defer server.Close()

	client := newMwebReplayClient(server.URL, "", "mainnet", 0)
	snapshots, err := replayMwebBlocks(client, []uint32{3}, []uint32{1, 3})
	if err != nil {
		t.Fatal(err)
	}

	if len(snapshots) != 1 {
		t.Fatalf("unexpected snapshot count: %d", len(snapshots))
	}
	if !reflect.DeepEqual(snapshots[0].leafset, []byte{0xe0}) {
		t.Fatalf("unexpected recovered leafset: %x", snapshots[0].leafset)
	}
}

func TestMwebReplayClient_cachedBlockInvalidHeight_refetchesRemote(t *testing.T) {
	output := testHash(1).String()
	cacheDir := t.TempDir()
	client := newMwebReplayClient("http://127.0.0.1", cacheDir, "mainnet", 0)
	if err := os.MkdirAll(filepath.Dir(client.cachePath(5)), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(client.cachePath(5), []byte(rpcExplorerBlockJSON(4, nil, []string{output}, []byte{0x80})), 0644); err != nil {
		t.Fatal(err)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = fmt.Fprint(w, rpcExplorerBlockJSON(5, nil, []string{output}, []byte{0x80}))
	}))
	defer server.Close()
	client.baseURL = server.URL

	block, err := client.block(5)
	if err != nil {
		t.Fatal(err)
	}
	if block.Height != 5 {
		t.Fatalf("unexpected block height: %d", block.Height)
	}
}

func rpcExplorerBlockJSON(height uint32, inputs []string, outputs []string, leafset []byte) string {
	return rpcExplorerBlockJSONWithSize(height, inputs, outputs, len(outputs), leafset)
}

func rpcExplorerBlockJSONWithSize(
	height uint32,
	inputs []string,
	outputs []string,
	outputMMRSize int,
	leafset []byte,
) string {
	header := testBlockHeader()
	return fmt.Sprintf(
		`{"hash":"%s","height":%d,"version":1,"merkleroot":"%s","previousblockhash":"%s","time":1700000000,"bits":"1d00ffff","nonce":42,"mweb":{"num_txos":%d,"leaf_root":"%s","inputs":%s,"outputs":%s}}`,
		header.BlockHash(),
		height,
		testHash(2),
		testHash(1),
		outputMMRSize,
		leafRootHex(leafset),
		jsonStringArray(inputs),
		jsonStringArray(outputs),
	)
}

func jsonStringArray(values []string) string {
	var builder strings.Builder
	builder.WriteByte('[')
	for index, value := range values {
		if index > 0 {
			builder.WriteByte(',')
		}
		fmt.Fprintf(&builder, `"%s"`, value)
	}
	builder.WriteByte(']')
	return builder.String()
}

func leafRootHex(bits []byte) string {
	sum := blake3.Sum256(bits)
	return fmt.Sprintf("%x", sum[:])
}
