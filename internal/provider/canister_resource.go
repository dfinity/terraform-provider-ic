// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/hex"
	"fmt"
	"net/url"
	"os/exec"

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
				MarkdownDescription: "Canister controllers",

				/* the controllers can either be fetched from the replica, or
				   set directly if necessary */
				Computed: true,
				Optional: true,
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

	agent, err := localhostAgent()
	if err != nil {
		resp.Diagnostics.AddError("Agent error", "Cannot set up agent: "+err.Error())
		return
	}

	managementCanister, err := principal.Decode("aaaaa-aa")
	if err != nil {
		resp.Diagnostics.AddError("Unexpected Error", "Cannot decode: "+err.Error())
		return
	}

	var result string
	var provisionalCreateCanisterArgument struct{}
	err = agent.Call(managementCanister, "provisional_create_canister_with_cycles",
		[]any{provisionalCreateCanisterArgument},
		[]any{&result})
	if err != nil {
		resp.Diagnostics.AddError("Unexpected Error", "Cannot create canister: "+err.Error())
		return
	}

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

/*
From the IC management canister:

	type canister_settings = record {
	    controllers : opt vec principal;
	    compute_allocation : opt nat;
	    memory_allocation : opt nat;
	    freezing_threshold : opt nat;
	    reserved_cycles_limit : opt nat;
	};
*/
type CanisterSettings struct {
	Controllers *[]principal.Principal `ic:"controllers" json:"controllers"`
}

/*
From the IC management canister:

	type update_settings_args = record {
	    canister_id : principal;
	    settings : canister_settings;
	    sender_canister_version : opt nat64;
	};
*/
type UpdateSettingsArgs struct {
	CanisterId principal.Principal `ic:"canister_id" json:"canister_id"`
	Settings   CanisterSettings    `ic:"settings" json:"settings"`
}

// An agent for local development
func localhostAgent() (*agent.Agent, error) {
	u, _ := url.Parse("http://localhost:4943")
	config := agent.Config{
		ClientConfig: &agent.ClientConfig{Host: u},
		FetchRootKey: true,
	}

	agent, err := agent.New(config)
	if err != nil {
		return nil, err
	}

	return agent, nil
}

// XXX: this is NOT atomic
func (r *CanisterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data CanisterResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	tflog.Info(ctx, fmt.Sprintf("Updating to new data: %s", data))

	canisterId := data.Id.ValueString()
	controllers := make([]string, len(data.Controllers))
	for i := 0; i < len(data.Controllers); i++ {
		controllers[i] = data.Controllers[i].ValueString()
	}

	err := setCanisterControllers(canisterId, controllers)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update canister: "+err.Error())
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	tflog.Info(ctx, "Done updating canister")
	return
}

func setCanisterControllers(canisterId string, controllers []string) error {
	/* XXX: this is the "correct" way of doing things by using
	        agent-go, but agent-go has a bug that prevents it from
	        calling to the management canister

			agent, err := localhostAgent()
			if err != nil {
				resp.Diagnostics.AddError("Agent error", "Cannot set up agent: "+err.Error())
				return
			}

			canisterId, err := principal.Decode(data.Id.ValueString())
			if err != nil {
				tflog.Error(ctx, "Cannot decode canister ID: "+err.Error())
				return
			}

			controllers := make([]principal.Principal, len(data.Controllers))
			for i := 0; i < len(data.Controllers); i++ {
				controller, err := principal.Decode(data.Controllers[i].ValueString())
				if err != nil {
					tflog.Error(ctx, "Cannot decode controller principal: "+err.Error())
					return
				}
				controllers[i] = controller
			}

			canisterSettings := CanisterSettings{
				Controllers: &controllers,
			}

			updateSettingsArgs := UpdateSettingsArgs{
				CanisterId: canisterId,
				Settings:   canisterSettings,
			}

			tflog.Info(ctx, fmt.Sprintf("Updating canister settings: %s", updateSettingsArgs))

			icManagementCanisterCanisterId, _ := principal.Decode("aaaaa-aa")
			err = agent.Call(icManagementCanisterCanisterId, "update_settings", []any{updateSettingsArgs}, nil)
			if err != nil {
				tflog.Error(ctx, "Could not update canister: "+err.Error())
				resp.Diagnostics.AddError("Client Error", "Could not update canister: "+err.Error())
				return
			}
	*/

	// XXX: workaround: use dfx

	setControllerArgs := make([]string, 2*len(controllers))
	for i := 0; i < len(controllers); i++ {
		setControllerArgs[2*i] = "--set-controller"
		setControllerArgs[2*i+1] = controllers[i]
	}

	var args []string
	args = append(args, "canister")
	args = append(args, "update-settings")
	args = append(args, canisterId)
	args = append(args, setControllerArgs...)

	cmd := exec.Command("dfx", args...)

	err := cmd.Run()

	return err

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

	agent, err := localhostAgent()
	if err != nil {
		resp.Diagnostics.AddError("Agent error", "Cannot set up agent: "+err.Error())
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
