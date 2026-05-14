package main

import (
	"fmt"
	"io"
	"net/http"
	"time"
)

func doCheckpointRequest(
	httpClient *http.Client,
	sourceName string,
	newRequest func() (*http.Request, error),
) (*http.Response, error) {
	var lastErr error
	for attempt := 1; attempt <= explorerRequestAttempts; attempt++ {
		request, err := newRequest()
		if err != nil {
			return nil, err
		}
		request.Header.Set("User-Agent", explorerUserAgent)

		response, err := httpClient.Do(request)
		if err == nil && !shouldRetryExplorerStatus(response.StatusCode) {
			return response, nil
		}
		if response != nil {
			lastErr = fmt.Errorf("%s returned HTTP %d", sourceName, response.StatusCode)
			_, _ = io.Copy(io.Discard, response.Body)
			_ = response.Body.Close()
		} else {
			lastErr = err
		}
		if attempt < explorerRequestAttempts {
			time.Sleep(time.Duration(attempt) * time.Second)
		}
	}
	return nil, lastErr
}
