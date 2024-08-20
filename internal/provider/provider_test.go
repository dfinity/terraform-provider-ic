// Copyright (c) DFINITY Foundation

package provider

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aviate-labs/agent-go/identity"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// testAccProtoV6ProviderFactories are used to instantiate a provider during
// acceptance testing. The factory function will be invoked for every Terraform
// CLI command executed to create a provider server to which the CLI can
// reattach.
var testAccProtoV6ProviderFactories = map[string]func() (tfprotov6.ProviderServer, error){
	"ic": providerserver.NewProtocol6WithError(New("test")()),
}

// Makes sure specific examples can be tf applied.
func TestAccExamples(t *testing.T) {

	testEnv := NewTestEnv(t)

	examplesResourcesIcCanister, err := os.ReadFile(path.Join(GetRepoRoot(t), "examples", "resources", "ic_canister", "resource.tf"))

	if err != nil {
		t.Fatal(err.Error())
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config:          ProviderConfig + VariablesConfig + string(examplesResourcesIcCanister),
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

// Struct carrying test-related data.
type TestEnv struct {
	PemPath              string
	Identity             identity.Identity
	ConfigVariables      map[string]config.Variable
	HelloWorldWasmPath   string
	HelloWorldWasmSha256 string
}

// Creates a new test env containing data used in tests.
// NOTE: this sets the IC_PEM_IDENTITY_PATH environment variable to a new identity
// (which is accessible from the TestEnv struct).
func NewTestEnv(t *testing.T) TestEnv {

	pemPath, id := CreateTestPEM(t)

	t.Setenv("IC_PEM_IDENTITY_PATH", pemPath)

	configVariables := map[string]config.Variable{}

	// The path to the test canister used in the terraforming
	helloWorldWasm := GetHelloWorldWasmPath(t)
	configVariables["hello_world_wasm"] = config.StringVariable(helloWorldWasm)

	// Use a temporary PEM as identity and inject it into the terraform config
	providerController := id.Sender().Encode()
	configVariables["provider_controller"] = config.StringVariable(providerController)

	wasmModule, err := os.ReadFile(helloWorldWasm)
	if err != nil {
		t.Fatalf("Could not read wasm module: %s", err.Error())
	}

	wasmSha256Raw := sha256.Sum256(wasmModule)
	wasmSha256 := hex.EncodeToString(wasmSha256Raw[:])
	return TestEnv{
		PemPath:              pemPath,
		Identity:             id,
		ConfigVariables:      configVariables,
		HelloWorldWasmPath:   helloWorldWasm,
		HelloWorldWasmSha256: wasmSha256,
	}
}

// Variables set by `NewTestEnv`.
var VariablesConfig = `
variable "hello_world_wasm" {
    type = string
}

variable "provider_controller" {
    type = string
}
`

// Provider config with local replica.
var ProviderConfig = `
provider "ic" {
    endpoint = "http://localhost:4943"
}
`

// Creates a PEM file in a temporary directory.
func CreateTestPEM(t *testing.T) (string, identity.Identity) {

	id, err := identity.NewRandomEd25519Identity()
	if err != nil {
		t.Fatal(err)
	}

	tmpdir := t.TempDir()
	pemPath := path.Join(tmpdir, "pem")

	data, err := id.ToPEM()
	if err != nil {
		t.Fatal(err)
	}

	err = os.WriteFile(pemPath, data, 0644)
	if err != nil {
		t.Fatal(err)
	}

	return pemPath, id
}

// Returns the root of the repo.
func GetRepoRoot(t *testing.T) string {

	cmdOut, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		t.Fatal(err)
	}

	return strings.TrimSpace(string(cmdOut))
}

func GetHelloWorldWasmPath(t *testing.T) string {

	repoRoot := GetRepoRoot(t)

	helloWorldWasm, err := filepath.Abs(path.Join(repoRoot, "test/testdata/canisters/hello_world/hello-world.wasm"))
	if err != nil {
		t.Fatalf("Could not read absolute path of test Wasm module")
	}

	return helloWorldWasm
}
