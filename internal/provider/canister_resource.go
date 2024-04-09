// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
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
var _ resource.ResourceWithModifyPlan = &CanisterResource{}

func NewCanisterResource() resource.Resource {
	return &CanisterResource{}
}

// CanisterResource defines the resource implementation.
type CanisterResource struct {
	config *agent.Config
}

// CanisterResourceModel describes the resource data model.
type CanisterResourceModel struct {
	Id          types.String   `tfsdk:"id"`
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

/* Generate a warning if the planned modifications for the canister do not include the controller that is used by terraform */
func (r *CanisterResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {

	// Here we use a pointer because terraform may pass a null value (e.g. in deletion)
	var data *CanisterResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// If terraform passed a null value (deletion) then there's nothing to do
	if data == nil {
		return
	}

	controllers := data.Controllers

	// Check if the identity used to terraform is amongst the controllers
	hasOurPrincipal := false
	ourPrincipal := r.config.Identity.Sender().Encode()
	for i := 0; i < len(controllers); i++ {
		if controllers[i].ValueString() == ourPrincipal {
			hasOurPrincipal = true
			break
		}
	}

	if !hasOurPrincipal {
		resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Target set of controllers does not include principal used by Terraform: %s", ourPrincipal))
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

	config, ok := req.ProviderData.(*agent.Config)

	if !ok {
		resp.Diagnostics.AddError(
			"Unexpected Resource Configure Type",
			fmt.Sprintf("Expected *agent.Agent, got: %T. Please report this issue to the provider developers.", req.ProviderData),
		)

		return
	}

	r.config = config
}

func (r *CanisterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data CanisterResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, *r.config)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", err.Error())
		return
	}

	createCanisterArgs := icMgmt.ProvisionalCreateCanisterWithCyclesArgs{}
	res, err := agent.ProvisionalCreateCanisterWithCycles(createCanisterArgs)

	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not create canister: "+err.Error())
		return
	}

	canisterId := res.CanisterId
	data.Id = types.StringValue(canisterId.Encode())
	tflog.Info(ctx, "Created canister: "+canisterId.Encode())

	/* Code install & args */

	argHex, err := data.GetArgHex(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not read argument: "+err.Error())
		return
	}

	wasmFile := data.WasmFile.ValueString()
	wasmSha256 := data.WasmSha256.ValueString()

	installMode := icMgmt.CanisterInstallMode{Install: &idl.Null{}}
	err = r.setCanisterCode(installMode, canisterId.Encode(), argHex, wasmFile, wasmSha256)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update code: "+err.Error())
		return
	}

	/* Controllers */

	// XXX: we set controllers at the very end so that e.g. blackhole code can be installed beforehand

	controllers := make([]string, len(data.Controllers))
	for i := 0; i < len(data.Controllers); i++ {
		controllers[i] = data.Controllers[i].ValueString()
	}
	err = r.setCanisterControllers(canisterId.Encode(), controllers)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update controllers: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
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

// XXX: this is NOT atomic
func (r *CanisterResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var data CanisterResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	tflog.Info(ctx, fmt.Sprintf("Updating to new data: %s", data))

	canisterId := data.Id.ValueString()

	/* Controllers */

	controllers := make([]string, len(data.Controllers))
	for i := 0; i < len(data.Controllers); i++ {
		controllers[i] = data.Controllers[i].ValueString()
	}

	err := r.setCanisterControllers(canisterId, controllers)
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

	skipPreUpgrade := false
	update := struct {
		SkipPreUpgrade *bool `ic:"skip_pre_upgrade,omitempty" json:"skip_pre_upgrade,omitempty"`
	}{SkipPreUpgrade: &skipPreUpgrade}
	ref := &update
	installMode := icMgmt.CanisterInstallMode{
		Upgrade: &ref,
	}
	err = r.setCanisterCode(installMode, canisterId, argHex, wasmFile, wasmSha256)
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

// NOTE: this checks that the wasm file contents have the given checksum and returns an error
// otherwise
func (r *CanisterResource) setCanisterCode(installMode icMgmt.CanisterInstallMode, canisterId string, argHex string, wasmFile string, wasmSha256 string) error {

	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, *r.config)
	if err != nil {
		return fmt.Errorf("Could not create agent: %w", err)
	}

	canisterIdP, err := principal.Decode(canisterId)
	if err != nil {
		return fmt.Errorf("Could not decode principal: %w", err)
	}

	wasmModule, err := os.ReadFile(wasmFile)
	if err != nil {
		return fmt.Errorf("Could not read wasm module: %w", err)
	}

	// Check sha256
	computed := sha256.Sum256(wasmModule)
	if wasmSha256 != hex.EncodeToString(computed[:]) {
		return errors.New(fmt.Sprintf("Sha256 mismatch, expected %s, got %s", wasmSha256, computed))
	}

	argRaw, err := hex.DecodeString(argHex)
	if err != nil {
		return err
	}

	installCodeArgs := icMgmt.InstallCodeArgs{
		Mode:       installMode,
		CanisterId: canisterIdP,
		WasmModule: wasmModule,
		Arg:        argRaw,
	}

	err = agent.InstallCode(installCodeArgs)
	if err != nil {
		return fmt.Errorf("Could not install code: %w", err)
	}

	return nil
}

