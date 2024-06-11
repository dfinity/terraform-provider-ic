// Copyright (c) DFINITY Foundation

package provider

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
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
	cmc "github.com/aviate-labs/agent-go/ic/cmc"
	icMgmt "github.com/aviate-labs/agent-go/ic/ic"
	ledger "github.com/aviate-labs/agent-go/ic/icpledger"
	"github.com/aviate-labs/agent-go/principal"
)

// Ensure provider defined types fully satisfy framework interfaces.
var _ resource.Resource = &CanisterResource{}
var _ resource.ResourceWithImportState = &CanisterResource{}
var _ resource.ResourceWithConfigValidators = &CanisterResource{}
var _ resource.ResourceWithValidateConfig = &CanisterResource{}
var _ resource.ResourceWithModifyPlan = &CanisterResource{}

func NewCanisterResource() resource.Resource {
	return &CanisterResource{}
}

// CanisterResource defines the resource implementation.
type CanisterResource struct {
	config *agent.Config
}

func (r *CanisterResource) ProviderPrincipal() string {
	return r.config.Identity.Sender().Encode()
}

// CanisterResourceModel describes the resource data model.
type CanisterResourceModel struct {
	Id          types.String  `tfsdk:"id"`
	Controllers types.List    `tfsdk:"controllers"`
	Arg         types.Dynamic `tfsdk:"arg"`
	ArgHex      types.String  `tfsdk:"arg_hex"`     // Hex-represented didc-encoded arguments
	WasmFile    types.String  `tfsdk:"wasm_file"`   // path to Wasm module
	WasmSha256  types.String  `tfsdk:"wasm_sha256"` // base64-encoded Wasm module
}

func (r CanisterResource) ConfigValidators(ctx context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		// arg & arg_hex cannot be both set.
		resourcevalidator.Conflicting(
			path.MatchRoot("arg"),
			path.MatchRoot("arg_hex"),
		),
	}
}

func (r CanisterResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var data CanisterResourceModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}

	// If a sha256 is given but no module is given, show a warning
	if !data.WasmSha256.IsNull() && data.WasmFile.IsNull() {
		resp.Diagnostics.AddAttributeWarning(
			path.Root("wasm_sha256"),
			"Sha256 specified without module",
			"Expected wasm_sha256 to have a wasm_file specified. "+
				"The resource may return unexpected results.",
		)
	}
}

// If the Controllers are Unknown or Null, update them (default) to the currently configured provider
// principal. After this function has been called, the controllers are not null or unknown.
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

// Generate a warning if the planned modifications for the canister do not include the controller that is used by terraform.
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

				// the controllers can either be fetched from the replica, or
				// set directly if necessary.
				// Upon canister creation, the following applies to controllers
				// * not set or null: use the provider's controller as only controller
				// * empty list: blackhole canister
				Computed: true,
				Optional: true,
			},
			"arg": schema.DynamicAttribute{
				Optional: true,

				MarkdownDescription: "Init & post_upgrade arguments for the canister. Heuristics are used to convert it to candid. " + "The Terraform value is automatically candid-encoded using the heurstics describe in the `did_encode` function. You should not call `did_encode` when using `arg`. " + argDefaultDescription,
			},
			"arg_hex": schema.StringAttribute{
				Optional: true,

				MarkdownDescription: "Hex representation of candid-encoded arguments. This is helpful if you generate a (hex) candid-encoded strings using didc or by using `did_encode` directly. " + argDefaultDescription,
			},
			"wasm_file": schema.StringAttribute{
				Optional:            true,
				MarkdownDescription: "Path to Wasm module to install",
			},
			"wasm_sha256": schema.StringAttribute{
				Optional:            true,
				Computed:            true,
				MarkdownDescription: "Sha256 sum of Wasm module (hex encoded). Recommended if `wasm_file` is specified.",
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

func createCanisterProvisional(config agent.Config) (principal.Principal, error) {

	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, config)
	if err != nil {
		return principal.Principal{}, err
	}

	createCanisterArgs := icMgmt.ProvisionalCreateCanisterWithCyclesArgs{}
	res, err := agent.ProvisionalCreateCanisterWithCycles(createCanisterArgs)

	if err != nil {
		return principal.Principal{}, err
	}

	return res.CanisterId, nil
}

