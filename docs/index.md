# Google Workspace Provider

Terraform provider for managing Google Workspace licenses and assignments.

## Example Usage

```hcl
provider "googleworkspace" {
  credentials             = file("credentials.json")
  impersonated_user_email = "admin@example.com"
}

## Authentication

This provider uses a Google service account with domain-wide delegation enabled.

### Required

- `credentials` (String, Sensitive) The JSON key of the service account with domain-wide delegation enabled.
- `impersonated_user_email` (String) The email of the user to impersonate when making API calls.

### Optional

- `customer_id` (String) The ID of the Google Workspace customer.

## Resources

googleworkspace_user_licenses

