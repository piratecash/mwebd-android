package mwebdb

import "github.com/btcsuite/btclog"

var log btclog.Logger

func init() {
	UseLogger(btclog.Disabled)
}

func UseLogger(logger btclog.Logger) {
	log = logger
}