var MEMO_CREATE_CANISTER uint64 = 0x41455243

func createCanisterCMC(ctx context.Context, config agent.Config) (principal.Principal, error) {

	ledgerAgent, err := ledger.NewAgent(ic.LEDGER_PRINCIPAL, config)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("Could not create ledger agent: %w", err)
	}

	// Prepare the subaccount to send ICP to

	myController := config.Identity.Sender().Raw
	subaccount := [32]byte{}
	subaccount[0] = byte(len(myController))

	for i := 0; i < len(myController); i++ {
		subaccount[i+1] = myController[i]
	}

	cmcDestAccount := principal.NewAccountID(ic.CYCLES_MINTING_PRINCIPAL, subaccount)

	// Figure out how much ICP to send by checking the cycles conversion rate on the CMC
	cmcAgent, err := cmc.NewAgent(ic.CYCLES_MINTING_PRINCIPAL, config)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("Could not create CMC agent: %w", err)
	}

	conversionRate, err := cmcAgent.GetIcpXdrConversionRate()
	if err != nil {
		return principal.Principal{}, fmt.Errorf("Could not get cycles conversion rate from CMC: %w", err)
	}

	if conversionRate == nil {
		return principal.Principal{}, fmt.Errorf("Got no conversion rate from CMC")
	}

	// XdrPermyriadPerIcp == price of 1e8s in cycles
	// => price of cycles in 1e8s = 1 / XdrPermyriadPerIcp
	nE8s := 1_000_000_000_000 /* 1T cycles (0.1 creation + 0.9 running costs) */ / conversionRate.Data.XdrPermyriadPerIcp

	tflog.Info(ctx, fmt.Sprintf("Creating canister with %d e8s", nE8s))

	transferArgs := ledger.TransferArgs{
		Amount: ledger.Tokens{E8s: nE8s},
		Fee:    ledger.Tokens{E8s: 10_000},
		// FromSubaccount: default to default (null) subaccount
		To:   cmcDestAccount.Bytes(),
		Memo: MEMO_CREATE_CANISTER,
	}

	res, err := ledgerAgent.Transfer(transferArgs)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("Could not transfer funds to create canister: %w", err)
	}

	if res.Ok == nil {
		str, _ := json.Marshal(res.Err)
		return principal.Principal{}, fmt.Errorf("Error when transferring funds: %s", string(str))
	}

	blockId := *res.Ok

	notifyCreateCanisterArg := cmc.NotifyCreateCanisterArg{
		BlockIndex: blockId,
		Controller: config.Identity.Sender(),
	}

	resCreate, err := cmcAgent.NotifyCreateCanister(notifyCreateCanisterArg)
	if err != nil {
		return principal.Principal{}, fmt.Errorf("Could not create canister on CMC: %w", err)
	}

	if resCreate.Ok == nil {
		str, _ := json.Marshal(res.Err)
		return principal.Principal{}, fmt.Errorf("Error when creating canister: %s", string(str))
	}

	canisterId := *resCreate.Ok

	return canisterId, nil

}

func (r *CanisterResource) createCanister(ctx context.Context) (principal.Principal, error) {
	if r.config.ClientConfig.Host.String() == icpApi.String() {
		// If we're on mainnet, use the CMC to create canisters
		return createCanisterCMC(ctx, *r.config)
	} else {
		// otherwise, assume some test setup and use provisional creation
		return createCanisterProvisional(*r.config)
	}
}

