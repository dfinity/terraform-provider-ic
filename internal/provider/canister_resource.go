// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/hex"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/aviate-labs/agent-go"
	"github.com/aviate-labs/agent-go/principal"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &CanisterResource{}
var _ resource.ResourceWithImportState = &CanisterResource{}

func NewCanisterResource() resource.Resource {
	return &CanisterResource{}
}

// CanisterResource defines the resource implementation.
type CanisterResource struct {
	agent *agent.Agent
}

// CanisterResourceModel describes the resource data model.
type CanisterResourceModel struct {
	Id          types.String   `tfsdk:"id"`
	ModuleHash  types.String   `tfsdk:"module_hash"`
	Controllers []types.String `tfsdk:"controllers"`
}

func (r *CanisterResource) Metadata(ctx context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_canister"
}

func (r *CanisterResource) Schema(ctx context.Context, req resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		// This description is used by the documentation generator and the language server.
		MarkdownDescription: "Canister resource",

		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Canister identifier",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"module_hash": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Canister Wasm module hash",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"controllers": schema.ListAttribute{
				ElementType:         types.StringType,
				Computed:            true,
				MarkdownDescription: "Canister controllers",
			},
		},
	}
}

func (r *CanisterResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	// Prevent panic if the provider has not been configured.
	if req.ProviderData == nil {
		return
	}

	agent, ok := req.ProviderData.(*agent.Agent)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *agent.Agent, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.agent = agent
}

func (r *CanisterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	resp.Diagnostics.AddError("Client Error", "Creating canisters is not supported yet")
	return
}

func (r *CanisterResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var data CanisterResourceModel
	tflog.Info(ctx, "Reading canister")

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (r *CanisterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError("Client Error", "Updating canisters is not supported yet")
	return
}

func (r *CanisterResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	resp.Diagnostics.AddError("Client Error", "Deleting canisters is not supported yet")
	return
}

func (r *CanisterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	tflog.Info(ctx, "Importing canister with ID: "+req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)

	tflog.Info(ctx, "Decoding principal")
	principal, err := principal.Decode(req.ID)
	if err != nil {
		tflog.Error(ctx, "Cannot decode principal")
		return
	}

	// NOTE: This calls mainnet
	agent, err := agent.New(agent.DefaultConfig)
	if err != nil {
		tflog.Error(ctx, "Cannot set up agent: "+err.Error())
		return
	}

	tflog.Info(ctx, "Reading canister module hash for "+principal.String())
	moduleHash, err := agent.GetCanisterModuleHash(principal)
	if err != nil {
		tflog.Error(ctx, "Cannot get canister module hash")
		return
	}

	tflog.Info(ctx, "encoding module hash")
	moduleHashString := hex.EncodeToString(moduleHash)
	if err != nil {
		tflog.Error(ctx, "Cannot decode canister module hash")
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("module_hash"), moduleHashString)...)

	tflog.Info(ctx, "Reading canister controllers for "+principal.String())
	controllers, err := agent.GetCanisterControllers(principal)
	if err != nil {
		tflog.Error(ctx, "Cannot get canister controllers")
		return
	}

	controller_principals := make([]string, len(controllers))
	tflog.Info(ctx, "Decoding controller principals")
	for i := 0; i < len(controllers); i++ {
		controller_principals[i] = controllers[i].Encode()
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("controllers"),
		controller_principals)...)

}
