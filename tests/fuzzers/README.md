# Fuzzers

All fuzz targets in this repository use Go's native fuzzing engine
(`testing.F`). Each directory here wraps a legacy go-fuzz entrypoint in a
`fuzz_test.go` so it runs with the standard toolchain; new fuzz targets live
directly in the package they exercise (for example `primitives/rlp`,
`kernel/xhash`, `wallet/keystore`, `p2p/...`).

## Running a fuzzer

```
go test ./tests/fuzzers/trie/ -run '^$' -fuzz '^FuzzTrie$' -fuzztime 60s
```

Go accepts a single `-fuzz` pattern per invocation. The complete list of
fuzz targets is maintained in `build/fuzz-targets.txt`, which the nightly
`fuzz-smoke` CI workflow iterates over; update it in the same commit that
adds or removes a target.

## Seed corpora and crashers

Legacy per-directory `corpus/` files are fed to the wrappers as seeds via
`tests/fuzzers/fuzzutil.SeedFromDir`. When a fuzzing campaign finds a
crasher, commit the minimized input under the target package's
`testdata/fuzz/<FuzzName>/` directory (Go's native corpus location) together
with the fix: it then replays as a regression test under plain `go test`
forever.
