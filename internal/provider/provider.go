package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/provider/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

type GoogleWorkspaceProvider struct{}

// GoogleWorkspaceProviderModel représente la configuration du provider, avec les champs nécessaires pour se connecter à l'API Google Workspace.
type GoogleWorkspaceProviderModel struct {
	ServiceAccountKey types.String `tfsdk:"credentials"`
	ImpersonatedAdmin types.String `tfsdk:"impersonated_user_email"`
	CustomerID        types.String `tfsdk:"customer_id"`
}

// New est la fonction qui crée une nouvelle instance du provider. C'est cette fonction qui est appelée dans main.go pour démarrer le serveur.
func New() provider.Provider {
	return &GoogleWorkspaceProvider{}
}

func (p *GoogleWorkspaceProvider) Metadata(_ context.Context, _ provider.MetadataRequest, resp *provider.MetadataResponse) {
	resp.TypeName = "googleworkspace"
}

func (p *GoogleWorkspaceProvider) Schema(_ context.Context, _ provider.SchemaRequest, resp *provider.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"credentials": schema.StringAttribute{
				Required:    true,
				Sensitive:   true,
				Description: "The JSON key of the service account with domain-wide delegation enabled.",
			},
			"impersonated_user_email": schema.StringAttribute{
				Required:    true,
				Description: "The email of the user to impersonate when making API calls.",
			},
			"customer_id": schema.StringAttribute{
				Optional:    true,
				Description: "The ID of the Google Workspace customer.",
			},
		},
	}
}

func (p *GoogleWorkspaceProvider) Configure(ctx context.Context, req provider.ConfigureRequest, resp *provider.ConfigureResponse) {
	var config GoogleWorkspaceProviderModel

	resp.Diagnostics.Append(req.Config.Get(ctx, &config)...)
	if resp.Diagnostics.HasError() {
		return
	}

	resp.ResourceData = config
	resp.DataSourceData = config
}

func (p *GoogleWorkspaceProvider) Resources(_ context.Context) []func() resource.Resource {
	return []func() resource.Resource{
		NewLicenseResource,
	}
}

func (p *GoogleWorkspaceProvider) DataSources(_ context.Context) []func() datasource.DataSource {
	return nil
}
