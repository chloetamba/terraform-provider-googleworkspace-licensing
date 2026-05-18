# googleworkspace_user_license (Resource)

Manages a Google Workspace license assignment for a user.

This resource assigns a Google Workspace product SKU to a user.

### Required


## Example Usage

```hcl
resource "googleworkspace_user_license" "example" {
  user_id    = "user@example.com"
  product_id = "Google-Apps"
  sku_id     = "1010470001"
}

```