# Terraform Provider for the IC

A [Terraform provider](https://developer.hashicorp.com/terraform/language/providers) for the [Internet Computer](http://internetcomputer.org).


```terraform
resource "ic_canister" "hello_world" {

    wasm_file = "${path.root}/hello-world.wasm"

    arg = { greeter = "Hi" }

    controllers = [ "fgte5-ciaaa-aaaad-aaatq-cai" ]
}
```

For provider usage, visit the [official docs](https://registry.terraform.io/providers/dfinity/ic/latest/docs).

> [!CAUTION]
> `terraform-provider-ic` is under active development and highly experimental.

The rest of this document describes how to **BUILD** the provider.

## Requirements

- [Terraform](https://developer.hashicorp.com/terraform/downloads) >= 1.0
- [Go](https://golang.org/doc/install) >= 1.21

## Building The Provider

1. Clone the repository
1. Enter the repository directory
1. Build the provider using the Go `install` command:

```shell
go install
```

## Developing the Provider

If you wish to work on the provider, you'll first need [Go](http://www.golang.org) installed on your machine (see [Requirements](#requirements) above).

To compile the provider, run `go install`. This will build the provider and put the provider binary in the `$GOPATH/bin` directory.

To generate or update documentation, run `go generate`.

To run the tests, start a local replica with `dfx start` and then run `make`.

## Releasing the provider

Create a tag:

```shell
git tag v0.0.3
```

Push the tag to trigger release creation:

```shell
git push origin v0.0.3
```
