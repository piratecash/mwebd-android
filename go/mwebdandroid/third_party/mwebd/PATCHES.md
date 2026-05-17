# Local mwebd Patches

Base: `github.com/ltcmweb/mwebd v0.1.19`.

This copy is used through `replace github.com/ltcmweb/mwebd => ./third_party/mwebd`
because the Android wrapper needs one local protocol extension before upstream
provides an equivalent API.

Local changes:

- `proto/mwebd.proto`: adds `Utxo.replay_complete_height` as a control sentinel.
- `proto/mwebd.pb.go`: regenerated protobuf bindings for that field.
- `server.go`: emits the replay-complete sentinel after historical UTXO replay
  and before the live stream loop.
- `server_test.go`: covers sentinel emission.

When upgrading the base dependency, reapply this patch set or replace it with an
upstream/tagged fork that exposes the same replay-complete signal.