func (r *CanisterResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var data CanisterResourceModel

	// Read Terraform plan data into the model
	resp.Diagnostics.Append(req.Plan.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	canisterId, err := r.createCanister(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", err.Error())
		return
	}

	data.Id = types.StringValue(canisterId.Encode())
	tflog.Info(ctx, "Created canister: "+canisterId.Encode())

	// Code install & args

	argHex, err := data.GetArgHex(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not read argument: "+err.Error())
		return
	}

	doInstallCode := !data.WasmFile.IsNull()

	// This may be the empty string (if sha256 was not set). `setCanisterCode` handles
	// it appropriately.
	wasmSha256 := data.WasmSha256.ValueString()

	// If the wasm file is not null, then install the code.
	if doInstallCode {

		wasmFile := data.WasmFile.ValueString()

		// We're creating a new canister, so we always use "install"
		err = r.setCanisterCode(ctx, canisterId.Encode(), argHex, wasmFile, wasmSha256)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", "Could not update code: "+err.Error())
			return
		}

	}

	canisterInfo, err := r.ReadCanisterInfo(ctx, canisterId)
	if err != nil {
		resp.Diagnostics.AddError("Client Error", "Could not read canister info: "+err.Error())
		return
	}

	if doInstallCode {
		// If we installed the code, and wasm_sha256 was set, we expect it to match
		// that of the newly created canister.

		if len(wasmSha256) > 0 && wasmSha256 != canisterInfo.WasmSha256 {
			resp.Diagnostics.AddWarning("Client Warning", fmt.Sprintf("Expected Wasm module sha %s does not match canister info sha %s. Please inspect canister", wasmSha256, canisterInfo.WasmSha256))
		}
	}

	// If the sha wasn't specified by the user (or technically also if set to the empty string), then we set it here.
	if len(wasmSha256) == 0 {
		data.WasmSha256 = types.StringValue(canisterInfo.WasmSha256)
	}

	// XXX: we set controllers at the very end so that e.g. blackhole code can be installed beforehand
	// NOTE: we do not test the creation of blackhole canisters because the terraform testing framework
	// does not support resources that can't be deleted, so this is really best effort:
	//   * https://github.com/hashicorp/terraform-plugin-testing/issues/85

	// Controllers

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

	// Controllers

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

	// Code install & args

	if data.WasmFile.IsNull() {
		// If there is no wasm, then we uninstall the canister (idempotent)

		err = r.setCanisterEmpty(canisterId)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", "Could not uninstall code: "+err.Error())
			return
		}

		// Update the value (instead of keeping it potentially "unknown")
		data.WasmSha256 = types.StringValue("")

	} else {
		// If wasm is set, then install it with the given args (idempotent)

		argHex, err := data.GetArgHex(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", "Could not read argument: "+err.Error())
			return
		}

		wasmFile := data.WasmFile.ValueString()
		wasmSha256 := data.WasmSha256.ValueString()
		err = r.setCanisterCode(ctx, canisterId, argHex, wasmFile, wasmSha256)
		if err != nil {
			resp.Diagnostics.AddError("Client Error", "Could not update code: "+err.Error())
			return
		}
	}

	// Save updated data into Terraform state
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)

	tflog.Info(ctx, "Done updating canister")
}

// Ensures the canister is empty (no code installed).
func (r *CanisterResource) setCanisterEmpty(canisterId string) error {

	agent, err := icMgmt.NewAgent(ic.MANAGEMENT_CANISTER_PRINCIPAL, *r.config)
	if err != nil {
		return fmt.Errorf("Uninstalling canister: Could not create agent: %w", err)
	}

	canisterIdP, err := principal.Decode(canisterId)
	if err != nil {
		return fmt.Errorf("Uninstalling canister: Could not decode principal: %w", err)
	}

	uninstallCodeArgs := icMgmt.UninstallCodeArgs{
		CanisterId: canisterIdP,
	}

	err = agent.UninstallCode(uninstallCodeArgs)
	if err != nil {
		return fmt.Errorf("Uninstalling canister: Could not uninstall code: %w", err)
	}

	return nil

}

