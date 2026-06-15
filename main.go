package main

import (
	"context"
	"log"

	"terraform-provider-googleworkspace-licensing/internal/provider"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

func main() {
	err := providerserver.Serve(
		context.Background(),
		provider.New,
		providerserver.ServeOpts{
			Address: "registry.terraform.io/local/googleworkspace-licensing",
		},
	)

	if err != nil {
		log.Fatal(err)
	}
}
