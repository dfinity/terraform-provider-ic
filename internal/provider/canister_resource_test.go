// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aviate-labs/agent-go"
	"github.com/aviate-labs/agent-go/identity"
	"github.com/aviate-labs/agent-go/principal"
	"github.com/hashicorp/terraform-plugin-testing/config"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

/* Creates a PEM file in a temporary directory. */
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

/* Returns the root of the repo. */
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

var VariablesConfig = `
variable "hello_world_wasm" {
    type = string
}

variable "provider_controller" {
    type = string
}
`

/*
	Check that the update call to the canister with the given resource name returns a string with

the expected value.
*/
func checkCanisterReplyString(s *terraform.State, resourceName string, methodName string, args []any, expected string) error {
	rs, ok := s.RootModule().Resources[resourceName]
	if !ok {
		return fmt.Errorf("No canister exists")
	}

	canisterId := rs.Primary.ID

	if canisterId == "" {
		return fmt.Errorf("Canister does not have an ID")
	}

	config, err := LocalhostConfig()
	if err != nil {
		return fmt.Errorf("Could not get config")
	}

	agent, err := agent.New(config)
	if err != nil {
		return fmt.Errorf("Could not create agent: %w", err)
	}

	canisterIdP, err := principal.Decode(canisterId)
	if err != nil {
		return fmt.Errorf("Could not decode principal %s: %w", canisterId, err)
	}

	var result string
	err = agent.Call(canisterIdP, methodName, args, []any{&result})
	if err != nil {
		return fmt.Errorf("Could not call %s on canister %s: %w", methodName, canisterId, err)
	}

	if result != expected {
		return fmt.Errorf("Mismatches reply: %s != %s", result, expected)
	}

	return nil
}

func TestAccCanisterResource(t *testing.T) {
	pemPath, id := CreateTestPEM(t)

	t.Setenv("IC_PEM_IDENTITY_PATH", pemPath)

	configVariables := map[string]config.Variable{}

	/* The path to the test canister used in the terraforming */
	helloWorldWasm := GetHelloWorldWasmPath(t)
	configVariables["hello_world_wasm"] = config.StringVariable(helloWorldWasm)

	/* Use a temporary PEM as identity and inject it into the terraform config */
	providerController := id.Sender().Encode()
	configVariables["provider_controller"] = config.StringVariable(providerController)

	/* We initialize the hello world canister with a greeting, and then call the `hello` method
	 * to make sure the specified greeting is used (i.e. the args are set) */
	greeting := "Salut"
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				ConfigVariables: configVariables,
				Config: VariablesConfig + fmt.Sprintf(`
resource "ic_canister" "test" {
    arg = "%s"
    controllers = [ var.provider_controller ]
    wasm_file = var.hello_world_wasm
    wasm_sha256 = filesha256(var.hello_world_wasm)
}
`, greeting),
				Check: func(s *terraform.State) error {
					greeted := "terraform"
					expected := fmt.Sprintf("%s, %s!", greeting, greeted)
					return checkCanisterReplyString(s, "ic_canister.test", "hello", []any{greeted}, expected)
				},
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func TestAccCanisterResourceEmpty(t *testing.T) {

	pemPath, id := CreateTestPEM(t)

	t.Setenv("IC_PEM_IDENTITY_PATH", pemPath)

	configVariables := map[string]config.Variable{}

	/* The path to the test canister used in the terraforming */
	helloWorldWasm := GetHelloWorldWasmPath(t)
	configVariables["hello_world_wasm"] = config.StringVariable(helloWorldWasm)

	/* Use a temporary PEM as identity and inject it into the terraform config */
	providerController := id.Sender().Encode()
	configVariables["provider_controller"] = config.StringVariable(providerController)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				ConfigVariables: configVariables,
				Config: VariablesConfig + `
resource "ic_canister" "test" {
    controllers = [ var.provider_controller ]
}
`,
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}
