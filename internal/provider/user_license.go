package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/googleapi"
	admin "google.golang.org/api/licensing/v1"
	"google.golang.org/api/option"
)

type LicenseResource struct {
	config GoogleWorkspaceProviderModel
}

type LicenseResourceModel struct {
	ID        types.String `tfsdk:"id"`
	UserID    types.String `tfsdk:"user_id"`
	ProductID types.String `tfsdk:"product_id"`
	SKUID     types.String `tfsdk:"sku_id"`
}

func NewLicenseResource() resource.Resource {
	return &LicenseResource{}
}

func (r *LicenseResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_license"
}

func (r *LicenseResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"user_id": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"product_id": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"sku_id": schema.StringAttribute{
				Required: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
		},
	}
}

func (r *LicenseResource) Configure(ctx context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}

	config := req.ProviderData.(GoogleWorkspaceProviderModel)
	r.config = config
}

func (r *LicenseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan LicenseResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jsonKey := []byte(r.config.ServiceAccountKey.ValueString())

	creds, err := google.CredentialsFromJSONWithParams(ctx, jsonKey, google.CredentialsParams{
		Scopes: []string{
			"https://www.googleapis.com/auth/apps.licensing",
		},
		Subject: r.config.ImpersonatedAdmin.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Erreur création credentials Google", err.Error())
		return
	}

	service, err := admin.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		resp.Diagnostics.AddError("Erreur création client Google", err.Error())
		return
	}

	assignment := &admin.LicenseAssignmentInsert{
		UserId: plan.UserID.ValueString(),
	}

	_, err = service.LicenseAssignments.Insert(
		plan.ProductID.ValueString(),
		plan.SKUID.ValueString(),
		assignment,
	).Do()

	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 409 {
			resp.Diagnostics.AddWarning("Licence déjà assignée", "La licence existe déjà, on continue")
		} else {
			resp.Diagnostics.AddError("Erreur assignation licence", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s/%s",
		plan.ProductID.ValueString(),
		plan.SKUID.ValueString(),
		plan.UserID.ValueString(),
	))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *LicenseResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state LicenseResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jsonKey := []byte(r.config.ServiceAccountKey.ValueString())

	creds, err := google.CredentialsFromJSONWithParams(ctx, jsonKey, google.CredentialsParams{
		Scopes: []string{
			"https://www.googleapis.com/auth/apps.licensing",
		},
		Subject: r.config.ImpersonatedAdmin.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Erreur création credentials Google", err.Error())
		return
	}

	service, err := admin.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		resp.Diagnostics.AddError("Erreur création client Google", err.Error())
		return
	}

	_, err = service.LicenseAssignments.Get(
		state.ProductID.ValueString(),
		state.SKUID.ValueString(),
		state.UserID.ValueString(),
	).Do()

	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError("Erreur lecture licence", err.Error())
		return
	}
}

func (r *LicenseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	resp.Diagnostics.AddError(
		"Not supported",
		"Update is not supported for license assignments; resource must be replaced.",
	)
}

func (r *LicenseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state LicenseResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	jsonKey := []byte(r.config.ServiceAccountKey.ValueString())

	creds, err := google.CredentialsFromJSONWithParams(ctx, jsonKey, google.CredentialsParams{
		Scopes: []string{
			"https://www.googleapis.com/auth/apps.licensing",
		},
		Subject: r.config.ImpersonatedAdmin.ValueString(),
	})
	if err != nil {
		resp.Diagnostics.AddError("Erreur création credentials Google", err.Error())
		return
	}

	service, err := admin.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		resp.Diagnostics.AddError("Erreur création client Google", err.Error())
		return
	}

	_, err = service.LicenseAssignments.Delete(
		state.ProductID.ValueString(),
		state.SKUID.ValueString(),
		state.UserID.ValueString(),
	).Do()

	if err != nil {
		resp.Diagnostics.AddError("Erreur suppression licence", err.Error())
		return
	}
}
