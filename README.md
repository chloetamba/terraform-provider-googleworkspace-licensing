# Terraform Provider Google Workspace Licensing

Custom Terraform provider for managing Google Workspace license assignments.

## Overview

This provider enables Google Workspace licensing operations to be managed through Terraform.

It currently supports assigning and removing Workspace licenses for users using the Google Enterprise License Manager API.

## Features

- User license assignment
- User license removal
- Terraform state management for Workspace licensing
- Infrastructure as Code approach for licensing operations

## Supported Resource

- `googleworkspace_user_license`

## Project Status

This provider is currently under active development and may introduce breaking changes before a stable `v1.0.0` release.

## License

MPL-2.0