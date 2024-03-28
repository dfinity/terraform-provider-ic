// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/aviate-labs/agent-go"
	"github.com/aviate-labs/agent-go/candid/idl"
	"github.com/aviate-labs/agent-go/ic"
	icMgmt "github.com/aviate-labs/agent-go/ic/ic"
	"github.com/aviate-labs/agent-go/principal"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &CanisterResource{}
var _ resource.ResourceWithImportState = &CanisterResource{}
var _ resource.ResourceWithConfigValidators = &CanisterResource{}

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
	Arg         types.Dynamic  `tfsdk:"arg"`
	ArgHex      types.String   `tfsdk:"arg_hex"`     /* Hex-represented didc-encoded arguments */
	WasmFile    types.String   `tfsdk:"wasm_file"`   /* path to Wasm module */
	WasmSha256  types.String   `tfsdk:"wasm_sha256"` /* base64-encoded Wasm module */
}

func (r CanisterResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		/* Exactly one of arg & arg_hex must be specified.
		   XXX: we currently don't support _not_ setting an argument */
		resourcevalidator.ExactlyOneOf(
			path.MatchRoot("arg"),
			path.MatchRoot("arg_hex"),
		),
	}
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
			"arg": schema.DynamicAttribute{
				Optional: true,

				MarkdownDescription: "Init & post_upgrade arguments for the canister. Heuristics are used to convert it to candid.",
			},
			"arg_hex": schema.StringAttribute{
				Optional: true,

				MarkdownDescription: "Hex representation of candid-encoded arguments",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"wasm_file": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Path to Wasm module to install",
			},
			"wasm_sha256": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Sha256 sum of Wasm module (hex encoded)",
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

func localhostConfig() agent.Config {

	u, _ := url.Parse("http://localhost:4943")
	config := agent.Config{
		ClientConfig: &agent.ClientConfig{Host: u},
		FetchRootKey: true,
	}

	return config
}

// An agent for local development
func localhostAgent() (*agent.Agent, error) {
	config := localhostConfig()

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

	/* Controllers */

	controllers := make([]string, len(data.Controllers))
	for i := 0; i < len(data.Controllers); i++ {
		controllers[i] = data.Controllers[i].ValueString()
	}

	err := setCanisterControllers(canisterId, controllers)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update controllers: "+err.Error())
		return
	}

	/* Code install & args */

	argHex, err := data.GetArgHex(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not read argument: "+err.Error())
		return
	}

	wasmFile := data.WasmFile.ValueString()
	wasmSha256 := data.WasmSha256.ValueString()

	err = setCanisterCode(canisterId, argHex, wasmFile, wasmSha256)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update code: "+err.Error())
		return
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	tflog.Info(ctx, "Done updating canister")
	return
}

// Returns the candid argument, hex-encoded.
func (m *CanisterResourceModel) GetArgHex(ctx context.Context) (string, error) {

	// If encoded arguments were provided, use that
	if !m.ArgHex.IsNull() {
		return m.ArgHex.ValueString(), nil
	}

	// Otherwise, turn the terraform value into something that makes sense
	// in the candid world
	val, err := m.Arg.ToTerraformValue(ctx)
	if err != nil {
		return "", err
	}

	var data any
	ty := val.Type().String()

	// XXX: We currently only support string
	switch ty {
	case "tftypes.String":
		var str string
		err = val.As(&str)
		if err != nil {
			return "", err
		}

		data = str
	default:
		return "", errors.New(fmt.Sprintf("Cannot candid-encode value of type: %s", ty))

	}

	did, err := idl.Marshal([]any{data})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(did), nil

}

func setCanisterCode(canisterId string, argHex string, wasmFile string, wasmSha256 string) error {

	cfg := localhostConfig()
	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, cfg)
	if err != nil {
		return err
	}

	canisterIdP, err := principal.Decode(canisterId)
	if err != nil {
		return err
	}

	wasmModule, err := os.ReadFile(wasmFile)
	if err != nil {
		return err
	}

	// Check sha256
	computed := sha256.Sum256(wasmModule)
	if wasmSha256 != hex.EncodeToString(computed[:]) {
		return errors.New(fmt.Sprintf("Sha256 mismatch, expected %s, got %s", wasmSha256, computed))
	}

	skipPreUpgrade := false
	update := struct {
		SkipPreUpgrade *bool `ic:"skip_pre_upgrade,omitempty" json:"skip_pre_upgrade,omitempty"`
	}{SkipPreUpgrade: &skipPreUpgrade}

	ref := &update

	argRaw, err := hex.DecodeString(argHex)
	if err != nil {
		return err
	}

	installCodeArgs := icMgmt.InstallCodeArgs{
		Mode: icMgmt.CanisterInstallMode{
			Upgrade: &ref,
		},
		CanisterId: canisterIdP,
		WasmModule: wasmModule,
		Arg:        argRaw,
	}

	err = agent.InstallCode(installCodeArgs)
	if err != nil {
		return err
	}

	return nil
}

func setCanisterControllers(canisterId string, controllers []string) error {

	cfg := localhostConfig()
	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, cfg)
	if err != nil {
		return err
	}

	canisterIdP, err := principal.Decode(canisterId)
	if err != nil {
		return err
	}

	controllersP := make([]principal.Principal, len(controllers))
	for i := 0; i < len(controllers); i++ {
		controller, err := principal.Decode(controllers[i])
		if err != nil {
			return err
		}
		controllersP[i] = controller
	}

	canisterSettings := icMgmt.CanisterSettings{
		Controllers: &controllersP,
	}

	updateSettingsArgs := icMgmt.UpdateSettingsArgs{
		CanisterId: canisterIdP,
		Settings:   canisterSettings,
	}

	err = agent.UpdateSettings(updateSettingsArgs)
	if err != nil {
		return err
	}

	return nil
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