func CanisterInstallModeInstall() icMgmt.CanisterInstallMode {
	return icMgmt.CanisterInstallMode{Install: &idl.Null{}}
}

func CanisterInstallModeUpgrade() icMgmt.CanisterInstallMode {
	skipPreUpgrade := false
	update := struct {
		SkipPreUpgrade        *bool `ic:"skip_pre_upgrade,omitempty" json:"skip_pre_upgrade,omitempty"`
		WasmMemoryPersistence *struct {
			Keep    *idl.Null `ic:"keep,variant"`
			Replace *idl.Null `ic:"replace,variant"`
		} `ic:"wasm_memory_persistence,omitempty" json:"wasm_memory_persistence,omitempty"`
	}{SkipPreUpgrade: &skipPreUpgrade}
	ref := &update
	return icMgmt.CanisterInstallMode{
		Upgrade: &ref,
	}
}

// Returns the candid argument, hex-encoded.
func (data *CanisterResourceModel) GetArgHex(ctx context.Context) (string, error) {

	// If encoded arguments were provided, use that
	if !data.ArgHex.IsNull() {
		return data.ArgHex.ValueString(), nil
	}

	// If no args are set, use the empty bytestring (hex encoding: empty string)
	if data.Arg.IsNull() {
		return "", nil
	}

	// Otherwise, turn the terraform value into something that makes sense
	// in the candid world
	tfVal, err := data.Arg.ToTerraformValue(ctx)
	if err != nil {
		return "", err
	}

	didValue, err := TFValToCandid(tfVal)
	if err != nil {
		return "", err
	}

	didEncoded, err := idl.Marshal([]any{didValue})
	if err != nil {
		return "", err
	}

	return hex.EncodeToString(didEncoded), nil

}

// NOTE: this checks that the wasm file contents have the given checksum and returns an error
// otherwise.
func (r *CanisterResource) setCanisterCode(ctx context.Context, canisterId string, argHex string, wasmFile string, wasmSha256 string) error {

	installMode, err := r.InferInstallMode(ctx, canisterId)
	if err != nil {
		return fmt.Errorf("Could not infer install mode: %w", err)
	}

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

	// If a sha is specified, then check that it matches that of the module.
	if len(wasmSha256) > 0 {
		computed := sha256.Sum256(wasmModule)
		computedStr := hex.EncodeToString(computed[:])
		if wasmSha256 != computedStr {
			return fmt.Errorf("Sha256 mismatch, expected %s, got %s", wasmSha256, computedStr)
		}
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
	WasmSha256  string // hex encoded
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

func (r *CanisterResource) InferInstallMode(ctx context.Context, canisterIdS string) (icMgmt.CanisterInstallMode, error) {

	installMode := icMgmt.CanisterInstallMode{}

	canisterId, err := principal.Decode(canisterIdS)
	if err != nil {
		return installMode, fmt.Errorf("Could not decode canister principal: %w", err)
	}

	tflog.Info(ctx, "Reading canister info for canister: "+canisterId.Encode())

	agent, err := agent.New(*r.config)
	if err != nil {
		return installMode, fmt.Errorf("could not create agent: %w", err)
	}

	tflog.Info(ctx, "Reading canister module hash for "+canisterId.Encode())
	moduleHash, err := agent.GetCanisterModuleHash(canisterId)
	if err != nil {
		return installMode, fmt.Errorf("could not get canister module hash: %w", err)
	}

	if len(moduleHash) == 0 {
		installMode = CanisterInstallModeInstall()
	} else {
		installMode = CanisterInstallModeUpgrade()
	}

	return installMode, nil
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

	controllerPrincipals := make([]string, len(controllers))
	tflog.Info(ctx, "Decoding controller principals")
	for i := 0; i < len(controllers); i++ {
		controllerPrincipals[i] = controllers[i].Encode()
	}

	return CanisterInfo{WasmSha256: moduleHashString, Controllers: controllerPrincipals}, nil
}
