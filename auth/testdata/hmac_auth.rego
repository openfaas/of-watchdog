package api.authz.hmac

default allow = false

allow {
	input.path = "/api/private"
	valid_signature
}

valid_signature {
	authTimestamp = input.headers["X-Auth-Timestamp"][0]
	data := sprintf(
		"ts=%s,path=%s,method=%s,body=%s",
		[
			authTimestamp,
			input.path,
			input.method,
			input.rawBody,
		],
	)

	# we assume secretKey is read read from a single secret
	generated := crypto.hmac.sha256(data, input.data.secretKey)

	constant_compare(generated, input.authorization)
}
