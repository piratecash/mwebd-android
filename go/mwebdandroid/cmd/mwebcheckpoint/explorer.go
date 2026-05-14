package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/ltcmweb/ltcd/chaincfg/chainhash"
	"github.com/ltcmweb/ltcd/wire"
)

const (
	explorerPageSize        = 1000
	explorerRequestAttempts = 3
	explorerUserAgent       = "mwebcheckpoint/1.0"
)

var hexHashPattern = regexp.MustCompile(`(?i)[0-9a-f]{64}`)

type mwebExplorerClient struct {
	networkName string
	baseURL     string
	cacheDir    string
	httpClient  *http.Client
}

type explorerBlockSummary struct {
	Hash         string `json:"hash"`
	Height       uint32 `json:"height"`
	KernelCount  int    `json:"kernelCount"`
	PegInCount   int    `json:"pegInCount"`
	PegOutCount  int    `json:"pegOutCount"`
	MwebInCount  int    `json:"mWebInCount"`
	MwebOutCount int    `json:"mwebOutCount"`
}

type explorerBlocksResponse struct {
	RecordsFiltered int                    `json:"recordsFiltered"`
	Data            []explorerBlockSummary `json:"data"`
}

func newMwebExplorerClient(networkName string, baseURL string, cacheDir string, timeout time.Duration) *mwebExplorerClient {
	return &mwebExplorerClient{
		networkName: networkName,
		baseURL:     strings.TrimRight(baseURL, "/"),
		cacheDir:    cacheDir,
		httpClient:  &http.Client{Timeout: timeout},
	}
}

func resolveExplorerHeights(
	explorer *mwebExplorerClient,
	heights []uint32,
	interval uint32,
	safeDepth uint32,
	startHeight uint32,
) ([]uint32, error) {
	if interval == 0 {
		return nil, errors.New("checkpoint interval must be greater than zero")
	}

	if len(heights) == 1 && heights[0] == 0 {
		latestHeight, err := explorer.latestHeight()
		if err != nil {
			return nil, err
		}
		if latestHeight <= safeDepth || latestHeight-safeDepth < startHeight {
			return nil, errors.New("safe tip is before MWEB activation")
		}
		safeHeight := latestHeight - safeDepth
		return []uint32{safeHeight - safeHeight%interval}, nil
	}

	heights = sortedUniqueHeights(heights)
	if len(heights) == 0 {
		return nil, errors.New("no checkpoint heights requested")
	}
	if heights[0] < startHeight {
		return nil, fmt.Errorf("checkpoint height %d is before MWEB activation %d", heights[0], startHeight)
	}
	return heights, nil
}

func (c *mwebExplorerClient) latestHeight() (uint32, error) {
	response, err := c.blocksPage(0, 1, false)
	if err != nil {
		return 0, err
	}
	if len(response.Data) == 0 {
		return 0, errors.New("MWEB Explorer returned no blocks")
	}
	return response.Data[0].Height, nil
}

func (c *mwebExplorerClient) blockHashes(heights []uint32) (map[uint32]chainhash.Hash, error) {
	hashes := make(map[uint32]chainhash.Hash, len(heights))
	for _, height := range sortedUniqueHeights(heights) {
		hashValue, err := c.directBlockHash(height)
		if err != nil {
			return nil, err
		}
		hash, err := chainhash.NewHashFromStr(hashValue)
		if err != nil {
			return nil, fmt.Errorf("invalid block hash at height %d: %w", height, err)
		}
		hashes[height] = *hash
	}
	return hashes, nil
}

func (c *mwebExplorerClient) directBlockHash(height uint32) (string, error) {
	response, err := doCheckpointRequest(c.httpClient, "MWEB checkpoint source", func() (*http.Request, error) {
		return http.NewRequest(http.MethodGet, fmt.Sprintf("%s/blocks/block/%d", c.baseURL, height), nil)
	})
	if err != nil {
		return "", err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return "", fmt.Errorf("MWEB Explorer returned HTTP %d", response.StatusCode)
	}

	bytes, err := io.ReadAll(response.Body)
	if err != nil {
		return "", err
	}
	hash, ok := parseDirectBlockHash(string(bytes))
	if !ok {
		return "", errors.New("missing Litecoin block hash")
	}
	return hash, nil
}

func parseDirectBlockHash(html string) (string, bool) {
	index := strings.Index(html, "Litecoin Block Hash:")
	if index < 0 {
		return "", false
	}
	end := index + 1000
	if end > len(html) {
		end = len(html)
	}
	match := hexHashPattern.FindString(html[index:end])
	if match == "" {
		return "", false
	}
	return strings.ToLower(match), true
}

