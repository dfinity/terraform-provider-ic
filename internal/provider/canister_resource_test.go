// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/hex"
	"fmt"
	"os"
	"testing"

	"github.com/aviate-labs/agent-go"
	"github.com/aviate-labs/agent-go/ic"
	icMgmt "github.com/aviate-labs/agent-go/ic/ic"
	"github.com/aviate-labs/agent-go/principal"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func LocalhostConfig() (agent.Config, error) {
	return EndpointConfig("http://localhost:4943")
}

func TestAccCanisterResource(t *testing.T) {

	testEnv := NewTestEnv(t)

	helloWorldWithArg := func(arg string, installWasm bool) string {

		config := fmt.Sprintf(`
        resource "ic_canister" "test" {
            arg = "%s"
            controllers = [ var.provider_controller ]
        `, arg)

		if installWasm {
			config += `
            wasm_file = var.hello_world_wasm
            wasm_sha256 = filesha256(var.hello_world_wasm)
        `
		}

		config += "}"

		return config

	}

	greeted := "terraform"

	/* We initialize the hello world canister with a greeting, and then call the `hello` method
	 * to make sure the specified greeting is used (i.e. ensure that the args are set) */
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			// Create an empty canister
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config:          ProviderConfig + VariablesConfig + helloWorldWithArg("", false),
				Check: func(s *terraform.State) error {
					return checkCanisterModuleHash(s, "ic_canister.test", "")
				},
			},
			// Install Wasm + play with args
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config:          ProviderConfig + VariablesConfig + helloWorldWithArg("Salut", true),
				Check: func(s *terraform.State) error {
					expected := fmt.Sprintf("Salut, %s!", greeted)
					return checkCanisterReplyString(s, "ic_canister.test", "hello", []any{greeted}, expected)
				},
			},
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config:          ProviderConfig + VariablesConfig + helloWorldWithArg("Hello", true),
				Check: func(s *terraform.State) error {
					expected := fmt.Sprintf("Hello, %s!", greeted)
					return checkCanisterReplyString(s, "ic_canister.test", "hello", []any{greeted}, expected)
				},
			},
			// Uninstall Wasm
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config:          ProviderConfig + VariablesConfig + helloWorldWithArg("", false),
				Check: func(s *terraform.State) error {
					return checkCanisterModuleHash(s, "ic_canister.test", "")
				},
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func TestAccCanisterResourceMany(t *testing.T) {

	testEnv := NewTestEnv(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config: ProviderConfig + VariablesConfig + `
resource "ic_canister" "test" {
            count = 10
            arg = "Hello-${count.index}"
            wasm_file = var.hello_world_wasm
            wasm_sha256 = filesha256(var.hello_world_wasm)
}
`,
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func TestAccCanisterResourceEmpty(t *testing.T) {

	testEnv := NewTestEnv(t)

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config: ProviderConfig + VariablesConfig + `
resource "ic_canister" "test" {}
`,
				/* Check that a canister with no configuration is initialized with the provider's own principal */
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("ic_canister.test", "controllers.#", "1"),
					resource.TestCheckResourceAttr("ic_canister.test", "controllers.0", testEnv.Identity.Sender().Encode()),
				),
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

func TestAccCanisterResourceImport(t *testing.T) {

	testEnv := NewTestEnv(t)

	canisterId, err := createCanisterFromWasmPath(testEnv.HelloWorldWasmPath)

	if err != nil {
		t.Fatalf("Could not create canister: %s", err.Error())
	}

	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				ConfigVariables: testEnv.ConfigVariables,
				ImportState:     true,
				ImportStateId:   canisterId,
				ResourceName:    "ic_canister.test",

				// Check that the canister was installed
				ImportStateCheck: func(instances []*terraform.InstanceState) error {

					found := false

					for i := 0; i < len(instances); i++ {
						instance := instances[i]
						if instance.ID != canisterId {
							continue
						}

						found = true

						moduleHash := instance.Attributes["wasm_sha256"]

						if moduleHash != testEnv.HelloWorldWasmSha256 {
							return fmt.Errorf("Wasm hash not set")
						}
					}

					if !found {
						return fmt.Errorf("Nope")
					}

					return nil

				},
				Config: ProviderConfig + VariablesConfig + `
resource "ic_canister" "test" {}
`,
			},
			// Delete testing automatically occurs in TestCase
		},
	})
}

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

func createCanisterFromWasmPath(wasmFilePath string) (string, error) {
	config, err := LocalhostConfig()
	if err != nil {
		return "", fmt.Errorf("Could not get config")
	}

	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, config)
	if err != nil {
		return "", fmt.Errorf("Could not create agent: %w", err)
	}

	createCanisterArgs := icMgmt.ProvisionalCreateCanisterWithCyclesArgs{}
	res, err := agent.ProvisionalCreateCanisterWithCycles(createCanisterArgs)
	if err != nil {
		return "", fmt.Errorf("Could not create canister: %w", err)
	}

	wasmModule, err := os.ReadFile(wasmFilePath)
	if err != nil {
		return "", fmt.Errorf("Could not read wasm module: %w", err)
	}
	argRaw := []byte{}

	canisterId := res.CanisterId
	installMode := CanisterInstallModeInstall()

	installCodeArgs := icMgmt.InstallCodeArgs{
		Mode:       installMode,
		CanisterId: canisterId,
		WasmModule: wasmModule,
		Arg:        argRaw,
	}

	err = agent.InstallCode(installCodeArgs)
	if err != nil {
		return "", fmt.Errorf("Could not install code: %w", err)
	}

	return canisterId.Encode(), nil

}

/* Check that the module has of the given canister matches the (hex-encoded) expected value. */
func checkCanisterModuleHash(s *terraform.State, resourceName string, expected string) error {
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

	moduleHash, err := agent.GetCanisterModuleHash(canisterIdP)
	if err != nil {
		return fmt.Errorf("could not get canister module hash: %w", err)
	}

	moduleHashString := hex.EncodeToString(moduleHash)
	if moduleHashString != expected {
		return fmt.Errorf("module hash mismatch: '%s' != '%s'", moduleHashString, expected)
	}

	return nil
}
