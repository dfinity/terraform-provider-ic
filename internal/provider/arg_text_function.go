package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const argTextSummary = "Mark a Terraform value as a candid text"

const argTextDescription = "See the documentation for `did_encode`."

// Ensure the implementation satisfies the desired interfaces.
var _ function.Function = &ArgTextFunction{}

type ArgTextFunction struct{}

func (f *ArgTextFunction) Metadata(ctx context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "did_text"
}

var didTextReturnAttrTypes = map[string]attr.Type{
	"__didType":  types.StringType, /* the string constant "text" */
	"__didValue": types.StringType, /* the string value itself */
}

func (f *ArgTextFunction) Definition(ctx context.Context, req function.DefinitionRequest, resp *function.DefinitionResponse) {

	resp.Definition = function.Definition{
		Summary:             argTextSummary,
		Description:         argTextDescription,
		MarkdownDescription: argTextDescription,

		Parameters: []function.Parameter{
			function.StringParameter{
				Name:        "input",
				Description: "The Terraform string to candid-encode as candid text",
			},
		},
		Return: function.ObjectReturn{
			AttributeTypes: didTextReturnAttrTypes,
		},
	}
}

func (f *ArgTextFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var input string

	// Read Terraform argument data into the variable
	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &input))

	wrapped, diags := types.ObjectValue(
		didTextReturnAttrTypes,
		map[string]attr.Value{
			"__didType":  types.StringValue("text"),
			"__didValue": types.StringValue(input),
		},
	)

	resp.Error = function.FuncErrorFromDiags(ctx, diags)
	if resp.Error != nil {
		return
	}

	// Set the result to the same data
	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, wrapped))
}
