resource "ic_canister" "hello_world" {
  arg = "Hello"

  wasm_file   = var.hello_world_wasm
  wasm_sha256 = filesha256(var.hello_world_wasm)
}
