package provider

import (
	"encoding/hex"
	"fmt"
	"testing"

	"github.com/aviate-labs/agent-go/candid"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/knownvalue"
	"github.com/hashicorp/terraform-plugin-testing/statecheck"
	"github.com/hashicorp/terraform-plugin-testing/tfversion"
)

// Checks that our provider's encoding function (tf -> candid) is equivalent
// to (the agent-go implementation of) didc.
func TestEncodeFunction_DidcEquivalence(t *testing.T) {
	t.Parallel()

	// Some HCL/candid pairs that should be equivalent (meaning: same encoding)
	goldens := []struct {
		hcl    string
		candid string
	}{
		{hcl: `"test-value"`, candid: `("test-value")`},
		{hcl: `provider::ic::did_text("test-value")`, candid: `("test-value")`},
		{hcl: `provider::ic::did_record({ param = "val" })`, candid: `(record { param = "val" })`},
		{hcl: `{ greeter = "hello" }`, candid: `(record { greeter = "hello" })`},
		{
			hcl:    `{ param1 = "val1", param2 = { param21 = "innerVal" } }`,
			candid: `(record { param1 = "val1"; param2 = record { param21 = "innerVal" } })`,
		},
	}

	testSteps := make([]resource.TestStep, len(goldens))

	// For each golden test, create a test step that checks the
	// result of the terraform encoding function against the didc (agent-go)
	// equivalent.
	// Each test step is simply an HCL config that calls the encode tf value
	// and reads the config value output.
	for i := 0; i < len(goldens); i++ {

		encoded, err := candid.EncodeValueString(goldens[i].candid)
		if err != nil {
			t.Fatalf("Nope %s", err.Error())
		}
		didStr := hex.EncodeToString(encoded)
		hcl := fmt.Sprintf(`
                output "test" {
                    value = provider::ic::did_encode(%s)
                }`,
			goldens[i].hcl,
		)

		testSteps[i] = resource.TestStep{
			Config: hcl,
			ConfigStateChecks: []statecheck.StateCheck{
				statecheck.ExpectKnownOutputValue("test", knownvalue.StringExact(didStr)),
			},
		}
	}

	resource.UnitTest(t, resource.TestCase{
		TerraformVersionChecks: []tfversion.TerraformVersionCheck{
			// Provider functions are only supports in 1.8.0+
			tfversion.SkipBelow(tfversion.Version1_8_0),
		},
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps:                    testSteps,
	})
}
