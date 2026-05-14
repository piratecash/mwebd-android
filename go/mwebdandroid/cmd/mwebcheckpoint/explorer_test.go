package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"testing"
	"time"
)

func TestResolveExplorerHeights_tipSentinel_usesSafeIntervalHeight(t *testing.T) {
	server := explorerSummaryServer(t, []explorerBlockSummary{{
		Hash:   testHash(1).String(),
		Height: 3_106_709,
	}})
	defer server.Close()
	explorer := newMwebExplorerClient("mainnet", server.URL, "", time.Second)

	heights, err := resolveExplorerHeights(explorer, []uint32{0}, 50_000, 8064, 2_265_984)
	if err != nil {
		t.Fatal(err)
	}

	expected := []uint32{3_050_000}
	if !reflect.DeepEqual(heights, expected) {
		t.Fatalf("unexpected heights: %v", heights)
	}
}

func TestMwebExplorerClient_blockHashes_readsRequestedDirectPages(t *testing.T) {
	server := directBlockHashServer(t, map[string]string{
		"/blocks/block/102": testHash(3).String(),
		"/blocks/block/105": testHash(1).String(),
	})
	defer server.Close()
	explorer := newMwebExplorerClient("mainnet", server.URL, "", time.Second)

	hashes, err := explorer.blockHashes([]uint32{102, 105})
	if err != nil {
		t.Fatal(err)
	}

	if hashes[102] != testHash(3) {
		t.Fatal("unexpected hash for height 102")
	}
	if hashes[105] != testHash(1) {
		t.Fatal("unexpected hash for height 105")
	}
}

func TestMwebExplorerClient_blockHashes_missingHeight_fails(t *testing.T) {
	server := directBlockHashServer(t, nil)
	defer server.Close()
	explorer := newMwebExplorerClient("mainnet", server.URL, "", time.Second)

	if _, err := explorer.blockHashes([]uint32{104}); err == nil {
		t.Fatal("expected missing height error")
	}
}

func TestMwebExplorerClient_nonEmptyHeights_usesSummaryCounts(t *testing.T) {
	server := explorerSummaryServer(t, []explorerBlockSummary{
		{Hash: testHash(1).String(), Height: 105},
		{Hash: testHash(2).String(), Height: 104, MwebInCount: 1, MwebOutCount: 1},
		{Hash: testHash(3).String(), Height: 103},
		{Hash: testHash(4).String(), Height: 102, KernelCount: 1},
	})
	defer server.Close()
	explorer := newMwebExplorerClient("mainnet", server.URL, "", time.Second)

	heights, err := explorer.nonEmptyHeights(102, 105)
	if err != nil {
		t.Fatal(err)
	}

	expected := []uint32{104}
	if !reflect.DeepEqual(heights, expected) {
		t.Fatalf("unexpected heights: %v", heights)
	}
}

func TestParseDirectBlockHash_missingLabel_fails(t *testing.T) {
	if _, ok := parseDirectBlockHash(`<span>Kernel Root: <strong>0123</strong></span>`); ok {
		t.Fatal("expected missing direct block hash")
	}
}

func explorerSummaryServer(t *testing.T, blocks []explorerBlockSummary) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/mwebblocks" {
			http.NotFound(w, r)
			return
		}
		if r.Header.Get("User-Agent") != explorerUserAgent {
			t.Fatalf("unexpected user agent: %s", r.Header.Get("User-Agent"))
		}
		if err := r.ParseForm(); err != nil {
			t.Fatal(err)
		}
		if r.Form.Get("order[0][dir]") != "desc" {
			t.Fatalf("missing desc order: %s", r.Form.Encode())
		}

		offset, length := formInt(t, r, "start"), formInt(t, r, "length")
		end := offset + length
		if end > len(blocks) {
			end = len(blocks)
		}
		if offset > len(blocks) {
			offset = len(blocks)
		}

		_, _ = fmt.Fprintf(w, `{"recordsFiltered":%d,"data":[`, len(blocks))
		for index, block := range blocks[offset:end] {
			if index > 0 {
				_, _ = fmt.Fprint(w, ",")
			}
			_, _ = fmt.Fprintf(
				w,
				`{"hash":"%s","height":%d,"kernelCount":%d,"pegInCount":%d,"pegOutCount":%d,"mWebInCount":%d,"mwebOutCount":%d}`,
				block.Hash,
				block.Height,
				block.KernelCount,
				block.PegInCount,
				block.PegOutCount,
				block.MwebInCount,
				block.MwebOutCount,
			)
		}
		_, _ = fmt.Fprint(w, "]}")
	}))
}

func directBlockHashServer(t *testing.T, hashesByPath map[string]string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") != explorerUserAgent {
			t.Fatalf("unexpected user agent: %s", r.Header.Get("User-Agent"))
		}
		hash, ok := hashesByPath[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = fmt.Fprintf(w, `Litecoin Block Hash: <strong><a>%s</a></strong>`, hash)
	}))
}

func formInt(t *testing.T, request *http.Request, name string) int {
	t.Helper()
	value, err := strconv.Atoi(request.Form.Get(name))
	if err != nil {
		t.Fatal(err)
	}
	return value
}
