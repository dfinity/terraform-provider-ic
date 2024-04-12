default: testacc

# Run acceptance tests
.PHONY: testacc
.PHONY: testdata

testacc: testdata
	TF_ACC=1 go test ./... -v $(TESTARGS) -timeout 120m

testdata: test/testdata/canisters/hello_world/hello-world.wasm

test/testdata/canisters/hello_world/hello-world.wasm: test/testdata/canisters/hello_world/src/*
	cd test/testdata/canisters/hello_world && \
		cargo build --target wasm32-unknown-unknown --release && \
		ic-wasm "target/wasm32-unknown-unknown/release/hello-world.wasm" -o "./hello-world.wasm" shrink
