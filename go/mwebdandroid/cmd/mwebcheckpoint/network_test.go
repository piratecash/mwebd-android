package main

import (
	"reflect"
	"testing"
)

func TestResolveNetworkHeights_explicitList_sortsAndDeduplicates(t *testing.T) {
	heights, err := resolveNetworkHeights("2600000,2300000,2600000", 0, 0, 50_000)
	if err != nil {
		t.Fatal(err)
	}

	expected := []uint32{2_300_000, 2_600_000}
	if !reflect.DeepEqual(heights, expected) {
		t.Fatalf("unexpected heights: %v", heights)
	}
}

func TestResolveNetworkHeights_interval_generatesInclusiveRange(t *testing.T) {
	heights, err := resolveNetworkHeights("", 2_300_000, 2_410_000, 50_000)
	if err != nil {
		t.Fatal(err)
	}

	expected := []uint32{2_300_000, 2_350_000, 2_400_000}
	if !reflect.DeepEqual(heights, expected) {
		t.Fatalf("unexpected heights: %v", heights)
	}
}

func TestResolveNetworkHeights_tip_returnsTipSentinel(t *testing.T) {
	heights, err := resolveNetworkHeights("tip", 0, 0, 50_000)
	if err != nil {
		t.Fatal(err)
	}

	expected := []uint32{0}
	if !reflect.DeepEqual(heights, expected) {
		t.Fatalf("unexpected heights: %v", heights)
	}
}

func TestResolveNetworkHeights_missingSource_fails(t *testing.T) {
	if _, err := resolveNetworkHeights("", 0, 0, 50_000); err == nil {
		t.Fatal("expected missing height source error")
	}
}
