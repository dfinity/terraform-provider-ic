// Copyright (c) HashiCorp, Inc.
// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"net/url"
	"os"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/function"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-log/tflog"

	"github.com/aviate-labs/agent-go"
	"github.com/aviate-labs/agent-go/identity"
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
type IcProviderModel struct{}

func LocalhostConfig() (agent.Config, error) {

	// If IC_PEM_IDENTITY_PATH is provided, read the file as the identity
	pemPath := os.Getenv("IC_PEM_IDENTITY_PATH")

	var id identity.Identity
	var config agent.Config

	if len(pemPath) > 0 {

		data, err := os.ReadFile(pemPath)

		if err != nil {
			return config, err
		}

		id, err = NewIdentityFromPEM(data)

		if err != nil {
			return config, err
		}
	}

	u, _ := url.Parse("http://localhost:4943")
	config = agent.Config{
		ClientConfig: &agent.ClientConfig{Host: u},
		FetchRootKey: true,
		Identity:     id,
	}

	return config, nil
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

	config, err := LocalhostConfig()
	if err != nil {
		resp.Diagnostics.AddError(
			"Could not set up IC agent",
			err.Error(),
		)
	}

	tflog.Info(ctx, fmt.Sprintf("Using identity: %s", config.Identity.Sender().Encode()))

	if resp.Diagnostics.HasError() {
		return
	}

	resp.ResourceData = &config
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
