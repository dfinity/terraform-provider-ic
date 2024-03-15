// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/aviate-labs/agent-go"
)

// Ensure IcProvider satisfies various provider interfaces.
var _ provider.Provider = &IcProvider{}
var _ provider.ProviderWithFunctions = &IcProvider{}

// IcProvider defines the provider implementation.
type IcProvider struct {
	// version is set to the provider version on release, "dev" when the
	// provider is built and ran locally, and "test" when running acceptance
	// testing.
	version string
}

// IcProviderModel describes the provider data model.
type IcProviderModel struct {
}

func (p *IcProvider) Metadata(ctx context.Context, req provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "ic"
	resp.Version = p.version
}

func (p *IcProvider) Schema(ctx context.Context, req provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{},
	}
}

func (p *IcProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {

	var data IcProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)

	if resp.Diagnostics.HasError() {
		return
	}

	// Configuration values are now available.

	agent, err := agent.New(agent.DefaultConfig)
	if err != nil {
		tflog.Error(ctx, "Cannot set up agent: "+err.Error())
		return
	}

	resp.DataSourceData = agent
}

func (p *IcProvider) Resources(ctx context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewCanisterResource,
	}
}

func (p *IcProvider) DataSources(ctx context.Context) []func() datasource.DataSource {
	return []func() datasource.DataSource{}
}

func (p *IcProvider) Functions(ctx context.Context) []func() function.Function {
	return []func() function.Function{}
}

func New(version string) func() provider.Provider {
	return func() provider.Provider {
		return &IcProvider{
			version: version,
		}
	}
}
