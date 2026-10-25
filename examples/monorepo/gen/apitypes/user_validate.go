package apitypes

import (
	"strings"

	"github.com/franco-grobler/go-api-gen/runtime/validate"
)

// ValidateEmailCorporateDomain implements the corporate_domain rule declared
// with x-validate on User.email.
//
// This file is hand-written. The generator declares UserValidator and asserts
// that User satisfies it, so deleting this method breaks the build rather than
// silently dropping the check.
func (u User) ValidateEmailCorporateDomain(v *validate.V) {
	if !strings.HasSuffix(u.Email, "@acme.com") {
		v.Fail("corporate_domain", "Email must be on the acme.com domain",
			"domain", "acme.com")
	}
}
