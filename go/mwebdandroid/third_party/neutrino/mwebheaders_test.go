package neutrino

import (
	"testing"

	"github.com/ltcmweb/ltcd/wire"
)

func TestMwebHeaderStartHeight_mainnetWithoutLeafset_usesActivationHeight(t *testing.T) {
	height := mwebHeaderStartHeight(wire.MainNet, 0)

	if height != 2_265_984 {
		t.Fatalf("unexpected mainnet start height: %d", height)
	}
}

func TestMwebHeaderStartHeight_mainnetWithLeafset_usesLeafsetHeight(t *testing.T) {
	height := mwebHeaderStartHeight(wire.MainNet, 2_900_000)

	if height != 2_900_000 {
		t.Fatalf("unexpected mainnet start height: %d", height)
	}
}

func TestMwebHeaderStartHeight_mainnetAtActivation_usesActivationHeight(t *testing.T) {
	height := mwebHeaderStartHeight(wire.MainNet, 2_265_984)

	if height != 2_265_984 {
		t.Fatalf("unexpected mainnet start height: %d", height)
	}
}

func TestMwebHeaderStartHeight_mainnetLeafsetBelowActivation_usesActivationHeight(t *testing.T) {
	height := mwebHeaderStartHeight(wire.MainNet, 2_200_000)

	if height != 2_265_984 {
		t.Fatalf("unexpected mainnet start height: %d", height)
	}
}

func TestMwebHeaderStartHeight_testnet4WithoutLeafset_usesActivationHeight(t *testing.T) {
	height := mwebHeaderStartHeight(wire.TestNet4, 0)

	if height != 2_215_584 {
		t.Fatalf("unexpected testnet4 start height: %d", height)
	}
}

func TestMwebHeaderStartHeight_testnetWithoutLeafset_usesActivationHeight(t *testing.T) {
	height := mwebHeaderStartHeight(wire.TestNet, 0)

	if height != 432 {
		t.Fatalf("unexpected testnet start height: %d", height)
	}
}
