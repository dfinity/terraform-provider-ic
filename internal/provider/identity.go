// Copyright (c) DFINITY Foundation

package provider

import (
	"errors"

	"github.com/aviate-labs/agent-go/identity"
)

// NewIdentityFromPEN reads a PEM file and tries to create an Identity from it.
func NewIdentityFromPEM(data []byte) (identity.Identity, error) {

	var errs []error

	ed25519Identity, err := identity.NewEd25519IdentityFromPEM(data)
	if err == nil {
		return *ed25519Identity, nil
	}
	errs = append(errs, err)

	secp256k1Identity, err := identity.NewSecp256k1IdentityFromPEM(data)
	if err == nil {
		return *secp256k1Identity, nil
	}
	errs = append(errs, err)

	prime256v1Identity, err := identity.NewPrime256v1IdentityFromPEM(data)
	if err == nil {
		return *prime256v1Identity, nil
	}
	errs = append(errs, err)

	return nil, errors.Join(errs...)
}
