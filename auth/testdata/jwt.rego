# api.jwt represents any JWT based authentication
# this may or may not be a JWT from an OAuth provider.
# When possible, prefer OIDC when implementing OAuth
# flows.
package api.jwt

default allow = {"allow": false}

# Below we extract various data from the input

# the next two are simpler and loaded directly from env vars
# assume you have set OPA_INPUT_ALLOWED_DOMAINS='{*@company_name.com,*@company_name2.com}'
# be careful with whitespace,
# {*@company_name.com,*@company_name2.com} is not equal to {*@company_name.com, *@company_name2.com}
# in the second, the space after the comma is part of the second glob pattern
# should be a valid glob https://www.openpolicyagent.org/docs/latest/policy-reference/#glob
allowed_domains := input.data.allowed_domains

# assume you have set OPA_INPUT_EMAIL_FIELD='email'
email_field := input.data.email_field

# alias the now value to make rego unit testing easier
now = value {
	value := time.now_ns()
}

# Api/user auth via oauth
# Verifies the provided JWT against a list of known providers.
# Extracts pre-configured fields from the token. All configurations
# for an issuer are checked against the available fields in the
# token. If more than one configuration matches, one is chosen
# arbitrarily.
allow = response {
	print("attempt bearer token auth")
	token := trim_prefix(input.authorization, "Bearer ")

	# here load from a secret
	# the jwt key is loaded from a secret by setting OPA_INPUT_SECRETS=jwt_key
	# you can also pass a JWKS set via the "cert".
	# You can pass a PEM encoded key via the "cert"
	# This is required for the RS*, PS*, and ES* algorithms.
	# A full description of the options can be found
	# https://www.openpolicyagent.org/docs/latest/policy-reference/#tokens
	opts := {
		# You can also pass a plaintext secret when using HS256, HS384 and HS512 verification.
		"secret": input.data.jwt_key,
		# The time in nanoseconds to verify the token at. If this is present then the exp and nbf claims are compared against this value.
		"time": now,
	}

	print("verify signature")
	[verified, _, claims] := io.jwt.decode_verify(token, opts)

	# stops here if false
	# print("verified: ", verified)
	verified

	# print("claims: ", input)
	# print("email_field: ", email_field)

	# stops here if email is not in the claims
	email = claims[email_field]

	# print("found email: ", email)
	# print("validating against glob: ", allowed_domains)

	# and it must match the allowed domains glob
	# see https://www.openpolicyagent.org/docs/latest/policy-reference/#glob
	glob.match(allowed_domains, null, email)

	response := {
		"allow": true,
		"headers": {"X-User-Email": email},
	}
}
