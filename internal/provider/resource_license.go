package provider

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"golang.org/x/oauth2/google"
	"golang.org/x/time/rate"
	"google.golang.org/api/googleapi"
	admin "google.golang.org/api/licensing/v1"
	"google.golang.org/api/option"
)

var (
	licensingLimiterOnce sync.Once
	licensingLimiter     *rate.Limiter
)

func getLicensingLimiter() *rate.Limiter {
	licensingLimiterOnce.Do(func() {
		licensingLimiter = rate.NewLimiter(rate.Every(800*time.Millisecond), 1)
	})

	return licensingLimiter
}

func waitLicensingRateLimit(ctx context.Context) error {
	return getLicensingLimiter().Wait(ctx)
}

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
	resp.TypeName = req.ProviderTypeName + "_user_license"
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

func retryGoogle(ctx context.Context, f func() error) error {
	delays := []time.Duration{
		1 * time.Second,
		2 * time.Second,
		4 * time.Second,
		8 * time.Second,
		16 * time.Second,
		30 * time.Second,
		45 * time.Second,
		60 * time.Second,
	}

	var err error

	for i, delay := range delays {
		if err := waitLicensingRateLimit(ctx); err != nil {
			return err
		}

		err = f()
		if err == nil {
			return nil
		}

		if !isRetryableError(err) {
			return err
		}

		if i == len(delays)-1 {
			return err
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	return err
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	if gerr, ok := err.(*googleapi.Error); ok {
		return isRetryableGoogleError(gerr)
	}

	msg := strings.ToLower(err.Error())

	return strings.Contains(msg, "cannot fetch token") ||
		strings.Contains(msg, "internal_failure") ||
		strings.Contains(msg, "backend error") ||
		strings.Contains(msg, "backenderror") ||
		strings.Contains(msg, "503") ||
		strings.Contains(msg, "502") ||
		strings.Contains(msg, "504") ||
		strings.Contains(msg, "connection reset") ||
		strings.Contains(msg, "timeout") ||
		strings.Contains(msg, "temporary failure")
}

func isRetryableGoogleError(err *googleapi.Error) bool {
	switch err.Code {
	case 429, 500, 502, 503, 504:
		return true
	case 403:
		for _, apiErr := range err.Errors {
			if apiErr.Reason == "rateLimitExceeded" ||
				apiErr.Reason == "userRateLimitExceeded" {
				return true
			}
		}
	case 412:
		for _, apiErr := range err.Errors {
			if apiErr.Reason == "conditionNotMet" {
				return true
			}
		}

		if strings.Contains(err.Message, "different SKU") ||
			strings.Contains(err.Message, "already has a license of the product") {
			return true
		}
	}

	return false
}

func (r *LicenseResource) newLicensingService(ctx context.Context) (*admin.Service, error) {
	jsonKey := []byte(r.config.ServiceAccountKey.ValueString())

	creds, err := google.CredentialsFromJSONWithParams(ctx, jsonKey, google.CredentialsParams{
		Scopes: []string{
			"https://www.googleapis.com/auth/apps.licensing",
		},
		Subject: r.config.ImpersonatedAdmin.ValueString(),
	})
	if err != nil {
		return nil, err
	}

	service, err := admin.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return nil, err
	}

	return service, nil
}

func licenseID(productID, skuID, userID string) string {
	return fmt.Sprintf("%s/%s/%s", productID, skuID, userID)
}

func (r *LicenseResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan LicenseResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}

	service, err := r.newLicensingService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Google client creation failed", err.Error())
		return
	}

	assignment := &admin.LicenseAssignmentInsert{
		UserId: plan.UserID.ValueString(),
	}

	err = retryGoogle(ctx, func() error {
		_, err := service.LicenseAssignments.Insert(
			plan.ProductID.ValueString(),
			plan.SKUID.ValueString(),
			assignment,
		).Do()

		return err
	})

	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 409 {
			resp.Diagnostics.AddWarning(
				"License already assigned",
				"The license already exists, continuing.",
			)
		} else {
			resp.Diagnostics.AddError("Failed to assign license", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(licenseID(
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

	if !state.ID.IsNull() && !state.ID.IsUnknown() &&
		(state.UserID.IsNull() || state.ProductID.IsNull() || state.SKUID.IsNull()) {

		parts := strings.Split(state.ID.ValueString(), "/")
		if len(parts) != 3 {
			resp.Diagnostics.AddError(
				"Invalid import ID",
				"Expected format: product_id/sku_id/user_id",
			)
			return
		}

		state.ProductID = types.StringValue(parts[0])
		state.SKUID = types.StringValue(parts[1])
		state.UserID = types.StringValue(parts[2])
	}

	service, err := r.newLicensingService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Google client creation failed", err.Error())
		return
	}

	err = retryGoogle(ctx, func() error {
		_, err := service.LicenseAssignments.Get(
			state.ProductID.ValueString(),
			state.SKUID.ValueString(),
			state.UserID.ValueString(),
		).Do()

		return err
	})

	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			resp.State.RemoveResource(ctx)
			return
		}

		resp.Diagnostics.AddError("Failed to read license", err.Error())
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *LicenseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state LicenseResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	service, err := r.newLicensingService(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Google client creation failed", err.Error())
		return
	}

	err = retryGoogle(ctx, func() error {
		_, err := service.LicenseAssignments.Delete(
			state.ProductID.ValueString(),
			state.SKUID.ValueString(),
			state.UserID.ValueString(),
		).Do()

		return err
	})

	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok && gerr.Code == 404 {
			return
		}

		resp.Diagnostics.AddError("Failed to delete license", err.Error())
		return
	}
}

func (r *LicenseResource) Update(
	ctx context.Context,
	req resource.UpdateRequest,
	resp *resource.UpdateResponse,
) {
	resp.Diagnostics.AddError(
		"Update not supported",
		"This resource only supports Create/Delete replacement operations.",
	)
}

func (r *LicenseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}