func (c *mwebExplorerClient) nonEmptyHeights(minHeight uint32, maxHeight uint32) ([]uint32, error) {
	if heights, err := c.readCachedNonEmptyHeights(minHeight, maxHeight); err == nil {
		fmt.Fprintf(os.Stderr, "MWEB Explorer non-empty scan loaded from cache: matched=%d\n", len(heights))
		return heights, nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return nil, err
	}

	fmt.Fprintf(os.Stderr, "fetching MWEB Explorer non-empty block heights: %d..%d\n", minHeight, maxHeight)
	offset, err := c.firstOffsetAtOrBelow(maxHeight)
	if err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "MWEB Explorer non-empty scan starts at offset %d\n", offset)

	var heights []uint32
	pages := 0
	for ; ; offset += explorerPageSize {
		// Explorer's non-empty filter has missed leafset-affecting blocks;
		// scan summaries and let replay verification recover any remaining gaps.
		response, err := c.blocksPage(offset, explorerPageSize, false)
		if err != nil {
			return nil, err
		}
		pages++
		if len(response.Data) == 0 {
			break
		}

		minPageHeight := response.Data[0].Height
		maxPageHeight := response.Data[0].Height
		previousHeight := response.Data[0].Height
		for _, block := range response.Data {
			if block.Height < minPageHeight {
				minPageHeight = block.Height
			}
			if block.Height > maxPageHeight {
				maxPageHeight = block.Height
			}
			if block.Height > previousHeight {
				return nil, errors.New("MWEB Explorer returned blocks out of descending order")
			}
			previousHeight = block.Height
			if block.Height >= minHeight && block.Height <= maxHeight && block.hasMwebEvents() {
				heights = append(heights, block.Height)
			}
		}
		if pages == 1 || pages%25 == 0 {
			fmt.Fprintf(
				os.Stderr,
				"MWEB Explorer non-empty scan: page=%d offset=%d range=%d..%d matched=%d\n",
				pages,
				offset,
				minPageHeight,
				maxPageHeight,
				len(heights),
			)
		}
		if maxPageHeight < minHeight || offset+len(response.Data) >= response.RecordsFiltered {
			break
		}
	}

	sort.Slice(heights, func(i, j int) bool { return heights[i] < heights[j] })
	fmt.Fprintf(os.Stderr, "MWEB Explorer non-empty scan complete: matched=%d pages=%d\n", len(heights), pages)
	if err = c.writeCachedNonEmptyHeights(minHeight, maxHeight, heights); err != nil {
		return nil, err
	}
	return heights, nil
}

func (c *mwebExplorerClient) firstOffsetAtOrBelow(maxHeight uint32) (int, error) {
	fmt.Fprintf(os.Stderr, "locating MWEB Explorer offset at or below height %d\n", maxHeight)
	response, err := c.blocksPage(0, 1, false)
	if err != nil {
		return 0, err
	}
	if len(response.Data) == 0 || response.Data[0].Height <= maxHeight {
		return 0, nil
	}

	low := 0
	high := response.RecordsFiltered - 1
	result := response.RecordsFiltered
	for low <= high {
		mid := low + (high-low)/2
		page, err := c.blocksPage(mid, 1, false)
		if err != nil {
			return 0, err
		}
		if len(page.Data) == 0 {
			break
		}
		if page.Data[0].Height > maxHeight {
			low = mid + 1
			continue
		}
		result = mid
		high = mid - 1
	}
	if result == response.RecordsFiltered {
		return response.RecordsFiltered, nil
	}
	return result, nil
}

func (b explorerBlockSummary) hasMwebEvents() bool {
	return b.PegInCount > 0 ||
		b.PegOutCount > 0 ||
		b.MwebInCount > 0 ||
		b.MwebOutCount > 0
}

func (c *mwebExplorerClient) blocksPage(offset int, length int, nonEmpty bool) (*explorerBlocksResponse, error) {
	form := url.Values{}
	form.Set("draw", "1")
	form.Set("start", fmt.Sprintf("%d", offset))
	form.Set("length", fmt.Sprintf("%d", length))
	form.Set("order[0][column]", "0")
	form.Set("order[0][dir]", "desc")
	form.Set("columns[0][data]", "height")
	if nonEmpty {
		form.Set("filter", "he")
	} else {
		form.Set("filter", "")
	}

	response, err := doCheckpointRequest(c.httpClient, "MWEB checkpoint source", func() (*http.Request, error) {
		request, err := http.NewRequest(
			http.MethodPost,
			c.baseURL+"/api/mwebblocks",
			strings.NewReader(form.Encode()),
		)
		if err != nil {
			return nil, err
		}
		request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		return request, nil
	})
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		return nil, fmt.Errorf("MWEB Explorer returned HTTP %d", response.StatusCode)
	}

	var result explorerBlocksResponse
	if err = json.NewDecoder(response.Body).Decode(&result); err != nil {
		return nil, err
	}
	return &result, nil
}

