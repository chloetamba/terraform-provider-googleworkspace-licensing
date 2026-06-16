package provider

import (
	"context"
	"fmt"
	"log"
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

var licenseCache = struct {
	sync.Mutex
	data map[string]map[string]bool
}{
	data: make(map[string]map[string]bool),
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

	service, ok := r.newLicensingService(ctx, &resp.Diagnostics)
	if !ok {
		return
	}

	assignment := &admin.LicenseAssignmentInsert{
		UserId: plan.UserID.ValueString(),
	}

	_, err := service.LicenseAssignments.Insert(
		plan.ProductID.ValueString(),
		plan.SKUID.ValueString(),
		assignment,
	).Do()

	if err != nil {
		if gerr, ok := err.(*googleapi.Error); ok {
			switch gerr.Code {
			case 409:
				log.Printf("[DEBUG] License already assigned for %s/%s/%s",
					plan.ProductID.ValueString(),
					plan.SKUID.ValueString(),
					plan.UserID.ValueString(),
				)

			case 412:
				log.Printf("[DEBUG] Insert returned 412, calling Update for %s/%s/%s",
					plan.ProductID.ValueString(),
					plan.SKUID.ValueString(),
					plan.UserID.ValueString(),
				)

				_, updateErr := service.LicenseAssignments.Update(
					plan.ProductID.ValueString(),
					plan.SKUID.ValueString(),
					plan.UserID.ValueString(),
					&admin.LicenseAssignment{
						UserId: plan.UserID.ValueString(),
					},
				).Do()

				if updateErr != nil {
					resp.Diagnostics.AddError("Failed to reassign license", updateErr.Error())
					return
				}

			default:
				resp.Diagnostics.AddError("Failed to assign license", err.Error())
				return
			}
		} else {
			resp.Diagnostics.AddError("Failed to assign license", err.Error())
			return
		}
	}

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s/%s",
		plan.ProductID.ValueString(),
		plan.SKUID.ValueString(),
		plan.UserID.ValueString(),
	))

	addUserToCache(
		plan.ProductID.ValueString(),
		plan.SKUID.ValueString(),
		plan.UserID.ValueString(),
	)

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

	cacheKey := buildCacheKey(state.ProductID.ValueString(), state.SKUID.ValueString())
	userID := state.UserID.ValueString()

	licenseCache.Lock()
	usersForLicense, exists := licenseCache.data[cacheKey]

	if exists {
		log.Printf("[DEBUG] Cache HIT for %s", cacheKey)
	} else {
		log.Printf("[DEBUG] Cache MISS for %s", cacheKey)
	}

	if !exists {
		licenseCache.Unlock()

		service, ok := r.newLicensingService(ctx, &resp.Diagnostics)
		if !ok {
			return
		}

		usersForLicense = make(map[string]bool)

		customerID := r.config.CustomerID.ValueString()
		if customerID == "" {
			customerID = "my_customer"
		}

		call := service.LicenseAssignments.ListForProductAndSku(
			state.ProductID.ValueString(),
			state.SKUID.ValueString(),
			customerID,
		)

		log.Printf("[DEBUG] Calling ListForProductAndSku for %s", cacheKey)

		for {
			var assignments *admin.LicenseAssignmentList
			var err error

			for attempt := 1; attempt <= 5; attempt++ {
				assignments, err = call.Do()
				if err == nil {
					break
				}

				if gerr, ok := err.(*googleapi.Error); ok &&
					gerr.Code == 403 &&
					strings.Contains(err.Error(), "RATE_LIMIT_EXCEEDED") {

					time.Sleep(time.Duration(attempt) * 15 * time.Second)
					continue
				}

				resp.Diagnostics.AddError("Failed to list licenses", err.Error())
				return
			}

			if err != nil {
				resp.Diagnostics.AddError("Failed to list licenses", err.Error())
				return
			}

			for _, assignment := range assignments.Items {
				usersForLicense[assignment.UserId] = true
			}

			if assignments.NextPageToken == "" {
				break
			}

			call.PageToken(assignments.NextPageToken)
		}

		licenseCache.Lock()
		licenseCache.data[cacheKey] = usersForLicense
	}

	licenseCache.Unlock()

	if !usersForLicense[userID] {
		resp.State.RemoveResource(ctx)
		return
	}

	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
}

