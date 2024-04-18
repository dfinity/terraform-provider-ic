// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/hashicorp/terraform-plugin-framework/types/basetypes"
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

func (c *CanisterResource) ProviderPrincipal() string {
	return c.config.Identity.Sender().Encode()
}

// CanisterResourceModel describes the resource data model.
type CanisterResourceModel struct {
	Id          types.String  `tfsdk:"id"`
	Controllers types.List    `tfsdk:"controllers"`
	Arg         types.Dynamic `tfsdk:"arg"`
	ArgHex      types.String  `tfsdk:"arg_hex"`     /* Hex-represented didc-encoded arguments */
	WasmFile    types.String  `tfsdk:"wasm_file"`   /* path to Wasm module */
	WasmSha256  types.String  `tfsdk:"wasm_sha256"` /* base64-encoded Wasm module */
}

func (r CanisterResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		/* arg & arg_hex cannot be both set. */
		resourcevalidator.Conflicting(
			path.MatchRoot("arg"),
			path.MatchRoot("arg_hex"),
		),
		/* If wasm_file is set, a sha must be given too. Moreover,
		   a sha doesn't make sense without a file. */
		resourcevalidator.RequiredTogether(
			path.MatchRoot("wasm_file"),
			path.MatchRoot("wasm_sha256"),
		),
	}
}

/* If the Controllers are Unknown or Null, update them (default) to the currently configured provider
 * principal. After this function has been called, the controllers are not null or unknown. */
func (data *CanisterResourceModel) InferDefaultControllers(ctx context.Context, config *agent.Config) error {

	tflog.Info(ctx, "Inferring controllers")
	providerController := config.Identity.Sender().Encode()

	if data.Controllers.IsNull() {
		elements := []attr.Value{types.StringValue(providerController)}
		data.Controllers = basetypes.NewListValueMust(types.StringType, elements)
	}

	if data.Controllers.IsUnknown() {
		elements := []attr.Value{types.StringValue(providerController)}
		data.Controllers = basetypes.NewListValueMust(types.StringType, elements)
	}

	return nil
}

func (data *CanisterResourceModel) StringControllers(ctx context.Context, config *agent.Config) ([]string, error) {

	if data.Controllers.IsNull() {
		return nil, nil
	}

	if data.Controllers.IsUnknown() {
		return nil, nil
	}

	// From here on, we know the Controllers are set

	controllersRaw := data.Controllers.Elements()
	controllers := make([]string, len(controllersRaw))

	for i := 0; i < len(controllers); i++ {
		controllerTF, err := controllersRaw[i].ToTerraformValue(ctx)
		if err != nil {
			return nil, err
		}

		ty := controllerTF.Type().String()

		if ty != "tftypes.String" {
			return nil, fmt.Errorf("Expected element type %s, got %s", controllerTF.String(), ty)
		}

		var str string
		err = controllerTF.As(&str)
		if err != nil {
			return nil, fmt.Errorf("Could not cast controller element: %w", err)
		}

		controllers[i] = str

	}

	return controllers, nil
}

