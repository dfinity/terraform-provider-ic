terraform {
  required_providers { ic = { source = "dfinity/ic" } }
}

provider "ic" {}

locals {
  wasm_file = "${path.root}/hello-world.wasm"
}

resource "ic_canister" "hello_world" {

  count = 3

  controllers = [
    "36ds5-4lauy-sjzsw-53yqn-5om5d-pwlxm-bvm6w-hx4pz-dxjnx-tdmm3-zqe",
    "nlkca-5hnlv-i223x-55vw7-ytnje-kbdg5-ulgk4-qjamx-45fvm-m3dbj-5qe"

  ]

  arg = "Hallo"

  wasm_file   = local.wasm_file
  wasm_sha256 = filesha256(local.wasm_file)
}

resource "ic_canister" "hello_world_2" {

  controllers = [
    "36ds5-4lauy-sjzsw-53yqn-5om5d-pwlxm-bvm6w-hx4pz-dxjnx-tdmm3-zqe",
    "nlkca-5hnlv-i223x-55vw7-ytnje-kbdg5-ulgk4-qjamx-45fvm-m3dbj-5qe"
  ]

  arg         = provider::ic::did_record({ greeter = "Hello" })
  wasm_file   = local.wasm_file
  wasm_sha256 = filesha256(local.wasm_file)
}

output "test_foo" {
  value = provider::ic::did_record({
    val = provider::ic::did_text("hello")
  })

}