func (r *CanisterResource) setCanisterControllers(canisterId string, controllers []string) error {

	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, *r.config)
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
	var data CanisterResourceModel

	// Read Terraform prior state data into the model
	resp.Diagnostics.Append(req.State.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	canisterId, err := principal.Decode(data.Id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Errorf("Could not parse canister ID: %w", err).Error())
		return
	}

	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, *r.config)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Errorf("Could not create agent: %w", err).Error())
		return
	}

	err = agent.StopCanister(icMgmt.StopCanisterArgs{CanisterId: canisterId})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Errorf("Could not stop canister before deletion: %w", err).Error())
		return
	}

	err = agent.DeleteCanister(icMgmt.DeleteCanisterArgs{CanisterId: canisterId})
	if err != nil {
		resp.Diagnostics.AddError("Client Error", fmt.Errorf("Could not delete canister: %w", err).Error())
		return
	}

	return
}

type CanisterInfo struct {
	Controllers []string
	WasmSha256  string /* hex encoded */
}

func (r *CanisterResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	tflog.Info(ctx, "Importing canister with ID: "+req.ID)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), req.ID)...)

	tflog.Info(ctx, "Decoding principal")
	canisterId, err := principal.Decode(req.ID)
	if err != nil {
		tflog.Error(ctx, "Cannot decode principal")
		return
	}

	canisterInfo, err := r.ReadCanisterInfo(ctx, canisterId)
	if err != nil {
		resp.Diagnostics.AddError("Agent error", "Cannot read canister info: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("wasm_sha256"), canisterInfo.WasmSha256)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("controllers"),
		canisterInfo.Controllers)...)
}

func (r *CanisterResource) ReadCanisterInfo(ctx context.Context, canisterId principal.Principal) (CanisterInfo, error) {

	tflog.Info(ctx, "Reading canister info for canister: "+canisterId.Encode())

	agent, err := agent.New(*r.config)
	if err != nil {
		return CanisterInfo{}, fmt.Errorf("could not create agent: %w", err)
	}

	tflog.Info(ctx, "Reading canister module hash for "+canisterId.Encode())
	moduleHash, err := agent.GetCanisterModuleHash(canisterId)
	if err != nil {
		return CanisterInfo{}, fmt.Errorf("could not get canister module hash: %w", err)
	}

	tflog.Info(ctx, "encoding module hash")
	moduleHashString := hex.EncodeToString(moduleHash)

	tflog.Info(ctx, "Reading canister controllers for "+canisterId.Encode())
	controllers, err := agent.GetCanisterControllers(canisterId)
	if err != nil {
		return CanisterInfo{}, fmt.Errorf("could not get canister controllers: %w", err)
	}

	controller_principals := make([]string, len(controllers))
	tflog.Info(ctx, "Decoding controller principals")
	for i := 0; i < len(controllers); i++ {
		controller_principals[i] = controllers[i].Encode()
	}

	return CanisterInfo{WasmSha256: moduleHashString, Controllers: controller_principals}, nil
}