func (c *mwebExplorerClient) readCachedNonEmptyHeights(minHeight uint32, maxHeight uint32) ([]uint32, error) {
	path, err := c.nonEmptyHeightsCachePath(minHeight, maxHeight)
	if err != nil {
		return nil, err
	}
	bytes, err := os.ReadFile(path)
	if err != nil {
		return c.readCachedNonEmptyHeightsFromSuperset(minHeight, maxHeight)
	}
	return parseCachedNonEmptyHeights(bytes, minHeight, maxHeight)
}

func (c *mwebExplorerClient) readCachedNonEmptyHeightsFromSuperset(
	minHeight uint32,
	maxHeight uint32,
) ([]uint32, error) {
	paths, err := filepath.Glob(filepath.Join(c.networkCacheDir(), "events-*-*.txt"))
	if err != nil {
		return nil, err
	}
	for _, path := range paths {
		cachedMin, cachedMax, ok := parseNonEmptyHeightsCacheName(filepath.Base(path))
		if !ok || cachedMin > minHeight || cachedMax < maxHeight {
			continue
		}
		bytes, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		return parseCachedNonEmptyHeights(bytes, minHeight, maxHeight)
	}
	return nil, os.ErrNotExist
}

func (c *mwebExplorerClient) writeCachedNonEmptyHeights(minHeight uint32, maxHeight uint32, heights []uint32) error {
	path, err := c.nonEmptyHeightsCachePath(minHeight, maxHeight)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}

	var builder strings.Builder
	for _, height := range heights {
		fmt.Fprintf(&builder, "%d\n", height)
	}
	return os.WriteFile(path, []byte(builder.String()), 0644)
}

func (c *mwebExplorerClient) nonEmptyHeightsCachePath(minHeight uint32, maxHeight uint32) (string, error) {
	if c.cacheDir == "" {
		return "", os.ErrNotExist
	}
	return filepath.Join(c.networkCacheDir(), fmt.Sprintf("events-%d-%d.txt", minHeight, maxHeight)), nil
}

func (c *mwebExplorerClient) networkCacheDir() string {
	networkName := c.networkName
	if networkName == "" {
		networkName = "unknown"
	}
	return filepath.Join(c.cacheDir, networkName)
}

func parseCachedNonEmptyHeights(bytes []byte, minHeight uint32, maxHeight uint32) ([]uint32, error) {
	var heights []uint32
	for _, line := range strings.Split(string(bytes), "\n") {
		value := strings.TrimSpace(line)
		if value == "" {
			continue
		}
		height, err := strconv.ParseUint(value, 10, 32)
		if err != nil {
			return nil, err
		}
		if uint32(height) >= minHeight && uint32(height) <= maxHeight {
			heights = append(heights, uint32(height))
		}
	}
	return sortedUniqueHeights(heights), nil
}

func parseNonEmptyHeightsCacheName(name string) (uint32, uint32, bool) {
	match := regexp.MustCompile(`^events-([0-9]+)-([0-9]+)\.txt$`).FindStringSubmatch(name)
	if len(match) != 3 {
		return 0, 0, false
	}
	minHeight, minErr := strconv.ParseUint(match[1], 10, 32)
	maxHeight, maxErr := strconv.ParseUint(match[2], 10, 32)
	if minErr != nil || maxErr != nil {
		return 0, 0, false
	}
	return uint32(minHeight), uint32(maxHeight), true
}

func shouldRetryExplorerStatus(statusCode int) bool {
	return statusCode == http.StatusRequestTimeout ||
		statusCode == http.StatusTooManyRequests ||
		statusCode >= http.StatusInternalServerError
}

func mwebActivationStartHeight(networkName string) (uint32, error) {
	params, err := networkParams(networkName)
	if err != nil {
		return 0, err
	}

	switch params.Net {
	case wire.MainNet:
		return 2265984, nil
	case wire.TestNet4:
		return 2215584, nil
	case wire.TestNet:
		return 432, nil
	default:
		return 0, fmt.Errorf("unsupported MWEB network: %s", networkName)
	}
}
