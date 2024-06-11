package provider

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

const argRecordSummary = "Mark a Terraform value as a candid record"

const argRecordDescription = "See the documentation for `did_encode`."

// Ensure the implementation satisfies the desired interfaces.
var _ function.Function = &ArgRecordFunction{}

type ArgRecordFunction struct{}

func (f *ArgRecordFunction) Metadata(ctx context.Context, req function.MetadataRequest, resp *function.MetadataResponse) {
	resp.Name = "did_record"
}

var didRecordReturnAttrTypes = map[string]attr.Type{
	"__didType":  types.StringType,  /* the string constant "record" */
	"__didValue": types.DynamicType, /* the record value (object) itself */
}

func (f *ArgRecordFunction) Definition(ctx context.Context, req function.DefinitionRequest, resp *function.DefinitionResponse) {

	resp.Definition = function.Definition{
		Summary:             argRecordSummary,
		Description:         argRecordDescription,
		MarkdownDescription: argRecordDescription,

		Parameters: []function.Parameter{
			// XXX: need dynamic parameter because e.g. Map<Dynamic> is not supported
			function.DynamicParameter{
				Name:        "input",
				Description: "The HCL object to candid-encoded as a candid record",
			},
		},
		Return: function.ObjectReturn{
			AttributeTypes: didRecordReturnAttrTypes,
		},
	}
}

func (f *ArgRecordFunction) Run(ctx context.Context, req function.RunRequest, resp *function.RunResponse) {
	var input attr.Value

	// Read Terraform argument data into the variable
	resp.Error = function.ConcatFuncErrors(resp.Error, req.Arguments.Get(ctx, &input))
	if resp.Error != nil {
		return
	}

	wrapped, diags := types.ObjectValue(
		didRecordReturnAttrTypes,
		map[string]attr.Value{
			"__didType":  types.StringValue("record"),
			"__didValue": types.DynamicValue(input),
		},
	)

	resp.Error = function.FuncErrorFromDiags(ctx, diags)
	if resp.Error != nil {
		return
	}

	// Set the result to the same data
	resp.Error = function.ConcatFuncErrors(resp.Error, resp.Result.Set(ctx, wrapped))
}
