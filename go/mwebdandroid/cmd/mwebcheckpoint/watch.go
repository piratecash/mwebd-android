package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/piratecash/mwebd-android/go/mwebdandroid/internal/checkpoint"
)

func watchLocalCheckpoints(
	ctx context.Context,
	dataDir string,
	outputPath string,
	minHeightStep uint32,
	pollInterval time.Duration,
) error {
	if pollInterval <= 0 {
		return errors.New("-watch-interval must be greater than zero")
	}
	if minHeightStep == 0 {
		return errors.New("-watch-min-step must be greater than zero")
	}

	lines, err := readCheckpointLines(outputPath)
	if err != nil {
		return err
	}
	lastHeight, err := lastCheckpointHeight(lines)
	if err != nil {
		return err
	}

	for {
		appended, height, err := archiveLocalCheckpointAfter(dataDir, outputPath, minHeightStep, lastHeight)
		if err != nil {
			fmt.Fprintf(os.Stderr, "mwebcheckpoint watch: %v\n", err)
		} else if appended {
			lastHeight = height
		}

		timer := time.NewTimer(pollInterval)
		select {
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return nil
		case <-timer.C:
		}
	}
}

func archiveLocalCheckpoint(
	dataDir string,
	outputPath string,
	minHeightStep uint32,
) (bool, error) {
	if minHeightStep == 0 {
		return false, errors.New("-watch-min-step must be greater than zero")
	}

	lines, err := readCheckpointLines(outputPath)
	if err != nil {
		return false, err
	}
	lastHeight, err := lastCheckpointHeight(lines)
	if err != nil {
		return false, err
	}
	appended, _, err := archiveLocalCheckpointAfter(dataDir, outputPath, minHeightStep, lastHeight)
	return appended, err
}

func archiveLocalCheckpointAfter(
	dataDir string,
	outputPath string,
	minHeightStep uint32,
	lastHeight uint32,
) (bool, uint32, error) {
	if minHeightStep == 0 {
		return false, 0, errors.New("-watch-min-step must be greater than zero")
	}

	line, err := exportCheckpoint(dataDir, 0)
	if err != nil {
		return false, 0, err
	}
	candidate, err := checkpoint.Parse(line)
	if err != nil {
		return false, 0, err
	}
	if lastHeight != 0 && candidate.Height < lastHeight+minHeightStep {
		return false, candidate.Height, nil
	}

	if err = appendCheckpointLine(outputPath, line); err != nil {
		return false, 0, err
	}
	return true, candidate.Height, nil
}

func appendCheckpointLine(path string, line string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0644)
	if err != nil {
		return err
	}

	if _, err = file.WriteString(line + "\n"); err != nil {
		_ = file.Close()
		return err
	}
	if err = file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func readCheckpointLines(path string) ([]string, error) {
	bytes, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}

	var lines []string
	for _, line := range strings.Split(string(bytes), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func lastCheckpointHeight(lines []string) (uint32, error) {
	var lastHeight uint32
	for _, line := range lines {
		parsed, err := checkpoint.Parse(line)
		if err != nil {
			return 0, err
		}
		if parsed.Height > lastHeight {
			lastHeight = parsed.Height
		}
	}
	return lastHeight, nil
}
