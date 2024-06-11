package provider

import (
	"context"
	"encoding/hex"
	"github.com/aviate-labs/agent-go/candid/idl"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
)

const argEncodeSummary = "Encode Terraform values into candid values."

const argEncodeDescription = "The `did_encode` function transforms Terraform values into hex-encoded candid values. It takes a single argument and applies heuristics to generate a candid value.\n" +

	"For primitive values (strings, etc) will be encoded as the equivalent candid type. HCL maps and objects will be encoded as records unless they contain the fields `__didType` or `__didValue`. When those fields are set, `__didValue` is the actual value to be encoded, and `__didType` must be a tag defining the type of the value. These fields however should be treated as implementation details and the various helpers (`did_text`, `did_record`) should be used instead.\n\n" +

	"Here are some equivalences between HCL values and textual candid value:\n\n" +

	"`" + `"hello"` + "` = `" + `("hello")` + "`" + "\n" +
	"`" + `{ foo = "bar" }` + "` = `" + `(record { foo = "bar" })` + "`" + "\n"

// Ensure the implementation satisfies the desired interfaces.
var _ function.Function = &ArgEncodeFunction{}

type ArgEncodeFunction struct{}

func (f *ArgEncodeFunction) Metadata(ctx context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "did_encode"
}

func (f *ArgEncodeFunction) Definition(ctx context.Context, req function.DefinitionRequest, resp *function.DefinitionResponse) {

	resp.Definition = function.Definition{
		Summary:             argEncodeSummary,
		Description:         argEncodeDescription,
		MarkdownDescription: argEncodeDescription,

		Parameters: []function.Parameter{
			function.DynamicParameter{
				Name:        "input",
				Description: "The HCL value to candid-encode",
			},
		},
		Return: function.StringReturn{},
	}
}

func (f *ArgEncodeFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var input attr.Value

	// Read Terraform argument data into the variable
	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &input))
	if resp.Error != nil {
		return
	}

	tfValue, err := input.ToTerraformValue(ctx)
	if err != nil {
		resp.Error = function.NewFuncError(err.Error())
		return
	}

	didValue, err := TFValToCandid(tfValue)
	if err != nil {
		resp.Error = function.NewFuncError(err.Error())
		return
	}

	encoded, err := idl.Marshal([]any{didValue})
	if err != nil {
		resp.Error = function.NewFuncError(err.Error())
		return
	}

	// Set the result to the same data
	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, hex.EncodeToString(encoded)))
}
