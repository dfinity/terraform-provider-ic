// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"fmt"
	"testing"

	"github.com/aviate-labs/agent-go"
	"github.com/aviate-labs/agent-go/principal"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/terraform"
)

func TestAccCanisterResource(t *testing.T) {

	testEnv := NewTestEnv(t)

	helloWorldWithArg := func(arg string) string {
		return fmt.Sprintf(`
        resource "ic_canister" "test" {
            arg = "%s"
            controllers = [ var.provider_controller ]
            wasm_file = var.hello_world_wasm
            wasm_sha256 = filesha256(var.hello_world_wasm)
        }
        `, arg)
	}

	greeted := "terraform"

	/* We initialize the hello world canister with a greeting, and then call the `hello` method
	 * to make sure the specified greeting is used (i.e. ensure that the args are set) */
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config:          VariablesConfig + helloWorldWithArg("Salut"),
				Check: func(s *terraform.State) error {
					expected := fmt.Sprintf("Salut, %s!", greeted)
					return checkCanisterReplyString(s, "ic_canister.test", "hello", []any{greeted}, expected)
				},
			},
			{
				ConfigVariables: testEnv.ConfigVariables,
				Config:          VariablesConfig + helloWorldWithArg("Hello"),
				Check: func(s *terraform.State) error {
					expected := fmt.Sprintf("Hello, %s!", greeted)
					return checkCanisterReplyString(s, "ic_canister.test", "hello", []any{greeted}, expected)
				},
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
				Config: VariablesConfig + `
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