func (r *LicenseResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan LicenseResourceModel
	var state LicenseResourceModel

	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	if state.ProductID.ValueString() != plan.ProductID.ValueString() {
		resp.Diagnostics.AddError(
			"Product update not supported",
			"Changing product_id is not supported. Delete and recreate the license assignment instead.",
		)
		return
	}

	if state.UserID.ValueString() != plan.UserID.ValueString() {
		resp.Diagnostics.AddError(
			"User update not supported",
			"Changing user_id is not supported. Delete and recreate the license assignment instead.",
		)
		return
	}

	service, ok := r.newLicensingService(ctx, &resp.Diagnostics)
	if !ok {
		return
	}

	_, err := service.LicenseAssignments.Update(
		plan.ProductID.ValueString(),
		plan.SKUID.ValueString(),
		plan.UserID.ValueString(),
		&admin.LicenseAssignment{
			UserId: plan.UserID.ValueString(),
		},
	).Do()

	if err != nil {
		resp.Diagnostics.AddError("Failed to update license", err.Error())
		return
	}

	removeUserFromCache(state.ProductID.ValueString(), state.SKUID.ValueString(), state.UserID.ValueString())
	addUserToCache(plan.ProductID.ValueString(), plan.SKUID.ValueString(), plan.UserID.ValueString())

	plan.ID = types.StringValue(fmt.Sprintf("%s/%s/%s",
		plan.ProductID.ValueString(),
		plan.SKUID.ValueString(),
		plan.UserID.ValueString(),
	))

	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
}

func (r *LicenseResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state LicenseResourceModel

	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}

	service, ok := r.newLicensingService(ctx, &resp.Diagnostics)
	if !ok {
		return
	}

	_, err := service.LicenseAssignments.Delete(
		state.ProductID.ValueString(),
		state.SKUID.ValueString(),
		state.UserID.ValueString(),
	).Do()

	if err != nil {
		resp.Diagnostics.AddError("Failed to delete license", err.Error())
		return
	}

	removeUserFromCache(state.ProductID.ValueString(), state.SKUID.ValueString(), state.UserID.ValueString())
}

func (r *LicenseResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	resource.ImportStatePassthroughID(ctx, path.Root("id"), req, resp)
}

func (r *LicenseResource) newLicensingService(ctx context.Context, diagnostics interface {
	AddError(summary string, detail string)
}) (*admin.Service, bool) {
	jsonKey := []byte(r.config.ServiceAccountKey.ValueString())

	creds, err := google.CredentialsFromJSONWithParams(ctx, jsonKey, google.CredentialsParams{
		Scopes: []string{
			"https://www.googleapis.com/auth/apps.licensing",
		},
		Subject: r.config.ImpersonatedAdmin.ValueString(),
	})
	if err != nil {
		diagnostics.AddError("Google credentials creation failed", err.Error())
		return nil, false
	}

	service, err := admin.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		diagnostics.AddError("Google client creation failed", err.Error())
		return nil, false
	}

	return service, true
}

func buildCacheKey(productID string, skuID string) string {
	return productID + "/" + skuID
}

func addUserToCache(productID string, skuID string, userID string) {
	cacheKey := buildCacheKey(productID, skuID)

	licenseCache.Lock()
	defer licenseCache.Unlock()

	if licenseCache.data[cacheKey] != nil {
		licenseCache.data[cacheKey][userID] = true
	}
}

func removeUserFromCache(productID string, skuID string, userID string) {
	cacheKey := buildCacheKey(productID, skuID)

	licenseCache.Lock()
	defer licenseCache.Unlock()

	if licenseCache.data[cacheKey] != nil {
		delete(licenseCache.data[cacheKey], userID)
	}
}
