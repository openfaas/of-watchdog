package api.oidc

# note that cache is per replica and will be lost when scaled down
metadata_discovery(issuer_url) = http.send({
	"url": concat("", [issuer_url, "/.well-known/openid-configuration"]),
	"method": "GET",
	"force_cache": true,
	"force_cache_duration_seconds": 86400,
}).body

# Cache response for 24 hours
jwks_request(url) = http.send({
	"url": url,
	"method": "GET",
	"force_cache": true,
	"force_cache_duration_seconds": 3600, # Cache response for an hour
})

default allow = false

# Below we extract various data from the input

# here load from a secret
# assume that you have a secret named issuers and it contains a
# json object of allow issuer urls
# {
#     "https://url1": true,
#     "https://url2": true,
#     "https://url3": false
# }
# the function is then configured with OPA_INPUT_SECRETS=oidc_issuers
#
# this could also be passed as a csv string of urls
#   OPA_INPUT_OIDC_ISSUERS=https://url1,https://url2,https://url3
# and then using
#   issuers := split(input.data.oidc_issuers, ",")
# to get the list of issuers
issuers := json.unmarshal(input.data.oidc_issuers)

# the next two are simpler and loaded directly from env vars
# assume you have set OPA_INPUT_ALLOWED_DOMAINS='{*@company_name.com,*@company_name2.com}'
# should be a valid glob https://www.openpolicyagent.org/docs/latest/policy-reference/#glob
allowed_domains := input.data.allowed_domains

# assume you have set OPA_INPUT_EMAIL_FIELD='email'
email_field := input.data.email_field

# Api/user auth via oauth
# Verifies the provided JWT against a list of known providers.
# Extracts pre-configured fields from the token. All configurations
# for an issuer are checked against the available fields in the
# token. If more than one configuration matches, one is chosen
# arbitrarily.
allow {
	print("attempt client-credentials auth")
	token := trim_prefix(input.authorization, "Bearer ")

	# note that the token is _NOT_ verified yet, but we need
	# to extract the issuer to perform the JWKS request and
	# then verify the token.
	# you should have a strict allow list of issuers otherwise
	# this is insecure.
	claims := io.jwt.decode(token)[1]

	print("Look up issuer in issuers list")

	# check is issuers is in the allow list dictionary and it is not false
	issuers[claims.iss]

	print("fetch metadata")
	metadata := metadata_discovery(claims.iss)

	print("fetch certificates to verify signature")
	jwks_endpoint := metadata.jwks_uri
	jwks := jwks_request(jwks_endpoint).raw_body

	print("verify signature")
	opts := object.union(issuers[idx].token_data, {"cert": jwks})
	[verified, _, _] := io.jwt.decode_verify(token, opts)

	# stops here if false
	verified

	# stops here if email is not in the claims
	email = claims[email_field]

	# and it must match the allowed domains glob
	# see https://www.openpolicyagent.org/docs/latest/policy-reference/#glob
	glob.match(allowed_domains, null, email)
}