/* Generate a warning if the planned modifications for the canister do not include the controller that is used by terraform. */
func (r *CanisterResource) ModifyPlan(ctx context.Context, req resource.ModifyPlanRequest, resp *resource.ModifyPlanResponse) {

	// Here we use a pointer because terraform may pass a null value (e.g. in deletion)
	var data *CanisterResourceModel

	tflog.Info(ctx, "Checking that provider controller is not removed")

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If terraform passed a null value (deletion) then there's nothing to do
	if data == nil {
		return
	}

	controllers, err := data.StringControllers(ctx, r.config)

	if err != nil {
		resp.Diagnostics.AddError("Client error", fmt.Sprintf("Could not read controllers: %s", err.Error()))
	}

	if resp.Diagnostics.HasError() {
		return
	}

	// The controllers being nil means we haven't yet figured out what controllers
	// to set (will be set to the provider's principal during creation)
	if controllers == nil {
		return
	}

	// Check if the identity used to terraform is amongst the controllers
	hasOurPrincipal := false
	ourPrincipal := r.ProviderPrincipal()
	for i := 0; i < len(controllers); i++ {
		if controllers[i] == ourPrincipal {
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

	// XXX: at this point, CanisterResource is not initialized yet

	var argDefaultDescription = "If neither `arg` nor `arg_hex` is set, the argument defaults to the empty blob (and not for instance to a Candid `null`)."
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
				MarkdownDescription: "Canister controllers. When creating a new canister, defaults to the principal used by the provider.",

				/* the controllers can either be fetched from the replica, or
				set directly if necessary.
				Upon canister creation, the following applies to controllers
				 * not set or null: use the provider's controller as only controller
				 * empty list: blackhole canister
				*/
				Computed: true,
				Optional: true,
			},
			"arg": schema.DynamicAttribute{
				Optional: true,

				MarkdownDescription: "Init & post_upgrade arguments for the canister. Heuristics are used to convert it to candid. " + argDefaultDescription,
			},
			"arg_hex": schema.StringAttribute{
				Optional: true,

				MarkdownDescription: "Hex representation of candid-encoded arguments. " + argDefaultDescription,
			},
			"wasm_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to Wasm module to install",
			},
			"wasm_sha256": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Sha256 sum of Wasm module (hex encoded). Required if `wasm_file` is specified.",
			},
		},
	}
}

func (r *CanisterResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	tflog.Info(ctx, "Configuring canister resource")
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

	// If the wasm file is not null, then install the code.
	if !data.WasmFile.IsNull() {

		wasmFile := data.WasmFile.ValueString()
		// If the wasm file is not null, then the sha256 is set (through resourcevalidator)
		wasmSha256 := data.WasmSha256.ValueString()

		installMode := icMgmt.CanisterInstallMode{Install: &idl.Null{}}
		err = r.setCanisterCode(installMode, canisterId.Encode(), argHex, wasmFile, wasmSha256)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", "Could not update code: "+err.Error())
			return
		}

	}

	// XXX: we set controllers at the very end so that e.g. blackhole code can be installed beforehand
	// NOTE: we do not test the creation of blackhole canisters because the terraform testing framework
	// does not support resources that can't be deleted, so this is really best effort:
	//   * https://github.com/hashicorp/terraform-plugin-testing/issues/85

	/* Controllers */

	err = data.InferDefaultControllers(ctx, r.config)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update controllers: "+err.Error())
		return
	}

	controllers, err := data.StringControllers(ctx, r.config)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update controllers: "+err.Error())
		return
	}

	// We did just call InferDefaultControllers, so if the controllers are not set this is a bad bug
	if controllers == nil {
		resp.Diagnostics.AddError("Client Error", "Controllers not set")
		return
	}

	err = r.setCanisterControllers(canisterId.Encode(), controllers)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update controllers: "+err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
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

// XXX: this is NOT atomic.
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

	controllers, err := data.StringControllers(ctx, r.config)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not update controllers: "+err.Error())
		return
	}

	// Here we don't expect nil (unknown/null) controllers. We only expect unknown or null controllers
	// during the initial creation.
	if controllers == nil {
		resp.Diagnostics.AddError("Client Error", "Controllers not set")
		return
	}

	err = r.setCanisterControllers(canisterId, controllers)
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
}

// Returns the candid argument, hex-encoded.
func (m *CanisterResourceModel) GetArgHex(ctx context.Context) (string, error) {

	// If encoded arguments were provided, use that
	if !m.ArgHex.IsNull() {
		return m.ArgHex.ValueString(), nil
	}

	// If no args are set, use the empty bytestring (hex encoding: empty string)
	if m.Arg.IsNull() {
		return "", nil
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
		return "", fmt.Errorf("Cannot candid-encode value %s of type: %s", m.Arg.String(), ty)

	}

	did, err := idl.Marshal([]any{data})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(did), nil

}

// NOTE: this checks that the wasm file contents have the given checksum and returns an error
// otherwise.
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
		return fmt.Errorf("Sha256 mismatch, expected %s, got %s", wasmSha256, computed)
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
